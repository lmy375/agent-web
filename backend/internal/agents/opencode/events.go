package opencode

import (
	"encoding/json"
	"time"

	"github.com/lmy375/agent-web/backend/internal/chat"
	"github.com/lmy375/agent-web/backend/internal/protocol"
)

// envelope is the shape of every frame on /event.
type envelope struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

// onEvent turns one server event into protocol events. The stream is
// server-wide, so the first job is finding whose thread this is.
func (b *Backend) onEvent(raw json.RawMessage) {
	var frame envelope
	if json.Unmarshal(raw, &frame) != nil {
		return
	}
	var ref struct {
		SessionID string `json:"sessionID"`
	}
	_ = json.Unmarshal(frame.Properties, &ref)
	threadID, ok := b.resolve(ref.SessionID)
	if !ok {
		return
	}
	t, ok := b.thread(threadID)
	if !ok {
		return
	}

	switch frame.Type {
	case "message.part.updated":
		b.onPartUpdated(threadID, t, frame.Properties)
	case "message.part.delta":
		b.onPartDelta(threadID, t, frame.Properties)
	case "message.updated":
		b.onMessageUpdated(threadID, frame.Properties)
	case "permission.asked":
		b.onPermissionAsked(threadID, t, frame.Properties)
	case "question.asked":
		b.onQuestionAsked(threadID, t, frame.Properties)
	case "permission.replied", "question.replied", "question.rejected":
		// Another client answered; the request simply leaves the pending list.
		b.forgetPending(threadID, t, frame.Properties)
	case "session.error":
		var payload struct {
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(frame.Properties, &payload) == nil {
			if code, message := sessionError(payload.Error); code != "" {
				b.deps.Publish(protocol.StreamError(threadID, code, message, false))
			}
		}
	case "session.compacted":
		b.deps.Publish(protocol.ContextBoundary(threadID, protocol.CompactAuto, nil))
	case "session.updated":
		var payload struct {
			Info struct {
				Title string `json:"title"`
			} `json:"info"`
		}
		if json.Unmarshal(frame.Properties, &payload) == nil && payload.Info.Title != "" {
			b.deps.Registry.Update(threadID, func(rec *chat.ThreadRecord) {
				if rec.Title == nil {
					title := payload.Info.Title
					rec.Title = &title
				}
			})
		}
	}
}

// onPartUpdated settles one part. A text or reasoning part is remembered so its
// deltas can be routed; a tool part becomes a card and, once finished, a result.
func (b *Backend) onPartUpdated(threadID string, t *thread, properties json.RawMessage) {
	var payload struct {
		Part part `json:"part"`
	}
	if json.Unmarshal(properties, &payload) != nil {
		return
	}
	p := payload.Part
	if p.MessageID != "" {
		t.remember(p)
	}

	switch p.Type {
	case "text", "reasoning":
		t.mu.Lock()
		t.parts[p.ID] = p.Type
		t.mu.Unlock()

	case "tool":
		t.mu.Lock()
		started := t.tools[p.CallID]
		t.tools[p.CallID] = true
		t.mu.Unlock()
		if !started {
			b.deps.Publish(protocol.ToolUseStart(threadID, p.MessageID, t.blockIndex(p.MessageID, p.ID), p.CallID, p.Tool, toolKind(p.Tool), nil))
			input := json.RawMessage("{}")
			if p.State != nil && len(p.State.Input) > 0 {
				input = p.State.Input
			}
			b.deps.Publish(protocol.ToolUseEnd(threadID, p.CallID, input))
		}
		if p.State != nil && p.State.Status != "running" && p.State.Status != "pending" {
			output, failed := toolResult(p)
			t.mu.Lock()
			delete(t.tools, p.CallID)
			t.mu.Unlock()
			b.deps.Publish(protocol.ToolResult(threadID, p.CallID, []protocol.ContentBlock{protocol.Text(output)}, failed))
		}
	}
}

func (b *Backend) onPartDelta(threadID string, t *thread, properties json.RawMessage) {
	var payload struct {
		MessageID string `json:"messageID"`
		PartID    string `json:"partID"`
		Field     string `json:"field"`
		Delta     string `json:"delta"`
	}
	if json.Unmarshal(properties, &payload) != nil || payload.Delta == "" {
		return
	}
	t.mu.Lock()
	kind := t.parts[payload.PartID]
	t.mu.Unlock()
	index := t.blockIndex(payload.MessageID, payload.PartID)
	if kind == "reasoning" {
		b.deps.Publish(protocol.ThinkingDelta(threadID, payload.MessageID, index, payload.Delta, nil))
		return
	}
	// A delta for a part we have not seen settled yet is text: that is the only
	// field OpenCode streams before the part itself arrives.
	if kind == "text" || payload.Field == "text" {
		b.deps.Publish(protocol.TextDelta(threadID, payload.MessageID, index, payload.Delta, nil))
	}
}

// onMessageUpdated settles an assistant message once OpenCode marks it complete;
// the turn itself is closed by the prompt call returning.
func (b *Backend) onMessageUpdated(threadID string, properties json.RawMessage) {
	var payload struct {
		Info message `json:"info"`
	}
	if json.Unmarshal(properties, &payload) != nil {
		return
	}
	if payload.Info.Role != "assistant" || payload.Info.Time.Completed == 0 {
		return
	}
	// The message is final: settle it from the parts collected along the way,
	// so the client replaces its delta assembly with the real blocks.
	if t, ok := b.thread(threadID); ok {
		if blocks := contentBlocks(t.take(payload.Info.ID)); len(blocks) > 0 {
			b.deps.Publish(protocol.AssistantMessage(threadID, payload.Info.ID, blocks, nil))
		}
		t.mu.Lock()
		clientMessageID, running := t.turn, !t.turnStarted.IsZero()
		if spent := payload.Info.usage(); spent != nil && running {
			t.turnUsage[payload.Info.ID] = *spent
		}
		total := t.spent()
		t.mu.Unlock()
		if running {
			b.deps.Publish(protocol.TurnUsage(threadID, clientMessageID, total))
		}
	}
	if payload.Info.Tokens != nil && payload.Info.Tokens.Total > 0 {
		usage := protocol.ContextUsage{
			TotalTokens: int(payload.Info.Tokens.Total),
			// OpenCode does not report the model's window; the client draws a
			// count rather than a ring when the maximum is unknown.
			Categories: []protocol.ContextCategory{},
		}
		if t, ok := b.thread(threadID); ok {
			t.mu.Lock()
			t.contextUsage = &usage
			t.mu.Unlock()
		}
		b.deps.Publish(protocol.ContextUsageChanged(threadID, usage))
	}
}

func (b *Backend) onPermissionAsked(threadID string, t *thread, properties json.RawMessage) {
	var payload struct {
		ID         string          `json:"id"`
		Permission string          `json:"permission"`
		Patterns   []string        `json:"patterns"`
		Metadata   json.RawMessage `json:"metadata"`
		Tool       *struct {
			CallID string `json:"callID"`
		} `json:"tool"`
	}
	if json.Unmarshal(properties, &payload) != nil || payload.ID == "" {
		return
	}
	callID := payload.ID
	if payload.Tool != nil && payload.Tool.CallID != "" {
		callID = payload.Tool.CallID
	}
	input := payload.Metadata
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	suggestions := []protocol.PermissionSuggestion{}
	for _, pattern := range payload.Patterns {
		suggestions = append(suggestions, protocol.PermissionSuggestion{Rule: pattern, Scope: protocol.ScopeAlways})
	}
	request := protocol.InteractionRequest{
		RequestID: "opencode:" + payload.ID,
		CreatedAt: time.Now().UTC(),
		Payload: protocol.PermissionPayload{
			PayloadKind: protocol.InteractionPermission,
			ToolUseID:   callID,
			ToolName:    payload.Permission,
			ToolKind:    toolKind(payload.Permission),
			ToolInput:   input,
			Suggestions: suggestions,
		},
	}
	b.addPending(threadID, t, request)
}

func (b *Backend) onQuestionAsked(threadID string, t *thread, properties json.RawMessage) {
	var payload struct {
		ID        string `json:"id"`
		Questions []struct {
			Question string `json:"question"`
			Header   string `json:"header"`
			Multiple bool   `json:"multiple"`
			Options  []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"questions"`
	}
	if json.Unmarshal(properties, &payload) != nil || len(payload.Questions) == 0 {
		return
	}
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
		// OpenCode keys its answers by position, so the question text is a
		// stable enough id for the round trip.
		questions = append(questions, protocol.Question{
			ID: q.Question, Prompt: q.Question, Options: options, MultiSelect: q.Multiple,
		})
	}
	b.addPending(threadID, t, protocol.InteractionRequest{
		RequestID: "opencode:" + payload.ID,
		CreatedAt: time.Now().UTC(),
		Payload:   protocol.QuestionPayload{PayloadKind: protocol.InteractionQuestion, Questions: questions},
	})
}

func (b *Backend) addPending(threadID string, t *thread, request protocol.InteractionRequest) {
	t.mu.Lock()
	t.pending[request.RequestID] = request
	t.mu.Unlock()
	b.deps.Publish(protocol.InteractionRequested(threadID, request))
	b.setState(threadID, protocol.StateWaitingInput)
}

// forgetPending drops a request something else answered, so this server's
// pending list and the harness's agree.
func (b *Backend) forgetPending(threadID string, t *thread, properties json.RawMessage) {
	var payload struct {
		ID        string `json:"id"`
		RequestID string `json:"requestID"`
	}
	_ = json.Unmarshal(properties, &payload)
	id := payload.ID
	if id == "" {
		id = payload.RequestID
	}
	if id == "" {
		return
	}
	requestID := "opencode:" + id
	t.mu.Lock()
	_, waiting := t.pending[requestID]
	delete(t.pending, requestID)
	running := t.turn != ""
	t.mu.Unlock()
	if !waiting {
		return
	}
	b.deps.Publish(protocol.InteractionResolved(threadID, requestID, nil))
	if running {
		b.setState(threadID, protocol.StateRunning)
	}
}

func (b *Backend) resolve(sessionID string) (string, bool) {
	if sessionID == "" {
		return "", false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	threadID, ok := b.bySession[sessionID]
	return threadID, ok
}

// remember keeps the latest version of one part, in the order the parts first
// appeared, so a settled message can be rebuilt without asking the server.
func (t *thread) remember(p part) {
	t.mu.Lock()
	defer t.mu.Unlock()
	parts := t.assembling[p.MessageID]
	for i := range parts {
		if parts[i].ID == p.ID {
			parts[i] = p
			t.assembling[p.MessageID] = parts
			return
		}
	}
	t.assembling[p.MessageID] = append(parts, p)
}

// take removes and returns one message's collected parts.
// spent totals what this turn has cost so far. The caller holds the lock.
func (t *thread) spent() protocol.Usage {
	var total protocol.Usage
	for _, one := range t.turnUsage {
		total.Add(one)
	}
	return total
}

func (t *thread) take(messageID string) []part {
	t.mu.Lock()
	defer t.mu.Unlock()
	parts := t.assembling[messageID]
	delete(t.assembling, messageID)
	delete(t.blocks, messageID)
	return parts
}

// blockIndex is where one part's content belongs in its message, assigned on
// first sight because OpenCode itself numbers nothing.
func (t *thread) blockIndex(messageID, partID string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	byPart := t.blocks[messageID]
	if byPart == nil {
		byPart = map[string]int{}
		t.blocks[messageID] = byPart
	}
	if index, ok := byPart[partID]; ok {
		return index
	}
	index := len(byPart)
	byPart[partID] = index
	return index
}
