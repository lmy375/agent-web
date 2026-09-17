package claudecode

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/lmy375/agent-web/backend/internal/protocol"
)

// projectSlug is how the CLI names a working directory's transcript folder:
// every character that is not a letter or a digit becomes a hyphen.
func projectSlug(cwd string) string {
	var b strings.Builder
	for _, r := range cwd {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func transcriptPath(cwd, sessionID string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects", projectSlug(cwd), sessionID+".jsonl")
}

// transcriptLine is the subset of a JSONL entry this reader understands. The
// file also holds attachments, queue operations, file snapshots and hook
// records, none of which are transcript in the protocol's sense.
type transcriptLine struct {
	Type        string     `json:"type"`
	Subtype     string     `json:"subtype"`
	UUID        string     `json:"uuid"`
	Timestamp   time.Time  `json:"timestamp"`
	IsSidechain bool       `json:"isSidechain"`
	Content     string     `json:"content"`
	Message     rawMessage `json:"message"`
	Compact     *struct {
		Trigger   string `json:"trigger"`
		PreTokens *int   `json:"pre_tokens"`
	} `json:"compactMetadata"`
}

// readTranscript maps the harness's own JSONL onto the protocol's four settled
// entry types, so history and a live stream render identically.
func readTranscript(threadID, cwd, sessionID string, before string, limit int) (protocol.TranscriptPage, error) {
	path := transcriptPath(cwd, sessionID)
	if path == "" || sessionID == "" {
		return protocol.TranscriptPage{Entries: []protocol.TranscriptEntry{}}, nil
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		// A thread that has never had a turn has no file yet.
		return protocol.TranscriptPage{Entries: []protocol.TranscriptEntry{}}, nil
	}
	if err != nil {
		return protocol.TranscriptPage{}, protocol.Errorf(protocol.CodeInternal, "cannot read transcript: %v", err)
	}
	defer file.Close()

	type keyed struct {
		id    string
		entry protocol.TranscriptEntry
	}
	entries := []keyed{}

	// The CLI writes one `assistant` line per content block, all under the same
	// message id, with the `user` lines carrying tool results in between; an
	// assistant_message carries the whole content array, so a later line grows
	// the entry the first one opened rather than starting its own.
	assistantAt := map[string]int{}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), maxLineBytes)
	for scanner.Scan() {
		var line transcriptLine
		if json.Unmarshal(scanner.Bytes(), &line) != nil || line.IsSidechain {
			// A sidechain is a subagent's own conversation; it is shown live
			// under its Task tool call and has no standalone place in history.
			continue
		}
		if line.Type == "assistant" {
			blocks := contentBlocks(line.Message.blocks())
			if at, opened := assistantAt[line.Message.ID]; opened && line.Message.ID != "" {
				grown := entries[at].entry.(protocol.AssistantMessageEvent)
				grown.Blocks = append(grown.Blocks, blocks...)
				entries[at].entry = grown
				continue
			}
			assistantAt[line.Message.ID] = len(entries)
			message := protocol.AssistantMessage(threadID, line.Message.ID, blocks, nil).At(line.Timestamp)
			entries = append(entries, keyed{line.UUID, message})
			continue
		}
		for _, entry := range transcriptEntries(threadID, line) {
			entries = append(entries, keyed{line.UUID, entry})
		}
	}

	// The whole file is in memory either way, so paging back from `before` is
	// a slice; both harnesses store one file per thread and neither indexes it.
	end := len(entries)
	if before != "" {
		for i, e := range entries {
			if e.id == before {
				end = i
				break
			}
		}
	}
	start := end - limit
	if start < 0 {
		start = 0
	}
	page := protocol.TranscriptPage{Entries: make([]protocol.TranscriptEntry, 0, end-start)}
	for _, e := range entries[start:end] {
		page.Entries = append(page.Entries, e.entry)
	}
	if start > 0 {
		cursor := entries[start].id
		page.NextCursor = &cursor
	}
	return page, nil
}

func transcriptEntries(threadID string, line transcriptLine) []protocol.TranscriptEntry {
	switch line.Type {
	case "user":
		blocks := line.Message.blocks()
		if len(blocks) == 1 && blocks[0].Type == "text" {
			if entries, ok := localCommandEntries(threadID, line, blocks[0].Text); ok {
				return entries
			}
		}
		out := []protocol.TranscriptEntry{}
		for _, block := range blocks {
			if block.Type == "tool_result" {
				out = append(out, protocol.ToolResult(threadID, block.ToolUseID,
					resultContent(block.Content), block.IsError).At(line.Timestamp))
			}
		}
		if visible := userBlocks(blocks); len(visible) > 0 {
			out = append(out, protocol.UserMessage(threadID, line.UUID, visible, nil, nil).At(line.Timestamp))
		}
		return out
	case "system":
		if line.Subtype == "local_command" {
			entries, _ := localCommandEntries(threadID, line, line.Content)
			return entries
		}
		if line.Subtype != "compact_boundary" {
			return nil
		}
		reason := protocol.CompactAuto
		var tokens *int
		if line.Compact != nil {
			tokens = line.Compact.PreTokens
			if line.Compact.Trigger == "manual" {
				reason = protocol.CompactManual
			}
		}
		return []protocol.TranscriptEntry{protocol.ContextBoundary(threadID, reason, tokens).At(line.Timestamp)}
	}
	return nil
}

// The CLI persists local commands differently from their live replies: a
// synthetic user prompt, then stdout/stderr in a system record. Match complete
// envelopes so examples of these tags in ordinary messages stay untouched.
var (
	localCommandCaveat = regexp.MustCompile(`(?s)^<local-command-caveat>.*</local-command-caveat>$`)
	localCommandPrompt = regexp.MustCompile(`(?s)^<command-name>(.*?)</command-name>\s*<command-message>.*?</command-message>\s*<command-args>(.*?)</command-args>$`)
	localCommandOutput = regexp.MustCompile(`(?s)^<local-command-(stdout|stderr)>(.*)</local-command-(stdout|stderr)>$`)
)

func localCommandEntries(threadID string, line transcriptLine, text string) ([]protocol.TranscriptEntry, bool) {
	text = strings.TrimSpace(text)
	if localCommandCaveat.MatchString(text) {
		return nil, true
	}
	if match := localCommandPrompt.FindStringSubmatch(text); match != nil {
		command := strings.TrimSpace(match[1])
		if args := strings.TrimSpace(match[2]); args != "" {
			command += " " + args
		}
		return []protocol.TranscriptEntry{
			protocol.UserMessage(threadID, line.UUID, []protocol.UserBlock{protocol.Text(command)}, nil, nil).At(line.Timestamp),
		}, true
	}
	if match := localCommandOutput.FindStringSubmatch(text); match != nil && match[1] == match[3] {
		if strings.TrimSpace(match[2]) == "" {
			return nil, true
		}
		return []protocol.TranscriptEntry{
			protocol.AssistantMessage(threadID, line.UUID, []protocol.ContentBlock{protocol.Text(match[2])}, nil).At(line.Timestamp),
		}, true
	}
	return nil, false
}
