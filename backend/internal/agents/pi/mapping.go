package pi

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/lmy375/agent-web/backend/internal/protocol"
)

// toolKinds is the rendering category for pi's built-in tools. An extension's
// tool keeps its own name and renders as "other".
var toolKinds = map[string]protocol.ToolKind{
	"bash": protocol.ToolShell, "powershell": protocol.ToolShell,
	"edit":  protocol.ToolFileEdit,
	"write": protocol.ToolFileWrite,
	"read":  protocol.ToolFileRead, "ls": protocol.ToolFileRead,
	"grep": protocol.ToolSearch, "find": protocol.ToolSearch,
}

func toolKind(name string) protocol.ToolKind {
	if kind, ok := toolKinds[name]; ok {
		return kind
	}
	return protocol.ToolOther
}

// contentPart is one element of a message's content array: text, thinking or
// a tool call on an assistant message, text or an image elsewhere.
type contentPart struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Data      string          `json:"data"`
	MimeType  string          `json:"mimeType"`
}

// messageContent is a content field that pi writes either as a bare string or
// as an array of parts.
type messageContent []contentPart

func (c *messageContent) UnmarshalJSON(raw []byte) error {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		*c = messageContent{{Type: "text", Text: text}}
		return nil
	}
	var parts []contentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return err
	}
	*c = parts
	return nil
}

type usage struct {
	Input       int  `json:"input"`
	Output      int  `json:"output"`
	CacheRead   int  `json:"cacheRead"`
	CacheWrite  int  `json:"cacheWrite"`
	Reasoning   *int `json:"reasoning"`
	TotalTokens int  `json:"totalTokens"`
	Cost        struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

// contextTokens is pi's own reading of how much of the window an assistant
// message left occupied.
func contextTokens(u usage) int {
	if u.TotalTokens > 0 {
		return u.TotalTokens
	}
	return u.Input + u.Output + u.CacheRead + u.CacheWrite
}

// agentMessage is one message as pi streams it in message_end and stores it in
// the session file. The roles this build renders are user, assistant and
// toolResult; the fields a role does not use stay zero.
type agentMessage struct {
	Role         string         `json:"role"`
	Content      messageContent `json:"content"`
	Usage        *usage         `json:"usage"`
	StopReason   string         `json:"stopReason"`
	ErrorMessage string         `json:"errorMessage"`
	Provider     string         `json:"provider"`
	Model        string         `json:"model"`
	Timestamp    int64          `json:"timestamp"`
	ToolCallID   string         `json:"toolCallId"`
	ToolName     string         `json:"toolName"`
	IsError      bool           `json:"isError"`
}

// id derives the message id from the creation timestamp pi stamps on every
// message, which is the one value the live stream and the session file share.
// A tool result is keyed by its call instead, so parallel results cannot
// collide.
func (m agentMessage) id() string {
	switch m.Role {
	case "assistant":
		return "a-" + strconv.FormatInt(m.Timestamp, 10)
	case "toolResult":
		return m.ToolCallID
	}
	return "u-" + strconv.FormatInt(m.Timestamp, 10)
}

func (m agentMessage) at() time.Time { return time.UnixMilli(m.Timestamp) }

// messageEntry is the one place a pi message becomes a transcript entry, so
// history and the live stream cannot render the same message differently.
// The roles pi keeps for itself -- bashExecution, custom, branchSummary,
// compactionSummary -- have no place in the transcript and report false, and
// so does an assistant message with nothing in it, which is what a request
// that failed or was aborted before its first token leaves behind.
func messageEntry(threadID string, m agentMessage, clientMessageID *string) (protocol.TranscriptEntry, bool) {
	switch m.Role {
	case "user":
		return protocol.UserMessage(threadID, m.id(), userBlocks(m.Content), clientMessageID, nil).At(m.at()), true
	case "assistant":
		if len(m.Content) == 0 {
			return nil, false
		}
		return protocol.AssistantMessage(threadID, m.id(), contentBlocks(m.Content), nil).At(m.at()), true
	case "toolResult":
		return protocol.ToolResult(threadID, m.ToolCallID, resultContent(m.Content), m.IsError).At(m.at()), true
	}
	return nil, false
}

func contentBlocks(parts []contentPart) []protocol.ContentBlock {
	out := make([]protocol.ContentBlock, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "text":
			out = append(out, protocol.Text(p.Text))
		case "thinking":
			out = append(out, protocol.Thinking(p.Thinking))
		case "toolCall":
			input := p.Arguments
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			out = append(out, protocol.ToolUseBlock{Type: "tool_use", ID: p.ID, Name: p.Name, ToolKind: toolKind(p.Name), Input: input})
		case "image":
			out = append(out, imageBlock(p))
		default:
			out = append(out, protocol.Text("["+p.Type+"]"))
		}
	}
	return out
}

func userBlocks(parts []contentPart) []protocol.UserBlock {
	out := make([]protocol.UserBlock, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "text":
			out = append(out, protocol.Text(p.Text))
		case "image":
			out = append(out, imageBlock(p))
		}
	}
	return out
}

func resultContent(parts []contentPart) []protocol.ContentBlock {
	out := make([]protocol.ContentBlock, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "text":
			out = append(out, protocol.Text(p.Text))
		case "image":
			out = append(out, imageBlock(p))
		}
	}
	return out
}

func imageBlock(p contentPart) protocol.ImageBlock {
	return protocol.ImageBlock{Type: "image", MediaType: p.MimeType, DataBase64: p.Data}
}

// promptImages is the images array of a prompt or steer command.
func promptImages(images []protocol.ImageBlock) []map[string]any {
	out := make([]map[string]any, 0, len(images))
	for _, image := range images {
		out = append(out, map[string]any{"type": "image", "data": image.DataBase64, "mimeType": image.MediaType})
	}
	return out
}

// classify reads a failed assistant message's error so the client can render
// and retry by code rather than by reading the message.
func classify(message string) protocol.StreamErrorCode {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "rate limit"), strings.Contains(lower, "rate_limit"),
		strings.Contains(lower, "429"), strings.Contains(lower, "overloaded"):
		return protocol.ErrRateLimited
	case strings.Contains(lower, "context"), strings.Contains(lower, "too long"),
		strings.Contains(lower, "exceeds"), strings.Contains(lower, "max_tokens"):
		return protocol.ErrContextOverflow
	case strings.Contains(lower, "401"), strings.Contains(lower, "unauthorized"),
		strings.Contains(lower, "api key"), strings.Contains(lower, "credential"),
		strings.Contains(lower, "authenticat"):
		return protocol.ErrCredential
	}
	return protocol.ErrOther
}

// model is the subset of pi's Model the descriptor and the context ring need.
type model struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	Reasoning     bool   `json:"reasoning"`
	ContextWindow int    `json:"contextWindow"`
}

// option is the value the model picker offers: pi addresses a model by
// provider and id together, and the same id exists under several providers.
func (m model) option() string { return m.Provider + "/" + m.ID }

// splitModel undoes option: the provider never contains a slash, the id may.
func splitModel(option string) (provider, modelID string) {
	provider, modelID, _ = strings.Cut(option, "/")
	return provider, modelID
}

// sessionState is the get_state response.
type sessionState struct {
	Model         *model `json:"model"`
	ThinkingLevel string `json:"thinkingLevel"`
	SessionFile   string `json:"sessionFile"`
}

// rpcCommand is one entry of the get_commands response: an extension command,
// a prompt template or a skill, invoked by sending "/<name>" as a prompt.
type rpcCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// uiRequest is an extension_ui_request. The blocking methods -- select,
// confirm, input, editor -- become questions; the rest need no answer.
type uiRequest struct {
	ID          string   `json:"id"`
	Method      string   `json:"method"`
	Title       string   `json:"title"`
	Message     string   `json:"message"`
	Placeholder string   `json:"placeholder"`
	Prefill     string   `json:"prefill"`
	Options     []string `json:"options"`
}

func (r uiRequest) blocking() bool {
	switch r.Method {
	case "select", "confirm", "input", "editor":
		return true
	}
	return false
}

// question renders the dialog as one question keyed by the request id. A
// confirm offers Yes and No; input and editor offer nothing and take the
// owner's own words.
func (r uiRequest) question() protocol.QuestionPayload {
	q := protocol.Question{ID: r.ID, Prompt: r.Title, Options: []protocol.QuestionOption{}}
	switch r.Method {
	case "select":
		for _, option := range r.Options {
			q.Options = append(q.Options, protocol.QuestionOption{Label: option})
		}
	case "confirm":
		if r.Message != "" {
			q.Prompt = r.Title + "\n" + r.Message
		}
		q.Options = []protocol.QuestionOption{{Label: "Yes"}, {Label: "No"}}
	case "input":
		if r.Placeholder != "" {
			q.Prompt = r.Title + "\n" + r.Placeholder
		}
	case "editor":
		if r.Prefill != "" {
			q.Prompt = r.Title + "\n" + r.Prefill
		}
	}
	return protocol.QuestionPayload{PayloadKind: protocol.InteractionQuestion, Questions: []protocol.Question{q}}
}

// uiResponse is the extension_ui_response line for a decision. Anything but
// an answer cancels the dialog, which the extension sees as its default.
func uiResponse(requestID, method string, d protocol.InteractionDecision) map[string]any {
	reply := map[string]any{"type": "extension_ui_response", "id": requestID}
	if d.Type != protocol.DecisionAnswer {
		reply["cancelled"] = true
		return reply
	}
	answer := ""
	if len(d.Answers) > 0 && len(d.Answers[0].Answers) > 0 {
		answer = d.Answers[0].Answers[0]
	}
	if method == "confirm" {
		reply["confirmed"] = answer == "Yes"
		return reply
	}
	reply["value"] = answer
	return reply
}

func ptr[T any](v T) *T { return &v }
