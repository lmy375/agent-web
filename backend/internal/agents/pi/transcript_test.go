package pi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lmy375/agent-web/backend/internal/protocol"
)

// A session file is a tree. Only the branch pi would continue -- from the last
// entry back to the root -- is history; ids and timestamps come from the
// messages themselves so a replay matches what the live stream published.
func TestTranscriptFollowsTheLiveBranch(t *testing.T) {
	lines := []string{
		`{"type":"session","version":3,"id":"019fd0db","timestamp":"2026-08-05T07:38:06.501Z","cwd":"/work"}`,
		`{"type":"model_change","id":"e67aa5c9","parentId":null,"timestamp":"2026-08-05T07:38:06.526Z","provider":"openai-codex","modelId":"gpt-5.5"}`,
		`{"type":"message","id":"m1","parentId":"e67aa5c9","timestamp":"2026-08-05T07:38:08.179Z","message":{"role":"user","content":[{"type":"text","text":"hello"}],"timestamp":1785915488178}}`,
		// A dead branch: an assistant answer pi later branched away from.
		`{"type":"message","id":"dead","parentId":"m1","timestamp":"2026-08-05T07:38:09.000Z","message":{"role":"assistant","content":[{"type":"text","text":"abandoned"}],"usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"total":0}},"stopReason":"stop","timestamp":1785915489000}}`,
		`{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-08-05T07:38:10.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Hi."},{"type":"toolCall","id":"call_1","name":"read","arguments":{"path":"README.md"}}],"usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"total":0}},"stopReason":"toolUse","timestamp":1785915490000}}`,
		`{"type":"message","id":"m3","parentId":"m2","timestamp":"2026-08-05T07:38:11.000Z","message":{"role":"toolResult","toolCallId":"call_1","toolName":"read","content":[{"type":"text","text":"# agent-web"}],"isError":false,"timestamp":1785915491000}}`,
		`{"type":"compaction","id":"c1","parentId":"m3","timestamp":"2026-08-05T07:39:00.000Z","summary":"...","firstKeptEntryId":"m3","tokensBefore":180000}`,
		`{"type":"message","id":"m4","parentId":"c1","timestamp":"2026-08-05T07:39:05.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Done."}],"usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"total":0}},"stopReason":"stop","timestamp":1785915545000}}`,
	}
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	page, err := readTranscript("pi:thread", path, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, entry := range page.Entries {
		switch e := entry.(type) {
		case protocol.UserMessageEvent:
			ids = append(ids, e.MessageID)
		case protocol.AssistantMessageEvent:
			ids = append(ids, e.MessageID)
			for _, block := range e.Blocks {
				if _, ok := block.(protocol.ToolUseBlock); ok {
					ids[len(ids)-1] += "+tool"
				}
			}
		case protocol.ToolResultEvent:
			ids = append(ids, e.ToolUseID)
		case protocol.ContextBoundaryEvent:
			if e.TokensBefore == nil || *e.TokensBefore != 180000 {
				t.Fatalf("compaction boundary is %+v", e)
			}
			ids = append(ids, "boundary")
		}
	}
	want := "u-1785915488178,a-1785915490000+tool,call_1,boundary,a-1785915545000"
	if got := strings.Join(ids, ","); got != want {
		t.Fatalf("replayed %s, want %s", got, want)
	}
	if page.NextCursor != nil {
		t.Fatalf("a full page has no older cursor, got %q", *page.NextCursor)
	}
	first := page.Entries[0].(protocol.UserMessageEvent)
	if first.Ts.UnixMilli() != 1785915488178 {
		t.Fatalf("entry time should be the message's own: %v", first.Ts)
	}

	// Paging: the cursor is pi's entry id, and `before` ends the page there.
	older, err := readTranscript("pi:thread", path, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(older.Entries) != 2 || older.NextCursor == nil || *older.NextCursor != "c1" {
		t.Fatalf("last page is %d entries with cursor %v", len(older.Entries), older.NextCursor)
	}
	previous, err := readTranscript("pi:thread", path, *older.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(previous.Entries) != 2 || previous.NextCursor == nil || *previous.NextCursor != "m2" {
		t.Fatalf("previous page is %d entries with cursor %v", len(previous.Entries), previous.NextCursor)
	}
	unknown, err := readTranscript("pi:thread", path, "nope", 2)
	if err != nil || len(unknown.Entries) != 0 || unknown.NextCursor != nil {
		t.Fatalf("an unknown cursor yields an empty page, got %+v (%v)", unknown, err)
	}
}

func TestMissingSessionFileIsEmptyHistory(t *testing.T) {
	page, err := readTranscript("pi:thread", filepath.Join(t.TempDir(), "never-written.jsonl"), "", 50)
	if err != nil || len(page.Entries) != 0 || page.NextCursor != nil {
		t.Fatalf("got %+v, %v", page, err)
	}
}
