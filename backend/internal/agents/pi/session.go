package pi

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lmy375/agent-web/backend/internal/chat"
	"github.com/lmy375/agent-web/backend/internal/protocol"
)

// pending is one extension dialog the owner has not answered yet.
type pending struct {
	request protocol.InteractionRequest
	method  string
}

// session is one thread's live view of a `pi` subprocess. Everything pi says
// arrives on its reader goroutine and leaves as protocol events; the mutex
// guards the state those events are derived from, and is never held across a
// call into deps, because the registry calls back into runState.
type session struct {
	threadID string
	deps     chat.Deps
	launcher func(rec chat.ThreadRecord, resume bool) launch

	mu      sync.Mutex
	proc    *process
	state   protocol.ThreadRunState
	pending map[string]*pending
	// turn is the client_message_id of the running turn, empty for a turn an
	// extension opened by itself; running is the fact that a turn is under
	// way at all.
	turn        string
	running     bool
	interrupted bool
	// echoed says the owner's prompt has already come back from pi as the
	// turn's first user message; every later one is a steer.
	echoed     bool
	turnUsage  protocol.Usage
	turnCost   float64
	turnFailed bool
	// turnStarted is when this turn was claimed, and doubles as the fact that
	// one is under way at all for liveState; zero means no running turn.
	turnStarted time.Time
	// contextWindow is the current model's, learnt from get_state.
	contextWindow int
	contextUsage  *protocol.ContextUsage
	lastTurn      *protocol.TurnSummary
	lastActive    time.Time

	// Per-message stream assembly: the assistant message being streamed and
	// which content index is which tool call.
	messageID  string
	blockTools map[int]string
	// toolOutput is the last cumulative output snapshot per running tool,
	// which is what pi streams and what the delta is taken against.
	toolOutput map[string]string
}

func newSession(threadID string, deps chat.Deps, launcher func(chat.ThreadRecord, bool) launch) *session {
	return &session{
		threadID:   threadID,
		deps:       deps,
		launcher:   launcher,
		state:      protocol.StateIdle,
		pending:    map[string]*pending{},
		blockTools: map[int]string{},
		toolOutput: map[string]string{},
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
	var current *protocol.RunningTurn
	if !s.turnStarted.IsZero() {
		spent := s.turnUsage
		current = &protocol.RunningTurn{ClientMessageID: s.turn, StartedAt: s.turnStarted, Usage: &spent}
	}
	return chat.LiveState{
		Pending: requests, ContextUsage: s.contextUsage,
		CurrentTurn: current, LastTurn: s.lastTurn,
	}
}

func (s *session) process() *process {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.proc
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

func (s *session) ensureRunning(ctx context.Context, rec chat.ThreadRecord) (*process, error) {
	if proc := s.process(); proc != nil {
		return proc, nil
	}
	return s.startProcess(ctx, rec)
}

func (s *session) startProcess(ctx context.Context, rec chat.ThreadRecord) (*process, error) {
	s.setState(protocol.StateStarting)
	proc, err := start(s.launcher(rec, rec.NativeID != ""), handlers{
		onEvent:     s.onEvent,
		onUIRequest: s.onUIRequest,
		onExit:      s.onExit,
	})
	if err != nil {
		s.setState(protocol.StateIdle)
		return nil, protocol.Errorf(protocol.CodeAgentUnavailable, "%v", err)
	}
	s.mu.Lock()
	s.proc = proc
	s.mu.Unlock()

	// get_state is the handshake: it proves pi is up, and it names the
	// session file, the model and the thinking level pi actually came up
	// with, which the row then follows.
	raw, err := proc.request(ctx, map[string]any{"type": "get_state"}, startupTimeout)
	if err != nil {
		s.stopProcess()
		s.setState(protocol.StateIdle)
		return nil, protocol.Errorf(protocol.CodeAgentUnavailable, "pi did not start: %v", err)
	}
	var state sessionState
	if err := json.Unmarshal(raw, &state); err != nil {
		s.stopProcess()
		s.setState(protocol.StateIdle)
		return nil, protocol.Errorf(protocol.CodeAgentUnavailable, "pi returned a state this build cannot read: %v", err)
	}
	s.reconcile(state)
	s.setState(protocol.StateIdle)
	return proc, nil
}

// reconcile makes the row follow pi: the session file it will write, the
// model it resolved and the thinking level it kept. pi clamps a level a model
// cannot do and refuses a model it does not know, so after every start and
// every option change the row is corrected from what pi reports.
func (s *session) reconcile(state sessionState) {
	s.mu.Lock()
	if state.Model != nil {
		s.contextWindow = state.Model.ContextWindow
	}
	s.mu.Unlock()
	s.deps.Registry.Update(s.threadID, func(rec *chat.ThreadRecord) {
		if state.SessionFile != "" {
			rec.NativeID = state.SessionFile
		}
		if state.Model != nil {
			rec.Options.Model = ptr(state.Model.option())
		}
		if state.ThinkingLevel != "" {
			rec.Options.Set("thinking", state.ThinkingLevel)
		}
	})
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

func (s *session) close() { s.stopProcess() }

// onExit fires when the subprocess is gone. A death mid-turn is a fatal stream
// error the client reconnects from; a death while idle is invisible, because
// the next prompt starts a fresh process on the same session file.
func (s *session) onExit(err error) {
	s.mu.Lock()
	wasRunning := s.running
	s.proc = nil
	s.turn, s.running, s.interrupted = "", false, false
	s.turnStarted = time.Time{}
	s.mu.Unlock()

	s.cancelPending()
	if wasRunning {
		message := "the pi process exited"
		if err != nil {
			message = err.Error()
		}
		s.publish(protocol.StreamError(s.threadID, protocol.ErrHarnessExited, message, true))
	}
	s.setState(protocol.StateIdle)
}

// --- commands ---

func (s *session) prompt(ctx context.Context, rec chat.ThreadRecord, cmd protocol.ClientCommand) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return protocol.Errorf(protocol.CodeThreadBusy, "a turn is already running")
	}
	// The turn is claimed before the process is even up: a second prompt
	// racing this one is refused as busy, and the echo pi sends back for
	// this prompt is the turn's first user message whatever else arrives.
	s.turn, s.running, s.interrupted, s.echoed = cmd.ClientMessageID, true, false, false
	s.turnUsage, s.turnCost, s.turnFailed = protocol.Usage{}, 0, false
	s.turnStarted = time.Now().UTC()
	started := s.turnStarted
	s.lastActive = time.Now()
	s.mu.Unlock()

	proc, err := s.ensureRunning(ctx, rec)
	if err != nil {
		s.abandonTurn()
		return err
	}
	request := map[string]any{"type": "prompt", "message": cmd.Text}
	if len(cmd.Images) > 0 {
		request["images"] = promptImages(cmd.Images)
	}
	// The response is pi's preflight verdict; it can arrive after the first
	// events of the turn, which is why the turn was claimed above.
	if _, err := proc.request(ctx, request, requestTimeout); err != nil {
		s.abandonTurn()
		return protocol.Errorf(protocol.CodeAgentUnavailable, "pi refused the prompt: %v", err)
	}
	s.setState(protocol.StateRunning)
	s.publish(protocol.TurnStarted(s.threadID, cmd.ClientMessageID, started))
	return nil
}

// abandonTurn gives up a turn pi never accepted; no event of it was published.
func (s *session) abandonTurn() {
	s.mu.Lock()
	s.turn, s.running, s.interrupted = "", false, false
	s.turnStarted = time.Time{}
	s.mu.Unlock()
	s.setState(protocol.StateIdle)
}

func (s *session) interrupt(ctx context.Context) error {
	s.mu.Lock()
	proc, running := s.proc, s.running
	if running {
		s.interrupted = true
	}
	s.mu.Unlock()
	if proc == nil || !running {
		return nil
	}
	// An extension blocked on a dialog never lets the run settle, so the
	// dialogs are cancelled before pi is asked to stop.
	s.cancelPending()
	if _, err := proc.request(ctx, map[string]any{"type": "abort"}, requestTimeout); err != nil {
		return protocol.Errorf(protocol.CodeAgentUnavailable, "abort failed: %v", err)
	}
	return nil
}

func (s *session) steer(ctx context.Context, text string) error {
	proc := s.process()
	if proc == nil {
		return protocol.Errorf(protocol.CodeNoRunningTurn, "no turn is running")
	}
	if _, err := proc.request(ctx, map[string]any{"type": "steer", "message": text}, requestTimeout); err != nil {
		return protocol.Errorf(protocol.CodeAgentUnavailable, "steer failed: %v", err)
	}
	return nil
}

// applyOptions pushes the model and the thinking level to a live process; a
// cold thread only needed the record, which the service stored. Whatever pi
// made of them is read back, so a refused model or a clamped level does not
// stay in the row.
func (s *session) applyOptions(ctx context.Context, rec chat.ThreadRecord) error {
	proc := s.process()
	if proc == nil {
		return nil
	}
	var failure error
	if rec.Options.Model != nil {
		provider, modelID := splitModel(*rec.Options.Model)
		request := map[string]any{"type": "set_model", "provider": provider, "modelId": modelID}
		if _, err := proc.request(ctx, request, requestTimeout); err != nil {
			failure = protocol.Errorf(protocol.CodeOptionInvalid, "cannot set model %s: %v", *rec.Options.Model, err)
		}
	}
	if level := rec.Options.Setting("thinking"); level != "" && failure == nil {
		request := map[string]any{"type": "set_thinking_level", "level": level}
		if _, err := proc.request(ctx, request, requestTimeout); err != nil {
			failure = protocol.Errorf(protocol.CodeOptionInvalid, "cannot set thinking level %s: %v", level, err)
		}
	}
	raw, err := proc.request(ctx, map[string]any{"type": "get_state"}, requestTimeout)
	if err != nil && failure == nil {
		return protocol.Errorf(protocol.CodeAgentUnavailable, "pi did not answer get_state: %v", err)
	}
	var state sessionState
	if err == nil {
		if err := json.Unmarshal(raw, &state); err != nil && failure == nil {
			return protocol.Errorf(protocol.CodeAgentUnavailable, "pi returned a state this build cannot read: %v", err)
		}
		s.reconcile(state)
	}
	return failure
}

func (s *session) respond(requestID string, decision protocol.InteractionDecision) error {
	s.mu.Lock()
	p, ok := s.pending[requestID]
	delete(s.pending, requestID)
	proc, running := s.proc, s.running
	s.mu.Unlock()
	if !ok {
		return protocol.Errorf(protocol.CodeInteractionNotPending, "no pending interaction %s", requestID)
	}
	if proc == nil {
		s.publish(protocol.InteractionResolved(s.threadID, requestID, nil))
		return protocol.Errorf(protocol.CodeAgentUnavailable, "%v", errProcessGone)
	}
	if err := proc.send(uiResponse(requestID, p.method, decision)); err != nil {
		s.publish(protocol.InteractionResolved(s.threadID, requestID, nil))
		return protocol.Errorf(protocol.CodeAgentUnavailable, "cannot answer pi: %v", err)
	}
	s.publish(protocol.InteractionResolved(s.threadID, requestID, &decision))
	if running {
		s.setState(protocol.StateRunning)
	}
	return nil
}

// cancelPending ends every unanswered dialog, which is what an interrupt, a
// finished turn or a dead process does to them. The extension sees each one
// as cancelled, which is its default answer.
func (s *session) cancelPending() {
	s.mu.Lock()
	waiting := s.pending
	s.pending = map[string]*pending{}
	proc := s.proc
	s.mu.Unlock()
	for id := range waiting {
		if proc != nil {
			_ = proc.send(map[string]any{"type": "extension_ui_response", "id": id, "cancelled": true})
		}
		s.publish(protocol.InteractionResolved(s.threadID, id, nil))
	}
}

// --- incoming ---

func (s *session) onEvent(kind string, raw json.RawMessage) {
	s.mu.Lock()
	s.lastActive = time.Now()
	s.mu.Unlock()

	switch kind {
	case "agent_start":
		s.openTurn()
	case "message_start":
		s.onMessageStart(raw)
	case "message_update":
		s.onMessageUpdate(raw)
	case "message_end":
		s.onMessageEnd(raw)
	case "tool_execution_update":
		s.onToolUpdate(raw)
	case "agent_settled":
		s.onSettled()
	case "compaction_end":
		s.onCompaction(raw)
	case "auto_retry_start":
		s.onRetry(raw)
	case "thinking_level_changed":
		s.onThinkingChanged(raw)
	}
}

// openTurn covers the turn nobody prompted: an extension can drive the agent
// on its own, and content arriving with no prompt behind it is bracketed like
// any other turn, with an empty client_message_id saying it was not the
// owner's.
func (s *session) openTurn() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.turn, s.running, s.interrupted, s.echoed = "", true, false, false
	s.turnUsage, s.turnCost, s.turnFailed = protocol.Usage{}, 0, false
	s.turnStarted = time.Now().UTC()
	started := s.turnStarted
	s.mu.Unlock()
	s.publish(protocol.TurnStarted(s.threadID, "", started))
	s.setState(protocol.StateRunning)
}

type messageEvent struct {
	Message agentMessage `json:"message"`
}

func (s *session) onMessageStart(raw json.RawMessage) {
	var env messageEvent
	if json.Unmarshal(raw, &env) != nil || env.Message.Role != "assistant" {
		return
	}
	s.mu.Lock()
	s.messageID = env.Message.id()
	s.blockTools = map[int]string{}
	s.mu.Unlock()
}

// updateEvent is a message_update as pi writes it over RPC: the delta alone,
// without the partial message, plus the call id and name on toolcall_start.
type updateEvent struct {
	Event struct {
		Type         string `json:"type"`
		ContentIndex int    `json:"contentIndex"`
		Delta        string `json:"delta"`
		ID           string `json:"id"`
		ToolName     string `json:"toolName"`
		ToolCall     struct {
			ID        string          `json:"id"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"toolCall"`
	} `json:"assistantMessageEvent"`
}

func (s *session) onMessageUpdate(raw json.RawMessage) {
	var env updateEvent
	if json.Unmarshal(raw, &env) != nil {
		return
	}
	event := env.Event
	s.mu.Lock()
	if event.Type == "toolcall_start" {
		s.blockTools[event.ContentIndex] = event.ID
	}
	messageID, toolUseID := s.messageID, s.blockTools[event.ContentIndex]
	s.mu.Unlock()

	switch event.Type {
	case "text_delta":
		s.publish(protocol.TextDelta(s.threadID, messageID, event.ContentIndex, event.Delta, nil))
	case "thinking_delta":
		s.publish(protocol.ThinkingDelta(s.threadID, messageID, event.ContentIndex, event.Delta, nil))
	case "toolcall_start":
		s.publish(protocol.ToolUseStart(s.threadID, messageID, event.ContentIndex, event.ID, event.ToolName, toolKind(event.ToolName), nil))
	case "toolcall_delta":
		if toolUseID != "" {
			s.publish(protocol.ToolInputDelta(s.threadID, toolUseID, event.Delta))
		}
	case "toolcall_end":
		input := event.ToolCall.Arguments
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		s.publish(protocol.ToolUseEnd(s.threadID, event.ToolCall.ID, input))
	}
}

// onMessageEnd settles one message. The owner's prompt comes back from pi as
// the turn's first user message and is claimed by its client_message_id; a
// later user message is a steer. An assistant message adds to the turn's
// usage and cost, and says how full the window is now.
func (s *session) onMessageEnd(raw json.RawMessage) {
	var env messageEvent
	if json.Unmarshal(raw, &env) != nil {
		s.publish(protocol.StreamError(s.threadID, protocol.ErrUnrenderable, "unreadable message from pi", false))
		return
	}
	m := env.Message

	var clientMessageID *string
	var window *protocol.ContextUsage
	var spent *protocol.Usage
	s.mu.Lock()
	turn := s.turn
	switch m.Role {
	case "user":
		if !s.echoed && s.turn != "" {
			clientMessageID = ptr(s.turn)
		}
		s.echoed = true
	case "assistant":
		if m.Usage != nil {
			s.turnUsage.Add(protocol.Usage{
				InputTokens:      m.Usage.Input,
				OutputTokens:     m.Usage.Output,
				CacheReadTokens:  m.Usage.CacheRead,
				CacheWriteTokens: m.Usage.CacheWrite,
				ReasoningTokens:  m.Usage.Reasoning,
			})
			s.turnCost += m.Usage.Cost.Total
			total := s.turnUsage
			spent = &total
		}
		if m.StopReason == "error" {
			s.turnFailed = true
		}
		// An aborted or failed response reports no usable usage; the window
		// reading stays at the last complete response.
		if m.Usage != nil && m.StopReason != "error" && m.StopReason != "aborted" && s.contextWindow > 0 {
			usage := protocol.ContextUsage{
				TotalTokens: contextTokens(*m.Usage),
				MaxTokens:   s.contextWindow,
				Categories:  []protocol.ContextCategory{},
			}
			s.contextUsage = &usage
			window = &usage
		}
	case "toolResult":
		delete(s.toolOutput, m.ToolCallID)
	}
	s.mu.Unlock()

	if entry, ok := messageEntry(s.threadID, m, clientMessageID); ok {
		s.publish(entry)
	}
	if m.Role != "assistant" {
		return
	}
	if m.StopReason == "error" {
		message := cmp.Or(m.ErrorMessage, "pi reported an error without a message")
		s.publish(protocol.StreamError(s.threadID, classify(message), message, false))
	}
	if window != nil {
		s.publish(protocol.ContextUsageChanged(s.threadID, *window))
	}
	if spent != nil {
		s.publish(protocol.TurnUsage(s.threadID, turn, *spent))
	}
}

type toolUpdateEvent struct {
	ToolCallID    string `json:"toolCallId"`
	PartialResult struct {
		Content []contentPart `json:"content"`
	} `json:"partialResult"`
}

// onToolUpdate turns pi's cumulative output snapshots into deltas. A snapshot
// that does not extend the previous one -- pi truncates long output from the
// front -- is sent whole.
func (s *session) onToolUpdate(raw json.RawMessage) {
	var env toolUpdateEvent
	if json.Unmarshal(raw, &env) != nil || env.ToolCallID == "" {
		return
	}
	var text strings.Builder
	for _, part := range env.PartialResult.Content {
		if part.Type == "text" {
			text.WriteString(part.Text)
		}
	}
	snapshot := text.String()

	s.mu.Lock()
	previous := s.toolOutput[env.ToolCallID]
	s.toolOutput[env.ToolCallID] = snapshot
	s.mu.Unlock()

	delta := snapshot
	if strings.HasPrefix(snapshot, previous) {
		delta = snapshot[len(previous):]
	}
	if delta == "" {
		return
	}
	s.publish(protocol.ToolOutputDelta(s.threadID, env.ToolCallID, delta))
}

// onSettled closes the turn. agent_settled is pi's word that nothing more
// will happen on its own -- no retry, no queued follow-up -- so it is the
// turn boundary rather than agent_end, which a retry can follow.
func (s *session) onSettled() {
	s.cancelPending()

	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	turn := s.turn
	status := protocol.TurnCompleted
	switch {
	case s.interrupted:
		status = protocol.TurnInterrupted
	case s.turnFailed:
		status = protocol.TurnFailed
	}
	spent, cost := s.turnUsage, s.turnCost
	summary := protocol.TurnSummary{
		Status: status, Usage: &spent, CostUSD: &cost,
		StartedAt: s.turnStarted, FinishedAt: time.Now().UTC(),
	}
	s.lastTurn = &summary
	s.turnStarted = time.Time{}
	s.turn, s.running, s.interrupted, s.messageID = "", false, false, ""
	s.blockTools = map[int]string{}
	s.mu.Unlock()

	s.publish(protocol.TurnFinished(s.threadID, turn, summary))
	s.setState(protocol.StateIdle)
	s.deps.Registry.Touch(s.threadID)
}

type compactionEvent struct {
	Reason string `json:"reason"`
	Result *struct {
		TokensBefore         int  `json:"tokensBefore"`
		EstimatedTokensAfter *int `json:"estimatedTokensAfter"`
	} `json:"result"`
}

func (s *session) onCompaction(raw json.RawMessage) {
	var env compactionEvent
	if json.Unmarshal(raw, &env) != nil || env.Result == nil {
		return
	}
	reason := protocol.CompactAuto
	if env.Reason == "manual" {
		reason = protocol.CompactManual
	}
	s.publish(protocol.ContextBoundary(s.threadID, reason, ptr(env.Result.TokensBefore)))

	if env.Result.EstimatedTokensAfter == nil {
		return
	}
	s.mu.Lock()
	if s.contextUsage == nil {
		s.mu.Unlock()
		return
	}
	usage := *s.contextUsage
	usage.TotalTokens = *env.Result.EstimatedTokensAfter
	s.contextUsage = &usage
	s.mu.Unlock()
	s.publish(protocol.ContextUsageChanged(s.threadID, usage))
}

type retryEvent struct {
	Attempt      int    `json:"attempt"`
	MaxAttempts  int    `json:"maxAttempts"`
	DelayMs      int    `json:"delayMs"`
	ErrorMessage string `json:"errorMessage"`
}

// onRetry surfaces pi's automatic retry, which it only does for transient
// provider failures such as an overloaded or rate-limited endpoint.
func (s *session) onRetry(raw json.RawMessage) {
	var env retryEvent
	if json.Unmarshal(raw, &env) != nil {
		return
	}
	s.publish(protocol.Notice(s.threadID, protocol.NoticeRateLimit,
		fmt.Sprintf("%s; retrying in %ds (attempt %d/%d)", env.ErrorMessage, env.DelayMs/1000, env.Attempt, env.MaxAttempts)))
}

// onThinkingChanged follows a level pi changed on its own, which it does when
// a model change makes the current level impossible.
func (s *session) onThinkingChanged(raw json.RawMessage) {
	var env struct {
		Level string `json:"level"`
	}
	if json.Unmarshal(raw, &env) != nil || env.Level == "" {
		return
	}
	s.deps.Registry.Update(s.threadID, func(rec *chat.ThreadRecord) { rec.Options.Set("thinking", env.Level) })
}

// onUIRequest turns an extension's dialog into a question the owner answers
// from the browser. Fire-and-forget methods (notify, status, widgets) have no
// place in the transcript and are dropped.
func (s *session) onUIRequest(raw json.RawMessage) {
	var req uiRequest
	if json.Unmarshal(raw, &req) != nil || req.ID == "" || !req.blocking() {
		return
	}
	request := protocol.InteractionRequest{RequestID: req.ID, CreatedAt: time.Now(), Payload: req.question()}
	s.mu.Lock()
	s.pending[req.ID] = &pending{request: request, method: req.Method}
	s.mu.Unlock()
	s.publish(protocol.InteractionRequested(s.threadID, request))
	s.setState(protocol.StateWaitingInput)
}
