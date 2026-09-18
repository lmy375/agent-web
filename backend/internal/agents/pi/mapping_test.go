package pi

import (
	"encoding/json"
	"testing"

	"github.com/lmy375/agent-web/backend/internal/protocol"
)

func TestToolKinds(t *testing.T) {
	cases := map[string]protocol.ToolKind{
		"bash": protocol.ToolShell, "powershell": protocol.ToolShell,
		"edit": protocol.ToolFileEdit, "write": protocol.ToolFileWrite,
		"read": protocol.ToolFileRead, "ls": protocol.ToolFileRead,
		"grep": protocol.ToolSearch, "find": protocol.ToolSearch,
		"my_extension_tool": protocol.ToolOther,
	}
	for name, want := range cases {
		if got := toolKind(name); got != want {
			t.Errorf("toolKind(%q) = %q, want %q", name, got, want)
		}
	}
}

// pi writes a user message's content as a bare string in some entries and as
// an array of parts in others; both have to decode.
func TestMessageContentAcceptsStringAndArray(t *testing.T) {
	var fromString agentMessage
	if err := json.Unmarshal([]byte(`{"role":"user","content":"hello","timestamp":1}`), &fromString); err != nil {
		t.Fatal(err)
	}
	if len(fromString.Content) != 1 || fromString.Content[0].Text != "hello" {
		t.Fatalf("string content decoded as %+v", fromString.Content)
	}

	var fromArray agentMessage
	raw := `{"role":"user","content":[{"type":"text","text":"look"},{"type":"image","data":"AAAA","mimeType":"image/png"}],"timestamp":1}`
	if err := json.Unmarshal([]byte(raw), &fromArray); err != nil {
		t.Fatal(err)
	}
	blocks := userBlocks(fromArray.Content)
	if len(blocks) != 2 {
		t.Fatalf("array content decoded as %+v", blocks)
	}
	image, ok := blocks[1].(protocol.ImageBlock)
	if !ok || image.MediaType != "image/png" || image.DataBase64 != "AAAA" {
		t.Fatalf("image block is %+v", blocks[1])
	}
}

func TestMessageIDsAreTheSameLiveAndReplayed(t *testing.T) {
	cases := []struct {
		message agentMessage
		want    string
	}{
		{agentMessage{Role: "user", Timestamp: 1785915488178}, "u-1785915488178"},
		{agentMessage{Role: "assistant", Timestamp: 1785915490000}, "a-1785915490000"},
		{agentMessage{Role: "toolResult", ToolCallID: "call_7", Timestamp: 1785915491000}, "call_7"},
	}
	for _, c := range cases {
		if got := c.message.id(); got != c.want {
			t.Errorf("id of %s message = %q, want %q", c.message.Role, got, c.want)
		}
	}
	if _, ok := messageEntry("pi:t", agentMessage{Role: "bashExecution"}, nil); ok {
		t.Error("a bashExecution message has no transcript entry")
	}
	if _, ok := messageEntry("pi:t", agentMessage{Role: "assistant", StopReason: "error", Timestamp: 1}, nil); ok {
		t.Error("an assistant message that never produced content has no transcript entry")
	}
}

func TestContextTokensFollowPi(t *testing.T) {
	if got := contextTokens(usage{TotalTokens: 1200, Input: 1}); got != 1200 {
		t.Errorf("totalTokens should win, got %d", got)
	}
	if got := contextTokens(usage{Input: 100, Output: 20, CacheRead: 300, CacheWrite: 5}); got != 425 {
		t.Errorf("the four counters should add up to 425, got %d", got)
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]protocol.StreamErrorCode{
		"429 Too Many Requests":                     protocol.ErrRateLimited,
		"Provider overloaded, try again":            protocol.ErrRateLimited,
		"prompt exceeds the model's context window": protocol.ErrContextOverflow,
		"401 Unauthorized":                          protocol.ErrCredential,
		"Invalid API key":                           protocol.ErrCredential,
		"connection reset by peer":                  protocol.ErrOther,
	}
	for message, want := range cases {
		if got := classify(message); got != want {
			t.Errorf("classify(%q) = %q, want %q", message, got, want)
		}
	}
}

func TestSplitModel(t *testing.T) {
	provider, id := splitModel("openrouter/anthropic/claude-sonnet-4")
	if provider != "openrouter" || id != "anthropic/claude-sonnet-4" {
		t.Errorf("split at the first slash only, got %q / %q", provider, id)
	}
}

func TestUIResponses(t *testing.T) {
	yes := protocol.InteractionDecision{Type: protocol.DecisionAnswer,
		Answers: []protocol.QuestionAnswer{{QuestionID: "r1", Answers: []string{"Yes"}}}}
	if reply := uiResponse("r1", "confirm", yes); reply["confirmed"] != true {
		t.Errorf("a Yes answer confirms, got %v", reply)
	}
	typed := protocol.InteractionDecision{Type: protocol.DecisionAnswer,
		Answers: []protocol.QuestionAnswer{{QuestionID: "r2", Answers: []string{"main"}}}}
	if reply := uiResponse("r2", "input", typed); reply["value"] != "main" {
		t.Errorf("the owner's words travel as value, got %v", reply)
	}
	skipped := protocol.InteractionDecision{Type: protocol.DecisionDeny}
	if reply := uiResponse("r3", "select", skipped); reply["cancelled"] != true {
		t.Errorf("a deny cancels the dialog, got %v", reply)
	}
}
