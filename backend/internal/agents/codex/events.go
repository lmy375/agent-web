package codex

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lmy375/agent-web/backend/internal/chat"
	"github.com/lmy375/agent-web/backend/internal/protocol"
)

// threadRef is the field every thread-scoped notification carries.
type threadRef struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Delta    string `json:"delta"`
}

// decodeItem parses one item payload, keeping the original JSON so a tool card
// for a type this build does not model can still show what the harness said.
func decodeItem(raw json.RawMessage) (item, bool) {
	var parsed item
	if json.Unmarshal(raw, &parsed) != nil || parsed.Type == "" {
		return item{}, false
	}
	parsed.Raw = raw
	return parsed, true
}

// onNotification turns one app-server notification into protocol events. The
// server is shared, so the first job is always finding whose thread this is.
func (b *Backend) onNotification(method string, params json.RawMessage) {
	var ref threadRef
	_ = json.Unmarshal(params, &ref)

	// Account-wide notices have no thread; they are worth a banner only when a
	// thread is open to hang one on.
	if method == "account/rateLimits/updated" {
		b.noticeEverywhere(protocol.NoticeRateLimit, rateLimitMessage(params))
		return
	}

	threadID, ok := b.resolve(ref.ThreadID)
	if !ok {
		return
	}
	t, ok := b.thread(threadID)
	if !ok {
		return
	}

	switch method {
	case "turn/started":
		var payload struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		_ = json.Unmarshal(params, &payload)
		t.mu.Lock()
		t.turnID = payload.Turn.ID
		clientMessageID := t.turn
		t.mu.Unlock()
		b.deps.Publish(protocol.TurnStarted(threadID, clientMessageID))
		b.setState(threadID, protocol.StateRunning)

	case "turn/completed":
		b.onTurnCompleted(threadID, t, params)

	case "item/started":
		b.onItemStarted(threadID, t, params)

	case "item/completed":
		b.onItemCompleted(threadID, t, params)

	case "item/agentMessage/delta":
		b.deps.Publish(protocol.TextDelta(threadID, ref.ItemID, 0, ref.Delta, nil))

	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		b.deps.Publish(protocol.ThinkingDelta(threadID, ref.ItemID, 0, ref.Delta, nil))

	case "item/commandExecution/outputDelta", "item/fileChange/outputDelta":
		b.deps.Publish(protocol.ToolOutputDelta(threadID, ref.ItemID, decodeOutputDelta(params)))

	case "thread/tokenUsage/updated":
		var payload struct {
			TokenUsage tokenUsage `json:"tokenUsage"`
		}
		if json.Unmarshal(params, &payload) == nil && payload.TokenUsage.ModelContextWindow > 0 {
			usage := payload.TokenUsage.contextUsage()
			t.mu.Lock()
			t.contextUsage, t.lastUsage = &usage, payload.TokenUsage.turnUsage()
			t.mu.Unlock()
			b.deps.Publish(protocol.ContextUsageChanged(threadID, usage))
		}

	case "thread/name/updated":
		var payload struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(params, &payload) == nil && payload.Name != "" {
			b.deps.Registry.Update(threadID, func(rec *chat.ThreadRecord) {
				if rec.Title == nil {
					name := payload.Name
					rec.Title = &name
				}
			})
		}

	case "thread/compacted":
		b.deps.Publish(protocol.ContextBoundary(threadID, protocol.CompactManual, nil))

	case "error":
		b.deps.Publish(protocol.StreamError(threadID, classifyError(params), errorMessage(params), false))
	}
}

func (b *Backend) onTurnCompleted(threadID string, t *thread, params json.RawMessage) {
	var payload struct {
		Turn struct {
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
	}
	_ = json.Unmarshal(params, &payload)

	b.resolveAllPending(threadID, t)

	t.mu.Lock()
	clientMessageID, interrupted := t.turn, t.interrupted
	t.turn, t.turnID, t.interrupted = "", "", false
	status := turnStatus(payload.Turn.Status)
	if interrupted {
		status = protocol.TurnInterrupted
	}
	// Codex reports no cost, so the summary carries only what it does report.
	summary := protocol.TurnSummary{Status: status, Usage: t.lastUsage}
	t.lastTurn = &summary
	t.mu.Unlock()

	if payload.Turn.Error != nil && !interrupted {
		b.deps.Publish(protocol.StreamError(threadID, protocol.ErrOther, payload.Turn.Error.Message, false))
	}
	b.deps.Publish(protocol.TurnFinished(threadID, clientMessageID, summary))
	b.setState(threadID, protocol.StateIdle)
	b.deps.Registry.Touch(threadID)
}

// onItemStarted opens a tool card or an assistant message that deltas will fill.
func (b *Backend) onItemStarted(threadID string, t *thread, params json.RawMessage) {
	parsed, ok := decodeSingleItem(params)
	if !ok || !parsed.isTool() {
		return
	}
	t.mu.Lock()
	t.tools[parsed.ID] = true
	t.mu.Unlock()
	// Codex delivers the whole input up front, so the card is complete at once:
	// start and end back to back, with no tool_input_delta in between.
	b.deps.Publish(protocol.ToolUseStart(threadID, parsed.ID, 0, parsed.ID, parsed.toolName(), parsed.toolKind(), nil))
	b.deps.Publish(protocol.ToolUseEnd(threadID, parsed.ID, parsed.toolInput()))
}

func (b *Backend) onItemCompleted(threadID string, t *thread, params json.RawMessage) {
	parsed, ok := decodeSingleItem(params)
	if !ok {
		return
	}
	switch {
	case parsed.Type == "userMessage":
		// Codex echoes the owner's own prompt back; clientId is the id we sent.
		var clientMessageID *string
		if parsed.ClientID != "" {
			id := parsed.ClientID
			clientMessageID = &id
		}
		b.deps.Publish(protocol.UserMessage(threadID, parsed.ID, parsed.userContent(), clientMessageID, nil))

	case parsed.Type == "agentMessage":
		blocks := []protocol.ContentBlock{protocol.Text(parsed.textContent())}
		b.deps.Publish(protocol.AssistantMessage(threadID, parsed.ID, blocks, nil))

	case parsed.Type == "reasoning":
		blocks := []protocol.ContentBlock{protocol.Thinking(parsed.textContent())}
		b.deps.Publish(protocol.AssistantMessage(threadID, parsed.ID, blocks, nil))

	case parsed.Type == "contextCompaction":
		b.deps.Publish(protocol.ContextBoundary(threadID, protocol.CompactAuto, nil))

	case parsed.isTool():
		t.mu.Lock()
		started := t.tools[parsed.ID]
		delete(t.tools, parsed.ID)
		t.mu.Unlock()
		if !started {
			// A tool that completed without a start (a resumed thread replaying
			// one) still needs its card before its result.
			b.deps.Publish(protocol.ToolUseStart(threadID, parsed.ID, 0, parsed.ID, parsed.toolName(), parsed.toolKind(), nil))
			b.deps.Publish(protocol.ToolUseEnd(threadID, parsed.ID, parsed.toolInput()))
		}
		output, failed := parsed.toolOutput()
		b.deps.Publish(protocol.ToolResult(threadID, parsed.ID, []protocol.ContentBlock{protocol.Text(output)}, failed))
	}
}

func decodeSingleItem(params json.RawMessage) (item, bool) {
	var payload struct {
		Item json.RawMessage `json:"item"`
	}
	if json.Unmarshal(params, &payload) != nil {
		return item{}, false
	}
	return decodeItem(payload.Item)
}

func decodeOutputDelta(params json.RawMessage) string {
	var payload struct {
		Delta string `json:"delta"`
		Chunk string `json:"chunk"`
	}
	_ = json.Unmarshal(params, &payload)
	if payload.Delta != "" {
		return payload.Delta
	}
	return payload.Chunk
}

// --- server requests: the approval prompts ---

// onRequest handles a request the app-server made of us. Every one of them is
// a moment the harness is blocked on the owner.
func (b *Backend) onRequest(id json.RawMessage, method string, params json.RawMessage) {
	var ref threadRef
	_ = json.Unmarshal(params, &ref)
	threadID, known := b.resolve(ref.ThreadID)
	if !known {
		b.declineUnknown(id, method)
		return
	}
	t, ok := b.thread(threadID)
	if !ok {
		b.declineUnknown(id, method)
		return
	}

	payload, supported := approvalPayload(method, params)
	if !supported {
		b.declineUnknown(id, method)
		return
	}
	requestID := fmt.Sprintf("codex-%s", strings.Trim(string(id), `"`))
	request := protocol.InteractionRequest{RequestID: requestID, CreatedAt: time.Now().UTC(), Payload: payload}

	t.mu.Lock()
	t.pending[requestID] = &approval{rpcID: id, method: method, request: request}
	t.mu.Unlock()
	b.deps.Publish(protocol.InteractionRequested(threadID, request))
	b.setState(threadID, protocol.StateWaitingInput)
}

// declineUnknown answers a request nothing can be done with, rather than
// leaving the harness blocked on a reply that will never come.
func (b *Backend) declineUnknown(id json.RawMessage, method string) {
	b.mu.Lock()
	server := b.server
	b.mu.Unlock()
	if server == nil {
		return
	}
	if method == "item/tool/requestUserInput" {
		_ = server.respond(id, map[string]any{"answers": map[string]any{}})
		return
	}
	_ = server.respond(id, map[string]any{"decision": "decline"})
}

// approvalPayload maps one server request onto the protocol's interaction kinds.
func approvalPayload(method string, params json.RawMessage) (protocol.InteractionPayload, bool) {
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval":
		var payload struct {
			ItemID  string `json:"itemId"`
			Command string `json:"command"`
			Reason  string `json:"reason"`
		}
		_ = json.Unmarshal(params, &payload)
		kind := protocol.ToolShell
		if method == "item/fileChange/requestApproval" {
			kind = protocol.ToolFileEdit
		}
		input := map[string]string{}
		if payload.Command != "" {
			input["command"] = payload.Command
		}
		if payload.Reason != "" {
			input["reason"] = payload.Reason
		}
		encoded, _ := json.Marshal(input)
		return protocol.PermissionPayload{
			PayloadKind: protocol.InteractionPermission,
			ToolUseID:   payload.ItemID,
			ToolName:    approvalToolName(method),
			ToolKind:    kind,
			ToolInput:   encoded,
			// Codex expresses "remember" as a decision value rather than as a
			// rule, so the one suggestion is the session-scoped accept.
			Suggestions: []protocol.PermissionSuggestion{
				{Rule: approvalToolName(method), Scope: protocol.ScopeSession},
			},
		}, true

	case "item/tool/requestUserInput":
		var payload struct {
			Questions []struct {
				ID      string `json:"id"`
				Header  string `json:"header"`
				Prompt  string `json:"prompt"`
				Options []struct {
					Label       string `json:"label"`
					Description string `json:"description"`
				} `json:"options"`
			} `json:"questions"`
		}
		_ = json.Unmarshal(params, &payload)
		questions := make([]protocol.Question, 0, len(payload.Questions))
		for _, q := range payload.Questions {
			options := make([]protocol.QuestionOption, 0, len(q.Options))
			for _, o := range q.Options {
				option := protocol.QuestionOption{Label: o.Label}
				if o.Description != "" {
					description := o.Description
					option.Description = &description
				}
				options = append(options, option)
			}
			prompt := q.Prompt
			if prompt == "" {
				prompt = q.Header
			}
			questions = append(questions, protocol.Question{ID: q.ID, Prompt: prompt, Options: options})
		}
		if len(questions) == 0 {
			return nil, false
		}
		return protocol.QuestionPayload{PayloadKind: protocol.InteractionQuestion, Questions: questions}, true
	}
	return nil, false
}

func approvalToolName(method string) string {
	switch method {
	case "item/fileChange/requestApproval":
		return "fileChange"
	case "item/permissions/requestApproval":
		return "permissions"
	default:
		return "commandExecution"
	}
}

// approvalResult turns a decision into the JSON-RPC result the server wants.
func approvalResult(method string, decision protocol.InteractionDecision) map[string]any {
	if method == "item/tool/requestUserInput" {
		answers := map[string]any{}
		for _, a := range decision.Answers {
			answers[a.QuestionID] = map[string]any{"answers": a.Answers}
		}
		return map[string]any{"answers": answers}
	}
	switch decision.Type {
	case protocol.DecisionAllow:
		if decision.Remember != nil {
			return map[string]any{"decision": "acceptForSession"}
		}
		return map[string]any{"decision": "accept"}
	default:
		if decision.Interrupt {
			return map[string]any{"decision": "cancel"}
		}
		return map[string]any{"decision": "decline"}
	}
}

// resolveAllPending declines everything still open, which is what the end of a
// turn does to a prompt nobody answered.
func (b *Backend) resolveAllPending(threadID string, t *thread) {
	t.mu.Lock()
	open := make([]*approval, 0, len(t.pending))
	for _, a := range t.pending {
		open = append(open, a)
	}
	t.pending = map[string]*approval{}
	t.mu.Unlock()
	if len(open) == 0 {
		return
	}
	b.mu.Lock()
	server := b.server
	b.mu.Unlock()
	for _, a := range open {
		if server != nil {
			_ = server.respond(a.rpcID, approvalResult(a.method, protocol.InteractionDecision{Type: protocol.DecisionDeny}))
		}
		b.deps.Publish(protocol.InteractionResolved(threadID, a.request.RequestID, nil))
	}
}

// resolve maps a Codex thread id to ours, consulting the registry once for a
// thread the server knows about after a restart on its side.
func (b *Backend) resolve(nativeID string) (string, bool) {
	if nativeID == "" {
		return "", false
	}
	b.mu.Lock()
	threadID, ok := b.byNative[nativeID]
	b.mu.Unlock()
	return threadID, ok
}

func (b *Backend) noticeEverywhere(kind protocol.NoticeKind, message string) {
	if message == "" {
		return
	}
	b.mu.Lock()
	ids := make([]string, 0, len(b.threads))
	for id := range b.threads {
		ids = append(ids, id)
	}
	b.mu.Unlock()
	for _, id := range ids {
		b.deps.Publish(protocol.Notice(id, kind, message))
	}
}

func rateLimitMessage(params json.RawMessage) string {
	var payload struct {
		RateLimits struct {
			Primary *struct {
				UsedPercent float64 `json:"usedPercent"`
			} `json:"primary"`
		} `json:"rateLimits"`
	}
	if json.Unmarshal(params, &payload) != nil || payload.RateLimits.Primary == nil {
		return ""
	}
	// Only worth a banner once it is close enough to matter.
	if payload.RateLimits.Primary.UsedPercent < 90 {
		return ""
	}
	return fmt.Sprintf("Codex usage is at %.0f%% of the current window", payload.RateLimits.Primary.UsedPercent)
}

func errorMessage(params json.RawMessage) string {
	var payload struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	_ = json.Unmarshal(params, &payload)
	if payload.Message != "" {
		return payload.Message
	}
	return payload.Error
}

func classifyError(params json.RawMessage) protocol.StreamErrorCode {
	lower := strings.ToLower(errorMessage(params))
	switch {
	case strings.Contains(lower, "rate limit"), strings.Contains(lower, "usage limit"):
		return protocol.ErrRateLimited
	case strings.Contains(lower, "context window"), strings.Contains(lower, "too long"):
		return protocol.ErrContextOverflow
	case strings.Contains(lower, "login"), strings.Contains(lower, "auth"), strings.Contains(lower, "credential"):
		return protocol.ErrCredential
	}
	return protocol.ErrOther
}
