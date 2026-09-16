package claudecode

import (
	"encoding/json"
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
