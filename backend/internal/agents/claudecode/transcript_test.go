package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lmy375/agent-web/backend/internal/protocol"
)

func TestLocalCommandHistory(t *testing.T) {
	const markdown = "## Context Usage\n\n| Category | Tokens |\n|---|---|\n| Messages | 20.3k |\n"
	const example = "Please explain <command-name>/context</command-name> and <local-command-stdout>output</local-command-stdout>."
	cases := []struct {
		name, kind, text, wantKind, wantText string
		array                                bool
	}{
		{"caveat is hidden", "user", "<local-command-caveat>Internal CLI notice</local-command-caveat>", "", "", false},
		{"command", "user", "<command-name>/context</command-name>\n <command-message>context</command-message>\n <command-args></command-args>", "user_message", "/context", false},
		{"arguments", "user", "<command-name>/model</command-name>\n <command-message>model</command-message>\n <command-args> sonnet </command-args>", "user_message", "/model sonnet", true},
		{"markdown output", "system", "<local-command-stdout>" + markdown + "</local-command-stdout>", "assistant_message", markdown, false},
		{"legacy user output", "user", "<local-command-stdout>" + markdown + "</local-command-stdout>", "assistant_message", markdown, true},
		{"error output", "system", "<local-command-stderr>Unknown model</local-command-stderr>", "assistant_message", "Unknown model", false},
		{"empty output", "system", "<local-command-stdout>\n </local-command-stdout>", "", "", false},
		{"ordinary text", "user", example, "user_message", example, false},
		{"quoted envelope", "user", "```xml\n<local-command-stdout>example</local-command-stdout>\n```", "user_message", "```xml\n<local-command-stdout>example</local-command-stdout>\n```", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line := transcriptLine{Type: tc.kind, UUID: "record-id", Timestamp: time.Unix(123, 0).UTC()}
			if tc.kind == "system" {
				line.Subtype, line.Content = "local_command", tc.text
			} else if tc.array {
				line.Message.Content, _ = json.Marshal([]rawBlock{{Type: "text", Text: tc.text}})
			} else {
				line.Message.Content, _ = json.Marshal(tc.text)
			}
			entries := transcriptEntries("thread-id", line)
			if tc.wantKind == "" {
				if len(entries) != 0 {
					t.Fatalf("expected no visible entry, got %+v", entries)
				}
				return
			}
			if len(entries) != 1 {
				t.Fatalf("expected one entry, got %+v", entries)
			}
			encoded, err := json.Marshal(entries[0])
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Type      string               `json:"type"`
				ThreadID  string               `json:"thread_id"`
				MessageID string               `json:"message_id"`
				Ts        time.Time            `json:"ts"`
				Blocks    []protocol.TextBlock `json:"blocks"`
			}
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if got.Type != tc.wantKind || len(got.Blocks) != 1 || got.Blocks[0].Text != tc.wantText {
				t.Fatalf("unexpected rendered entry: %s", encoded)
			}
			if got.MessageID != line.UUID || got.ThreadID != "thread-id" || !got.Ts.Equal(line.Timestamp) {
				t.Fatalf("history must preserve identity and timestamp: %s", encoded)
			}
		})
	}
}

// The CLI writes one `assistant` line per content block, under one message id,
// with the tool results of earlier blocks in between.
func TestAssistantLinesMergeByMessageID(t *testing.T) {
	const cwd = "/work/app"
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude", "projects", projectSlug(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := strings.Join([]string{
		`{"type":"assistant","uuid":"a1","message":{"id":"msg_1","content":[{"type":"thinking","thinking":"weighing it"}]}}`,
		`{"type":"assistant","uuid":"a2","message":{"id":"msg_1","content":[{"type":"text","text":"here is the plan"}]}}`,
		`{"type":"assistant","uuid":"a3","message":{"id":"msg_1","content":[{"type":"tool_use","id":"tu_a","name":"Bash","input":{}}]}}`,
		`{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_a","content":"done"}]}}`,
		`{"type":"assistant","uuid":"a4","message":{"id":"msg_1","content":[{"type":"tool_use","id":"tu_b","name":"Read","input":{}}]}}`,
		`{"type":"assistant","uuid":"a5","message":{"id":"msg_2","content":[{"type":"text","text":"done"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	page, err := readTranscript("thread-id", cwd, "session", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(page.Entries)
	if err != nil {
		t.Fatal(err)
	}
	var got []struct {
		Type      string `json:"type"`
		MessageID string `json:"message_id"`
		ToolUseID string `json:"tool_use_id"`
		Blocks    []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}

	if len(got) != 3 {
		t.Fatalf("expected the message, its tool result and the next message, got %s", encoded)
	}
	if got[0].Type != "assistant_message" || got[0].MessageID != "msg_1" {
		t.Fatalf("expected msg_1 first, got %+v", got[0])
	}
	want := []string{"thinking", "text", "tool_use tu_a", "tool_use tu_b"}
	if len(got[0].Blocks) != len(want) {
		t.Fatalf("expected %d blocks, got %s", len(want), encoded)
	}
	for i, block := range got[0].Blocks {
		name := block.Type
		if block.ID != "" {
			name += " " + block.ID
		}
		if name != want[i] {
			t.Fatalf("block %d is %q, want %q", i, name, want[i])
		}
	}
	if got[1].Type != "tool_result" || got[1].ToolUseID != "tu_a" {
		t.Fatalf("expected tu_a's result after the message, got %+v", got[1])
	}
	if got[2].Type != "assistant_message" || got[2].MessageID != "msg_2" {
		t.Fatalf("expected msg_2 to stay its own entry, got %+v", got[2])
	}
}
