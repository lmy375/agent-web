package opencode

import (
	"encoding/json"
	"strings"

	"github.com/lmy375/agent-web/backend/internal/protocol"
)

// defaultAgent is what a thread prompts with before the owner picks one, and
// what OpenCode itself treats as the default.
const defaultAgent = "build"

func agentFor(o protocol.ThreadOptions) string {
	if agent := o.Setting("agent"); agent != "" {
		return agent
	}
	return defaultAgent
}

// toolKinds maps OpenCode's built-in tool names onto rendering categories.
var toolKinds = map[string]protocol.ToolKind{
	"bash": protocol.ToolShell, "pty": protocol.ToolShell,
	"edit": protocol.ToolFileEdit, "patch": protocol.ToolFileEdit, "multiedit": protocol.ToolFileEdit,
	"write": protocol.ToolFileWrite,
	"read":  protocol.ToolFileRead, "glob": protocol.ToolFileRead, "list": protocol.ToolFileRead,
	"grep":      protocol.ToolSearch,
	"todowrite": protocol.ToolTodo, "todoread": protocol.ToolTodo,
	"task":     protocol.ToolSubagent,
	"webfetch": protocol.ToolWeb, "websearch": protocol.ToolWeb,
}

func toolKind(name string) protocol.ToolKind {
	if kind, ok := toolKinds[strings.ToLower(name)]; ok {
		return kind
	}
	// OpenCode names an MCP tool "<server>_<tool>"; there is no prefix that
	// marks one, so anything unknown is simply other.
	return protocol.ToolOther
}

// part is one piece of an OpenCode message.
type part struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	MessageID string `json:"messageID"`
	Type      string `json:"type"`
	Text      string `json:"text"`
	Synthetic bool   `json:"synthetic"`
	CallID    string `json:"callID"`
	Tool      string `json:"tool"`
	Mime      string `json:"mime"`
	URL       string `json:"url"`
	State     *struct {
		Status string          `json:"status"`
		Input  json.RawMessage `json:"input"`
		Output string          `json:"output"`
		Title  string          `json:"title"`
		Error  string          `json:"error"`
	} `json:"state"`
}

// message is the info half of an OpenCode message.
type message struct {
	ID         string  `json:"id"`
	SessionID  string  `json:"sessionID"`
	Role       string  `json:"role"`
	ProviderID string  `json:"providerID"`
	ModelID    string  `json:"modelID"`
	Cost       float64 `json:"cost"`
	Tokens     *struct {
		Total     float64 `json:"total"`
		Input     float64 `json:"input"`
		Output    float64 `json:"output"`
		Reasoning float64 `json:"reasoning"`
		Cache     struct {
			Read  float64 `json:"read"`
			Write float64 `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
	Error json.RawMessage `json:"error"`
	Time  struct {
		Created   int64 `json:"created"`
		Completed int64 `json:"completed"`
	} `json:"time"`
}

func (m message) usage() *protocol.Usage {
	if m.Tokens == nil {
		return nil
	}
	reasoning := int(m.Tokens.Reasoning)
	return &protocol.Usage{
		InputTokens:      int(m.Tokens.Input),
		OutputTokens:     int(m.Tokens.Output),
		CacheReadTokens:  int(m.Tokens.Cache.Read),
		CacheWriteTokens: int(m.Tokens.Cache.Write),
		ReasoningTokens:  &reasoning,
	}
}

// contentBlocks renders one message's parts as the protocol's block list.
func contentBlocks(parts []part) []protocol.ContentBlock {
	out := []protocol.ContentBlock{}
	for _, p := range parts {
		switch p.Type {
		case "text":
			if p.Text != "" && !p.Synthetic {
				out = append(out, protocol.Text(p.Text))
			}
		case "reasoning":
			if p.Text != "" {
				out = append(out, protocol.Thinking(p.Text))
			}
		case "tool":
			out = append(out, toolUseBlock(p))
		}
	}
	return out
}

func userBlocks(parts []part) []protocol.UserBlock {
	out := []protocol.UserBlock{}
	for _, p := range parts {
		switch {
		case p.Type == "text" && p.Text != "" && !p.Synthetic:
			out = append(out, protocol.Text(p.Text))
		case p.Type == "file" && strings.HasPrefix(p.Mime, "image/"):
			if _, data, ok := strings.Cut(p.URL, ";base64,"); ok {
				out = append(out, protocol.ImageBlock{Type: "image", MediaType: p.Mime, DataBase64: data})
			}
		}
	}
	return out
}

func toolUseBlock(p part) protocol.ToolUseBlock {
	input := json.RawMessage("{}")
	if p.State != nil && len(p.State.Input) > 0 {
		input = p.State.Input
	}
	return protocol.ToolUseBlock{
		Type: "tool_use", ID: p.CallID, Name: p.Tool, ToolKind: toolKind(p.Tool), Input: input,
	}
}

// toolResult is what a finished tool part produced.
func toolResult(p part) (string, bool) {
	if p.State == nil {
		return "", false
	}
	if p.State.Error != "" {
		return p.State.Error, true
	}
	return p.State.Output, p.State.Status == "error"
}

// sessionError classifies the error object OpenCode attaches to a failed turn.
func sessionError(raw json.RawMessage) (protocol.StreamErrorCode, string) {
	var failure struct {
		Name string `json:"name"`
		Data struct {
			Message string `json:"message"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw, &failure)
	message := failure.Data.Message
	if message == "" {
		message = failure.Name
	}
	switch failure.Name {
	case "ProviderAuthError":
		return protocol.ErrCredential, message
	case "ContextOverflowError", "MessageOutputLengthError":
		return protocol.ErrContextOverflow, message
	case "MessageAbortedError":
		return "", "" // An abort is the owner's own interrupt, not an error.
	}
	if strings.Contains(strings.ToLower(message), "rate limit") {
		return protocol.ErrRateLimited, message
	}
	return protocol.ErrOther, message
}

// modelID is the "<provider>/<model>" form the descriptor lists, so one string
// carries what OpenCode splits across two fields.
func splitModelID(id string) (provider, model string, ok bool) {
	provider, model, ok = strings.Cut(id, "/")
	return provider, model, ok && provider != "" && model != ""
}
