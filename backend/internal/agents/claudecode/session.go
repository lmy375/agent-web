package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/lmy375/agent-web/backend/internal/chat"
	"github.com/lmy375/agent-web/backend/internal/protocol"
)

// session is one thread's live view of a `claude` subprocess. Everything the
// CLI says arrives on its reader goroutine and leaves as protocol events; the
// mutex guards the state those events are derived from.
type session struct {
	threadID string
	deps     chat.Deps
	launcher func(rec chat.ThreadRecord, resume bool) launch

	mu      sync.Mutex
	proc    *process
	state   protocol.ThreadRunState
	pending map[string]*pending
	// turn is the client_message_id of the running turn, empty when idle.
	turn         string
	interrupted  bool
	contextUsage *protocol.ContextUsage
	lastTurn     *protocol.TurnSummary
	lastActive   time.Time
	// effort the live process was launched with; changing it needs a restart
	// because it is a CLI flag rather than a control request.
	launchedEffort string

	// Per-message stream assembly: which content block index is what.
	messageID  string
	blockTools map[int]string
}

func newSession(threadID string, deps chat.Deps, launcher func(chat.ThreadRecord, bool) launch) *session {
	return &session{
		threadID:   threadID,
		deps:       deps,
		launcher:   launcher,
		state:      protocol.StateIdle,
		pending:    map[string]*pending{},
		blockTools: map[int]string{},
		lastActive: time.Now(),
	}
}

func (s *session) publish(e protocol.ServerEvent) { s.deps.Publish(e) }

func (s *session) setState(state protocol.ThreadRunState) {
	s.mu.Lock()
	changed := s.state != state
	s.state = state
	s.mu.Unlock()
	if changed {
		s.deps.StateChanged(s.threadID)
	}
}

func (s *session) runState() protocol.ThreadRunState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *session) liveState() chat.LiveState {
	s.mu.Lock()
	defer s.mu.Unlock()
	requests := make([]protocol.InteractionRequest, 0, len(s.pending))
	for _, p := range s.pending {
		requests = append(requests, p.request)
	}
	return chat.LiveState{Pending: requests, ContextUsage: s.contextUsage, LastTurn: s.lastTurn}
}

// idleSince reports how long the session has had no process work, and whether
// it is safe to reap: a thread waiting on the owner is not idle.
func (s *session) idleSince() (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.proc == nil || s.state != protocol.StateIdle {
		return 0, false
	}
	return time.Since(s.lastActive), true
}

// --- lifecycle ---

// ensureRunning starts the subprocess if there is none, or restarts it when the
// effort flag it was launched with no longer matches the thread's options.
func (s *session) ensureRunning(ctx context.Context, rec chat.ThreadRecord) error {
	s.mu.Lock()
	live, launched := s.proc, s.launchedEffort
	s.mu.Unlock()

	wanted := rec.Options.Setting("effort")
	if live != nil && launched == wanted {
		return nil
	}
	if live != nil {
		// Effort is a CLI flag, so changing it means a new process. The
		// conversation is resumed from the transcript; nothing is lost.
		s.stopProcess()
	}
	return s.startProcess(ctx, rec)
}

func (s *session) startProcess(ctx context.Context, rec chat.ThreadRecord) error {
	s.setState(protocol.StateStarting)
	proc, err := start(s.launcher(rec, rec.NativeID != ""), handlers{
		onMessage:        s.onMessage,
		onControlRequest: s.onControlRequest,
		onExit:           s.onExit,
	})
	if err != nil {
		s.setState(protocol.StateIdle)
		return protocol.Errorf(protocol.CodeAgentUnavailable, "%v", err)
	}

	s.mu.Lock()
	s.proc = proc
	s.launchedEffort = rec.Options.Setting("effort")
	s.mu.Unlock()

	// initialize is what switches the CLI into the control protocol; until it
	// answers, can_use_tool prompts would have nowhere to go.
	if _, err := proc.control(ctx, map[string]any{"subtype": "initialize", "hooks": nil}, initializeTimeout); err != nil {
		s.stopProcess()
		return protocol.Errorf(protocol.CodeAgentUnavailable, "claude did not initialize: %v", err)
	}
	s.setState(protocol.StateIdle)
	return nil
}

func (s *session) stopProcess() {
	s.mu.Lock()
	proc := s.proc
	s.proc = nil
	s.mu.Unlock()
	if proc != nil {
		proc.stop()
	}
}

// onExit fires when the subprocess is gone. A death mid-turn is a fatal stream
// error the client reconnects from; a death while idle is invisible, because
// the next prompt starts a fresh process and resumes the transcript.
func (s *session) onExit(err error) {
	s.mu.Lock()
	wasRunning := s.turn != ""
	s.proc = nil
	s.turn = ""
	s.mu.Unlock()

	s.resolveAllPending()
	if wasRunning {
		message := "the claude process exited"
		if err != nil {
			message = err.Error()
		}
		s.publish(protocol.StreamError(s.threadID, protocol.ErrHarnessExited, message, true))
	}
	s.setState(protocol.StateIdle)
}

func (s *session) close() { s.stopProcess() }

// --- commands ---

func (s *session) prompt(ctx context.Context, rec chat.ThreadRecord, cmd protocol.ClientCommand) error {
	s.mu.Lock()
	if s.turn != "" {
		s.mu.Unlock()
		return protocol.Errorf(protocol.CodeThreadBusy, "a turn is already running")
	}
	s.mu.Unlock()

	if err := s.ensureRunning(ctx, rec); err != nil {
		return err
	}

	blocks := promptBlocks(cmd)
	s.mu.Lock()
	s.turn = cmd.ClientMessageID
	s.interrupted = false
	s.lastActive = time.Now()
	proc := s.proc
	s.mu.Unlock()
	if proc == nil {
		return protocol.Errorf(protocol.CodeAgentUnavailable, "the claude process is gone")
	}

	message := map[string]any{
		"type":               "user",
		"message":            map[string]any{"role": "user", "content": blocks},
		"parent_tool_use_id": nil,
		"session_id":         rec.NativeID,
	}
	if err := proc.writeJSON(message); err != nil {
		s.mu.Lock()
		s.turn = ""
		s.mu.Unlock()
		return protocol.Errorf(protocol.CodeAgentUnavailable, "%v", err)
	}

	s.setState(protocol.StateRunning)
	s.publish(protocol.TurnStarted(s.threadID, cmd.ClientMessageID))
	// The CLI does not echo an owner prompt back, so the echo is ours to emit
	// -- and emitting it from the very blocks that were sent means the
	// transcript the browser renders cannot drift from what the model saw.
	id := cmd.ClientMessageID
	s.publish(protocol.UserMessage(s.threadID, "prompt-"+id, echoBlocks(cmd), &id, nil))
	return nil
}

// promptBlocks is the content array of the stream-json user message.
func promptBlocks(cmd protocol.ClientCommand) []any {
	blocks := make([]any, 0, len(cmd.Images)+1)
	for _, image := range cmd.Images {
		blocks = append(blocks, map[string]any{
			"type": "image",
			"source": map[string]any{
				"type": "base64", "media_type": image.MediaType, "data": image.DataBase64,
			},
		})
	}
	if cmd.Text != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": cmd.Text})
	}
	return blocks
}

func echoBlocks(cmd protocol.ClientCommand) []protocol.UserBlock {
	blocks := make([]protocol.UserBlock, 0, len(cmd.Images)+1)
	for _, image := range cmd.Images {
		blocks = append(blocks, image)
	}
	if cmd.Text != "" {
		blocks = append(blocks, protocol.Text(cmd.Text))
	}
	return blocks
}

func (s *session) interrupt(ctx context.Context) error {
	s.mu.Lock()
	proc, running := s.proc, s.turn != ""
	if running {
		s.interrupted = true
	}
	s.mu.Unlock()
	if proc == nil || !running {
		return nil
	}
	if _, err := proc.control(ctx, map[string]any{"subtype": "interrupt"}, controlTimeout); err != nil {
		return protocol.Errorf(protocol.CodeAgentUnavailable, "interrupt failed: %v", err)
	}
	return nil
}

// applyOptions pushes model and mode to a live process. Effort is a launch flag
// and is picked up by the restart the next prompt performs; a cold thread has
// nothing to push at all.
func (s *session) applyOptions(ctx context.Context, rec chat.ThreadRecord) error {
	s.mu.Lock()
	proc := s.proc
	s.mu.Unlock()
	if proc == nil {
		return nil
	}
	if rec.Options.Model != nil {
		if _, err := proc.control(ctx, map[string]any{"subtype": "set_model", "model": *rec.Options.Model}, controlTimeout); err != nil {
			return protocol.Errorf(protocol.CodeOptionInvalid, "cannot set model: %v", err)
		}
	}
	if mode := rec.Options.Setting("permission-mode"); mode != "" {
		request := map[string]any{"subtype": "set_permission_mode", "mode": mode}
		if _, err := proc.control(ctx, request, controlTimeout); err != nil {
			return protocol.Errorf(protocol.CodeOptionInvalid, "cannot set permission mode: %v", err)
		}
	}
	return nil
}

func (s *session) respond(requestID string, decision protocol.InteractionDecision) error {
	s.mu.Lock()
	p, ok := s.pending[requestID]
	s.mu.Unlock()
	if !ok {
		return protocol.Errorf(protocol.CodeInteractionNotPending, "no pending interaction %s", requestID)
	}
	select {
	case p.answered <- decision:
		return nil
	default:
		return protocol.Errorf(protocol.CodeInteractionNotPending, "interaction %s was already answered", requestID)
	}
}

// --- incoming ---

// onControlRequest answers the CLI. Only can_use_tool is expected: hooks and
// SDK-side MCP servers are not configured, so anything else is refused rather
// than left to time out.
func (s *session) onControlRequest(requestID, subtype string, payload json.RawMessage) {
	s.mu.Lock()
	proc := s.proc
	s.mu.Unlock()
	if proc == nil {
		return
	}
	if subtype != "can_use_tool" {
		_ = proc.replyError(requestID, fmt.Sprintf("unsupported control request %q", subtype))
		return
	}
	var request canUseToolRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		_ = proc.replyError(requestID, "unreadable can_use_tool request")
		return
	}

	p := newPending(requestID, request)
	s.mu.Lock()
	s.pending[requestID] = p
	s.mu.Unlock()
	s.publish(protocol.InteractionRequested(s.threadID, p.request))
	s.setState(protocol.StateWaitingInput)

	// Block this one goroutine until the owner answers or the turn ends; the
	// CLI is waiting on the response and has nothing else to say meanwhile.
	go func() {
		decision, answered := <-p.answered
		s.mu.Lock()
		delete(s.pending, requestID)
		stillRunning := s.turn != ""
		s.mu.Unlock()

		if !answered {
			// resolveAllPending closed the channel: the turn ended first.
			_ = proc.reply(requestID, map[string]any{"behavior": "deny", "message": "The turn ended before a decision was made."})
			s.publish(protocol.InteractionResolved(s.threadID, requestID, nil))
			return
		}
		_ = proc.reply(requestID, p.controlReply(decision))
		s.publish(protocol.InteractionResolved(s.threadID, requestID, &decision))
		if stillRunning {
			s.setState(protocol.StateRunning)
		}
	}()
}

// resolveAllPending ends every unanswered request, which is what an interrupt
// or a finished turn does to them.
func (s *session) resolveAllPending() {
	s.mu.Lock()
	waiting := make([]*pending, 0, len(s.pending))
	for _, p := range s.pending {
		waiting = append(waiting, p)
	}
	s.mu.Unlock()
	for _, p := range waiting {
		close(p.answered)
	}
}

func (s *session) onMessage(kind string, raw json.RawMessage) {
	s.mu.Lock()
	s.lastActive = time.Now()
	s.mu.Unlock()

	switch kind {
	case "stream_event":
		s.onStreamEvent(raw)
	case "assistant":
		s.onAssistant(raw)
	case "user":
		s.onUser(raw)
	case "result":
		s.onResult(raw)
	case "system":
		s.onSystem(raw)
	case "rate_limit_event":
		s.onRateLimit(raw)
	}
}

type streamEnvelope struct {
	Event struct {
		Type    string `json:"type"`
		Index   int    `json:"index"`
		Message struct {
			ID string `json:"id"`
		} `json:"message"`
		ContentBlock rawBlock `json:"content_block"`
		Delta        struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
	} `json:"event"`
	ParentToolUseID *string `json:"parent_tool_use_id"`
}

func (s *session) onStreamEvent(raw json.RawMessage) {
	var env streamEnvelope
	if json.Unmarshal(raw, &env) != nil {
		return
	}
	event, parent := env.Event, env.ParentToolUseID

	switch event.Type {
	case "message_start":
		s.mu.Lock()
		s.messageID = event.Message.ID
		s.blockTools = map[int]string{}
		s.mu.Unlock()

	case "content_block_start":
		if event.ContentBlock.Type != "tool_use" {
			return
		}
		s.mu.Lock()
		s.blockTools[event.Index] = event.ContentBlock.ID
		messageID := s.messageID
		s.mu.Unlock()
		s.publish(protocol.ToolUseStart(s.threadID, messageID, event.Index,
			event.ContentBlock.ID, event.ContentBlock.Name, toolKind(event.ContentBlock.Name), parent))

	case "content_block_delta":
		s.mu.Lock()
		messageID, toolUseID := s.messageID, s.blockTools[event.Index]
		s.mu.Unlock()
		switch event.Delta.Type {
		case "text_delta":
			s.publish(protocol.TextDelta(s.threadID, messageID, event.Index, event.Delta.Text, parent))
		case "thinking_delta":
			s.publish(protocol.ThinkingDelta(s.threadID, messageID, event.Index, event.Delta.Thinking, parent))
		case "input_json_delta":
			if toolUseID != "" {
				s.publish(protocol.ToolInputDelta(s.threadID, toolUseID, event.Delta.PartialJSON))
			}
		}
	}
}

type assistantEnvelope struct {
	Message         rawMessage `json:"message"`
	ParentToolUseID *string    `json:"parent_tool_use_id"`
}

func (s *session) onAssistant(raw json.RawMessage) {
	var env assistantEnvelope
	if json.Unmarshal(raw, &env) != nil {
		s.publish(protocol.StreamError(s.threadID, protocol.ErrUnrenderable, "unreadable assistant message", false))
		return
	}
	blocks := env.Message.blocks()
	// A tool call's input is only complete here, so this is where it settles.
	for _, block := range blocks {
		if block.Type == "tool_use" {
			input := block.Input
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			s.publish(protocol.ToolUseEnd(s.threadID, block.ID, input))
		}
	}
	s.publish(protocol.AssistantMessage(s.threadID, env.Message.ID, contentBlocks(blocks), env.ParentToolUseID))
}

type userEnvelope struct {
	Message         rawMessage `json:"message"`
	ParentToolUseID *string    `json:"parent_tool_use_id"`
	UUID            string     `json:"uuid"`
}

// onUser splits what the CLI calls a user message: tool results become their own
// events, and anything else is a message the harness synthesized, since an owner
// prompt is echoed at the point it is sent.
func (s *session) onUser(raw json.RawMessage) {
	var env userEnvelope
	if json.Unmarshal(raw, &env) != nil {
		return
	}
	blocks := env.Message.blocks()
	for _, block := range blocks {
		if block.Type == "tool_result" {
			s.publish(protocol.ToolResult(s.threadID, block.ToolUseID, resultContent(block.Content), block.IsError))
		}
	}
	if visible := userBlocks(blocks); len(visible) > 0 {
		s.publish(protocol.UserMessage(s.threadID, env.UUID, visible, nil, env.ParentToolUseID))
	}
}

func (s *session) onResult(raw json.RawMessage) {
	var result rawResult
	_ = json.Unmarshal(raw, &result)

	s.resolveAllPending()

	s.mu.Lock()
	turn, interrupted := s.turn, s.interrupted
	s.turn, s.interrupted = "", false
	summary := turnSummary(result, interrupted)
	s.lastTurn = &summary
	s.mu.Unlock()

	if code, message, failed := resultError(result); failed && !interrupted {
		s.publish(protocol.StreamError(s.threadID, code, message, false))
	}
	s.publish(protocol.TurnFinished(s.threadID, turn, summary))
	s.setState(protocol.StateIdle)
	s.deps.Registry.Touch(s.threadID)
	go s.refreshContextUsage()
}

// refreshContextUsage asks the CLI what the window holds now. It is a control
// request rather than an event in any harness, which is why the protocol also
// exposes it as part of the thread detail.
func (s *session) refreshContextUsage() {
	s.mu.Lock()
	proc := s.proc
	s.mu.Unlock()
	if proc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
	defer cancel()
	response, err := proc.control(ctx, map[string]any{"subtype": "get_context_usage"}, controlTimeout)
	if err != nil {
		return
	}
	var raw rawContextUsage
	if json.Unmarshal(response, &raw) != nil || raw.MaxTokens == 0 {
		return
	}
	usage := contextUsage(raw)
	s.mu.Lock()
	s.contextUsage = &usage
	s.mu.Unlock()
	s.publish(protocol.ContextUsageChanged(s.threadID, usage))
}

type systemEnvelope struct {
	Subtype        string `json:"subtype"`
	SessionID      string `json:"session_id"`
	Model          string `json:"model"`
	PermissionMode string `json:"permissionMode"`
	// status messages carry the mode under a different name
	Mode            string `json:"mode"`
	Trigger         string `json:"trigger"`
	PreTokens       *int   `json:"pre_tokens"`
	CompactMetadata *struct {
		Trigger   string `json:"trigger"`
		PreTokens *int   `json:"pre_tokens"`
	} `json:"compact_metadata"`
}

func (s *session) onSystem(raw json.RawMessage) {
	var env systemEnvelope
	if json.Unmarshal(raw, &env) != nil {
		return
	}
	switch env.Subtype {
	case "init":
		// The CLI is the authority on the session id and on which model and
		// mode it actually came up with; the row follows it, not the reverse.
		s.deps.Registry.Update(s.threadID, func(rec *chat.ThreadRecord) {
			if env.SessionID != "" {
				rec.NativeID = env.SessionID
			}
			if env.Model != "" {
				model := env.Model
				rec.Options.Model = &model
			}
			if env.PermissionMode != "" {
				rec.Options.Set("permission-mode", modeAsFlag(env.PermissionMode))
			}
		})
	case "compact_boundary":
		reason, tokens := protocol.CompactAuto, env.PreTokens
		trigger := env.Trigger
		if env.CompactMetadata != nil {
			trigger = env.CompactMetadata.Trigger
			tokens = env.CompactMetadata.PreTokens
		}
		if trigger == "manual" {
			reason = protocol.CompactManual
		}
		s.publish(protocol.ContextBoundary(s.threadID, reason, tokens))
	case "status":
		// The owner can change the mode inside the CLI (a plan approval does);
		// the row has to follow.
		if env.Mode != "" {
			s.deps.Registry.Update(s.threadID, func(rec *chat.ThreadRecord) { rec.Options.Set("permission-mode", modeAsFlag(env.Mode)) })
		}
	}
}

type rateLimitEnvelope struct {
	Info struct {
		Status        string `json:"status"`
		RateLimitType string `json:"rateLimitType"`
	} `json:"rate_limit_info"`
}

func (s *session) onRateLimit(raw json.RawMessage) {
	var env rateLimitEnvelope
	if json.Unmarshal(raw, &env) != nil || env.Info.Status == "allowed" {
		return
	}
	s.publish(protocol.Notice(s.threadID, protocol.NoticeRateLimit,
		fmt.Sprintf("%s rate limit: %s", env.Info.RateLimitType, env.Info.Status)))
}
