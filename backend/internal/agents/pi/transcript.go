package pi

import (
	"bufio"
	"encoding/json"
	"os"
	"slices"
	"time"

	"github.com/lmy375/agent-web/backend/internal/protocol"
)

// fileEntry is the subset of a session file line this reader understands. The
// file is a tree: every entry names its parent, and the conversation pi will
// continue is the path from the entry it wrote last back to the root. The
// other entry types -- model and thinking changes, labels, branch summaries,
// custom data -- are pi's own bookkeeping and not transcript.
type fileEntry struct {
	Type         string       `json:"type"`
	ID           string       `json:"id"`
	ParentID     *string      `json:"parentId"`
	Timestamp    time.Time    `json:"timestamp"`
	Message      agentMessage `json:"message"`
	TokensBefore int          `json:"tokensBefore"`
}

// readTranscript maps pi's session file onto the protocol's settled entry
// types, so history and a live stream render identically.
func readTranscript(threadID, path, before string, limit int) (protocol.TranscriptPage, error) {
	empty := protocol.TranscriptPage{Entries: []protocol.TranscriptEntry{}}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		// pi writes the file once the first assistant message exists, so a
		// thread that has not had a reply yet has no file.
		return empty, nil
	}
	if err != nil {
		return protocol.TranscriptPage{}, protocol.Errorf(protocol.CodeInternal, "cannot read session file: %v", err)
	}
	defer file.Close()

	entries := []fileEntry{}
	byID := map[string]int{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), maxLineBytes)
	for scanner.Scan() {
		var entry fileEntry
		if json.Unmarshal(scanner.Bytes(), &entry) != nil || entry.Type == "session" || entry.ID == "" {
			continue
		}
		byID[entry.ID] = len(entries)
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return protocol.TranscriptPage{}, protocol.Errorf(protocol.CodeInternal, "cannot read session file: %v", err)
	}

	// Walk from the last entry to the root; a branch pi abandoned is left out.
	branch := []fileEntry{}
	for at, ok := len(entries)-1, len(entries) > 0; ok && len(branch) < len(entries); {
		entry := entries[at]
		branch = append(branch, entry)
		if entry.ParentID == nil {
			break
		}
		at, ok = byID[*entry.ParentID]
	}
	slices.Reverse(branch)

	type keyed struct {
		id    string
		entry protocol.TranscriptEntry
	}
	settled := []keyed{}
	for _, entry := range branch {
		switch entry.Type {
		case "message":
			if mapped, ok := messageEntry(threadID, entry.Message, nil); ok {
				settled = append(settled, keyed{entry.ID, mapped})
			}
		case "compaction":
			// The file does not record what triggered a compaction, so a
			// manual one reads as automatic here.
			settled = append(settled, keyed{entry.ID,
				protocol.ContextBoundary(threadID, protocol.CompactAuto, ptr(entry.TokensBefore)).At(entry.Timestamp)})
		}
	}

	// The whole branch is in memory, so paging back from `before` is a slice.
	// The cursor is pi's entry id, unique by construction.
	end := len(settled)
	if before != "" {
		end = slices.IndexFunc(settled, func(k keyed) bool { return k.id == before })
		if end < 0 {
			return empty, nil
		}
	}
	start := max(end-limit, 0)
	page := protocol.TranscriptPage{Entries: make([]protocol.TranscriptEntry, 0, end-start)}
	for _, k := range settled[start:end] {
		page.Entries = append(page.Entries, k.entry)
	}
	if start > 0 {
		page.NextCursor = ptr(settled[start].id)
	}
	return page, nil
}
