package claudecode

import (
	"encoding/json"
	"strings"

	"github.com/lmy375/agent-web/backend/internal/protocol"
)

// toolKinds is the rendering category for the CLI's built-in tools; anything
// else falls through to the mcp__ prefix check and then to "other".
var toolKinds = map[string]protocol.ToolKind{
	"Bash": protocol.ToolShell, "BashOutput": protocol.ToolShell, "KillShell": protocol.ToolShell,
	"Edit": protocol.ToolFileEdit, "MultiEdit": protocol.ToolFileEdit, "NotebookEdit": protocol.ToolFileEdit,
	"Write": protocol.ToolFileWrite,
	"Read":  protocol.ToolFileRead, "Glob": protocol.ToolFileRead,
	"Grep":      protocol.ToolSearch,
	"TodoWrite": protocol.ToolTodo,
	"Task":      protocol.ToolSubagent, "Agent": protocol.ToolSubagent,
	"WebSearch": protocol.ToolWeb, "WebFetch": protocol.ToolWeb,
}

func toolKind(name string) protocol.ToolKind {
	if kind, ok := toolKinds[name]; ok {
		return kind
	}
	if strings.HasPrefix(name, "mcp__") {
		return protocol.ToolMCP
	}
	return protocol.ToolOther
}

// rawBlock is one entry of an Anthropic message's content array, in the shape
// both the stream and the on-disk transcript use.
type rawBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Source    struct {
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	} `json:"source"`
}

// rawMessage is the Anthropic message envelope the CLI wraps in every
// assistant and user line.
type rawMessage struct {
	ID      string          `json:"id"`
	Model   string          `json:"model"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Usage   *rawUsage       `json:"usage"`
}

// blocks decodes a content field that is either a bare string or an array.
func (m rawMessage) blocks() []rawBlock {
	var text string
	if json.Unmarshal(m.Content, &text) == nil {
		return []rawBlock{{Type: "text", Text: text}}
	}
	var list []rawBlock
	_ = json.Unmarshal(m.Content, &list)
	return list
}

func contentBlock(b rawBlock) protocol.ContentBlock {
	switch b.Type {
	case "text":
		return protocol.Text(b.Text)
	case "thinking":
		return protocol.Thinking(b.Thinking)
	case "image":
		return protocol.ImageBlock{Type: "image", MediaType: b.Source.MediaType, DataBase64: b.Source.Data}
	case "tool_use":
		input := b.Input
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		return protocol.ToolUseBlock{Type: "tool_use", ID: b.ID, Name: b.Name, ToolKind: toolKind(b.Name), Input: input}
	case "tool_result":
		return protocol.ToolResultBlock{
			Type: "tool_result", ToolUseID: b.ToolUseID,
			Content: resultContent(b.Content), IsError: b.IsError,
		}
	}
	// A block type this build does not know still has to render as something,
	// and its own type name is more use than an empty bubble.
	return protocol.Text("[" + b.Type + "]")
}

func contentBlocks(blocks []rawBlock) []protocol.ContentBlock {
	out := make([]protocol.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, contentBlock(b))
	}
	return out
}

// userBlocks keeps only what a user message can carry; a tool_result arrives as
// its own event rather than as part of the message.
func userBlocks(blocks []rawBlock) []protocol.UserBlock {
	out := []protocol.UserBlock{}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			out = append(out, protocol.Text(b.Text))
		case "image":
			out = append(out, protocol.ImageBlock{Type: "image", MediaType: b.Source.MediaType, DataBase64: b.Source.Data})
		}
	}
	return out
}

// resultContent decodes a tool result's content, which the CLI writes as a bare
// string for the common case and as a block array when it carries images.
func resultContent(raw json.RawMessage) []protocol.ContentBlock {
	if len(raw) == 0 {
		return []protocol.ContentBlock{}
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []protocol.ContentBlock{protocol.Text(text)}
	}
	var list []rawBlock
	if json.Unmarshal(raw, &list) != nil {
		return []protocol.ContentBlock{protocol.Text(string(raw))}
	}
	out := make([]protocol.ContentBlock, 0, len(list))
	for _, b := range list {
		switch b.Type {
		case "image":
			out = append(out, protocol.ImageBlock{Type: "image", MediaType: b.Source.MediaType, DataBase64: b.Source.Data})
		default:
			out = append(out, protocol.Text(b.Text))
		}
	}
	return out
}

type rawUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

func usage(raw *rawUsage) *protocol.Usage {
	if raw == nil {
		return nil
	}
	return &protocol.Usage{
		InputTokens:      raw.InputTokens,
		OutputTokens:     raw.OutputTokens,
		CacheReadTokens:  raw.CacheReadInputTokens,
		CacheWriteTokens: raw.CacheCreationInputTokens,
	}
}

// rawResult is the CLI's result line, which closes every turn.
type rawResult struct {
	Subtype      string    `json:"subtype"`
	IsError      bool      `json:"is_error"`
	TotalCostUSD *float64  `json:"total_cost_usd"`
	Usage        *rawUsage `json:"usage"`
	Result       string    `json:"result"`
	Errors       []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func turnSummary(r rawResult, interrupted bool) protocol.TurnSummary {
	status := protocol.TurnCompleted
	switch {
	case interrupted:
		status = protocol.TurnInterrupted
	case r.IsError || (r.Subtype != "" && r.Subtype != "success"):
		status = protocol.TurnFailed
	}
	return protocol.TurnSummary{Status: status, Usage: usage(r.Usage), CostUSD: r.TotalCostUSD}
}

// resultError classifies a failed turn so the client can render and retry by
// code rather than by reading the message.
func resultError(r rawResult) (protocol.StreamErrorCode, string, bool) {
	if !r.IsError && (r.Subtype == "" || r.Subtype == "success") {
		return "", "", false
	}
	message := r.Result
	if len(r.Errors) > 0 && r.Errors[0].Message != "" {
		message = r.Errors[0].Message
	}
	if message == "" {
		message = r.Subtype
	}
	lower := strings.ToLower(message + " " + r.Subtype)
	switch {
	case strings.Contains(lower, "rate limit"), strings.Contains(lower, "429"):
		return protocol.ErrRateLimited, message, true
	case strings.Contains(lower, "max_tokens"), strings.Contains(lower, "prompt is too long"),
		strings.Contains(lower, "context low"), strings.Contains(lower, "context_overflow"):
		return protocol.ErrContextOverflow, message, true
	case strings.Contains(lower, "authenticat"), strings.Contains(lower, "credential"),
		strings.Contains(lower, "oauth"), strings.Contains(lower, "401"):
		return protocol.ErrCredential, message, true
	}
	return protocol.ErrOther, message, true
}

// rawContextUsage is the get_context_usage control response.
type rawContextUsage struct {
	TotalTokens int `json:"totalTokens"`
	MaxTokens   int `json:"maxTokens"`
	Categories  []struct {
		Name   string `json:"name"`
		Tokens int    `json:"tokens"`
		Kind   string `json:"kind"`
	} `json:"categories"`
}

func contextUsage(raw rawContextUsage) protocol.ContextUsage {
	usage := protocol.ContextUsage{
		TotalTokens: raw.TotalTokens,
		MaxTokens:   raw.MaxTokens,
		Categories:  []protocol.ContextCategory{},
	}
	for _, c := range raw.Categories {
		// "free" and "buffer" are the window's remainder, not consumption; the
		// client's ring draws what is used, so they would double-count.
		if c.Kind == "free" || c.Kind == "buffer" {
			if c.Kind == "buffer" {
				threshold := raw.MaxTokens - c.Tokens
				usage.AutoCompactThresholdToken = &threshold
			}
			continue
		}
		usage.Categories = append(usage.Categories, protocol.ContextCategory{Name: c.Name, Tokens: c.Tokens})
	}
	return usage
}

// reportedModes is the CLI's other spelling for one of its own modes: the flag
// takes `manual`, and the session reports that same mode back as `default`.
// Every other mode round-trips under one name. One knob, two spellings, both
// the CLI's.
var reportedModes = map[string]string{"default": "manual"}

// modeAsFlag is a mode the CLI reported, under the name its flag takes.
func modeAsFlag(reported string) string {
	if flag, ok := reportedModes[reported]; ok {
		return flag
	}
	return reported
}

// serverInfo is the initialize control response: everything the descriptor's
// runtime half needs, answered by the CLI itself.
type serverInfo struct {
	Commands []struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		ArgumentHint string `json:"argument_hint"`
	} `json:"commands"`
	Models []struct {
		Value                 string   `json:"value"`
		DisplayName           string   `json:"displayName"`
		Description           string   `json:"description"`
		SupportedEffortLevels []string `json:"supportedEffortLevels"`
	} `json:"models"`
	CurrentPermissionMode string `json:"current_permission_mode"`
	Account               *struct {
		Email string `json:"email"`
	} `json:"account"`
}

func (s serverInfo) modelOptions() []protocol.ModelOption {
	out := make([]protocol.ModelOption, 0, len(s.Models))
	for _, m := range s.Models {
		option := protocol.ModelOption{ID: m.Value, Label: m.DisplayName}
		if m.Description != "" {
			description := m.Description
			option.Description = &description
		}
		out = append(out, option)
	}
	return out
}

// optionGroups is the CLI's own knobs. The effort levels are the ones the
// models themselves advertise; --permission-mode has no such list, so its
// choices are the ones `claude --help` prints, in the order it prints them.
func (s serverInfo) optionGroups() []protocol.OptionGroup {
	out := []protocol.OptionGroup{permissionModes}
	efforts := []protocol.OptionChoice{}
	seen := map[string]bool{}
	for _, model := range s.Models {
		for _, level := range model.SupportedEffortLevels {
			if !seen[level] {
				seen[level] = true
				efforts = append(efforts, protocol.OptionChoice{Value: level, Label: level})
			}
		}
	}
	if len(efforts) > 0 {
		out = append(out, protocol.OptionGroup{ID: "effort", Label: "effort", Options: efforts})
	}
	return out
}

func (s serverInfo) slashCommands() []protocol.SlashCommandInfo {
	out := make([]protocol.SlashCommandInfo, 0, len(s.Commands))
	for _, c := range s.Commands {
		out = append(out, protocol.SlashCommandInfo{Name: c.Name, Description: c.Description, ArgumentHint: c.ArgumentHint})
	}
	return out
}
