package codex

import (
	"encoding/json"
	"strings"

	"github.com/lmy375/agent-web/backend/internal/protocol"
)

// sandbox holds the knobs Codex splits a permission mode across. The sandbox
// itself is spelled two ways by the same server: thread/start takes the plain
// mode string, turn/start takes an internally tagged policy object.
type sandbox struct {
	approvalPolicy string
	sandboxMode    string
	policyType     string
}

// policy is the turn/start spelling of the same sandbox.
func (s sandbox) policy() map[string]any { return map[string]any{"type": s.policyType} }

// permissionModes is the protocol's vocabulary expressed in Codex's. Plan is
// absent: Codex has no plan mode, so the descriptor does not offer it.
var permissionModes = map[protocol.PermissionMode]sandbox{
	protocol.ModeAsk:      {"on-request", "workspace-write", "workspaceWrite"},
	protocol.ModeAutoEdit: {"never", "workspace-write", "workspaceWrite"},
	protocol.ModeFullAuto: {"never", "danger-full-access", "dangerFullAccess"},
	protocol.ModeDontAsk:  {"untrusted", "read-only", "readOnly"},
}

func nativeMode(mode *protocol.PermissionMode) sandbox {
	if mode != nil {
		if native, ok := permissionModes[*mode]; ok {
			return native
		}
	}
	return permissionModes[protocol.ModeAsk]
}

// itemToolKinds is the rendering category for each item type Codex reports as
// work rather than as a message.
var itemToolKinds = map[string]protocol.ToolKind{
	"commandExecution":    protocol.ToolShell,
	"fileChange":          protocol.ToolFileEdit,
	"read":                protocol.ToolFileRead,
	"listFiles":           protocol.ToolFileRead,
	"search":              protocol.ToolSearch,
	"findInPage":          protocol.ToolSearch,
	"plan":                protocol.ToolTodo,
	"mcpToolCall":         protocol.ToolMCP,
	"collabAgentToolCall": protocol.ToolSubagent,
	"subAgentActivity":    protocol.ToolSubagent,
	"webSearch":           protocol.ToolWeb,
	"openPage":            protocol.ToolWeb,
	"dynamicToolCall":     protocol.ToolOther,
	"skill":               protocol.ToolOther,
	"imageGeneration":     protocol.ToolOther,
	"functionCallOutput":  protocol.ToolOther,
}

// item is the shape shared by every item/started and item/completed payload.
// Only the fields this build renders are named; the rest stay in Raw so a tool
// card can still show the harness's own object.
type item struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Text string `json:"text"`
	// reasoning: the summary the UI is meant to show arrives as a list of
	// sections, and decoding it as anything else fails the whole item.
	Summary []string `json:"summary"`
	// userMessage
	ClientID string `json:"clientId"`
	Content  []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	// commandExecution
	Command          string `json:"command"`
	Cwd              string `json:"cwd"`
	AggregatedOutput string `json:"aggregatedOutput"`
	ExitCode         *int   `json:"exitCode"`
	Status           string `json:"status"`
	// mcpToolCall / dynamicToolCall
	Tool   string `json:"tool"`
	Server string `json:"server"`
	// contextCompaction
	Reason string `json:"reason"`

	Raw json.RawMessage `json:"-"`
}

// isTool reports whether this item renders as a tool call rather than a message.
func (i item) isTool() bool {
	_, ok := itemToolKinds[i.Type]
	return ok
}

func (i item) toolKind() protocol.ToolKind { return itemToolKinds[i.Type] }

// toolName is what the client shows on the card. Codex names the work by item
// type, except for MCP and dynamic tools which carry a real tool name.
func (i item) toolName() string {
	if i.Tool != "" {
		if i.Server != "" {
			return i.Server + "__" + i.Tool
		}
		return i.Tool
	}
	return i.Type
}

// toolInput is the card's parameter block, built from the fields that item type
// actually has, so the client renders one shape for every harness.
func (i item) toolInput() json.RawMessage {
	fields := map[string]any{}
	if i.Command != "" {
		fields["command"] = i.Command
	}
	if i.Cwd != "" {
		fields["cwd"] = i.Cwd
	}
	if i.Text != "" {
		fields["text"] = i.Text
	}
	if len(fields) == 0 && len(i.Raw) > 0 {
		return i.Raw
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return json.RawMessage("{}")
	}
	return encoded
}

// toolOutput is what the finished item produced.
func (i item) toolOutput() (string, bool) {
	if i.AggregatedOutput != "" {
		return i.AggregatedOutput, i.ExitCode != nil && *i.ExitCode != 0
	}
	failed := i.Status == "failed" || (i.ExitCode != nil && *i.ExitCode != 0)
	if i.Text != "" {
		return i.Text, failed
	}
	return string(i.Raw), failed
}

// reasoningText is what a reasoning item has to show. Codex puts the summary in
// `summary` and leaves `content` for the raw chain, which only some accounts
// are served, so an item with neither is one this client cannot render.
func (i item) reasoningText() string {
	if joined := strings.Join(i.Summary, "\n\n"); strings.TrimSpace(joined) != "" {
		return joined
	}
	return i.textContent()
}

func (i item) textContent() string {
	if i.Text != "" {
		return i.Text
	}
	parts := make([]string, 0, len(i.Content))
	for _, c := range i.Content {
		if c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "")
}

func (i item) userContent() []protocol.UserBlock {
	if text := i.textContent(); text != "" {
		return []protocol.UserBlock{protocol.Text(text)}
	}
	return []protocol.UserBlock{}
}

// tokenUsage is thread/tokenUsage/updated.
type tokenUsage struct {
	Total struct {
		TotalTokens          int `json:"totalTokens"`
		InputTokens          int `json:"inputTokens"`
		CachedInputTokens    int `json:"cachedInputTokens"`
		CacheWriteInputToken int `json:"cacheWriteInputTokens"`
		OutputTokens         int `json:"outputTokens"`
		ReasoningOutput      int `json:"reasoningOutputTokens"`
	} `json:"total"`
	Last struct {
		TotalTokens       int `json:"totalTokens"`
		InputTokens       int `json:"inputTokens"`
		CachedInputTokens int `json:"cachedInputTokens"`
		OutputTokens      int `json:"outputTokens"`
		ReasoningOutput   int `json:"reasoningOutputTokens"`
	} `json:"last"`
	ModelContextWindow int `json:"modelContextWindow"`
}

func (t tokenUsage) contextUsage() protocol.ContextUsage {
	return protocol.ContextUsage{
		TotalTokens: t.Total.TotalTokens,
		MaxTokens:   t.ModelContextWindow,
		Categories:  []protocol.ContextCategory{},
	}
}

func (t tokenUsage) turnUsage() *protocol.Usage {
	reasoning := t.Last.ReasoningOutput
	return &protocol.Usage{
		InputTokens:      t.Last.InputTokens,
		OutputTokens:     t.Last.OutputTokens,
		CacheReadTokens:  t.Last.CachedInputTokens,
		CacheWriteTokens: 0, // Codex reports cache writes only in the total.
		ReasoningTokens:  &reasoning,
	}
}

func turnStatus(status string) protocol.TurnStatus {
	switch status {
	case "completed":
		return protocol.TurnCompleted
	case "interrupted", "cancelled", "canceled":
		return protocol.TurnInterrupted
	default:
		return protocol.TurnFailed
	}
}

// modelEntry is one row of model/list.
type modelEntry struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
}

func modelOptions(entries []modelEntry) []protocol.ModelOption {
	out := make([]protocol.ModelOption, 0, len(entries))
	for _, e := range entries {
		label := e.DisplayName
		if label == "" {
			label = e.ID
		}
		option := protocol.ModelOption{ID: e.ID, Label: label}
		if e.Description != "" {
			description := e.Description
			option.Description = &description
		}
		out = append(out, option)
	}
	return out
}
