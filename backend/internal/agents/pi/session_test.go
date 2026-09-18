package pi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lmy375/agent-web/backend/internal/chat"
	"github.com/lmy375/agent-web/backend/internal/protocol"
)

// lineSink stands in for pi's stdin and keeps what the session wrote.
type lineSink struct {
	mu    sync.Mutex
	lines []string
}

func (w *lineSink) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	scanner := bufio.NewScanner(bytes.NewReader(p))
	for scanner.Scan() {
		w.lines = append(w.lines, scanner.Text())
	}
	return len(p), nil
}

func (w *lineSink) Close() error { return nil }

func (w *lineSink) written() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.lines...)
}

// testSession builds a session whose process is a bare stdin sink: events are
// fed straight into the handlers, and every answer the session writes back is
// captured.
func testSession(t *testing.T) (*session, *[]protocol.ServerEvent, *lineSink) {
	t.Helper()
	registry, err := chat.NewRegistry(filepath.Join(t.TempDir(), "threads.db"))
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	t.Cleanup(func() { _ = registry.Close() })

	published := []protocol.ServerEvent{}
	deps := chat.Deps{
		Publish:  func(e protocol.ServerEvent) { published = append(published, e) },
		Registry: registry,
	}
	s := newSession("pi:thread", deps, nil)
	sink := &lineSink{}
	s.proc = &process{stdin: sink, pending: map[string]chan response{}, done: make(chan struct{})}
	return s, &published, sink
}

// startTurn puts the session where prompt leaves it once pi accepted the
// prompt, without a process to talk to.
func startTurn(s *session, clientMessageID string) {
	s.mu.Lock()
	s.turn, s.running, s.echoed = clientMessageID, true, false
	s.state = protocol.StateRunning
	s.contextWindow = 272000
	s.mu.Unlock()
}

func eventTypes(events []protocol.ServerEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = protocol.EventType(e)
	}
	return out
}

func find[T protocol.ServerEvent](t *testing.T, events []protocol.ServerEvent) T {
	t.Helper()
	for _, e := range events {
		if match, ok := e.(T); ok {
			return match
		}
	}
	var zero T
	t.Fatalf("no %T among %v", zero, eventTypes(events))
	return zero
}

// pi echoes the owner's prompt back as the turn's first user message, and a
// steer as a later one; only the first is the owner's local echo.
func TestOwnerPromptIsClaimedOnceAndSteersAreNot(t *testing.T) {
	s, published, _ := testSession(t)
	startTurn(s, "01JBXQ8G7M4K2P9R3T5V7W9Y1Z")

	s.onEvent("agent_start", json.RawMessage(`{"type":"agent_start"}`))
	s.onEvent("message_end", json.RawMessage(`{"type":"message_end","message":{"role":"user","content":"hello","timestamp":1000}}`))
	s.onEvent("message_end", json.RawMessage(`{"type":"message_end","message":{"role":"user","content":[{"type":"text","text":"also do this"}],"timestamp":2000}}`))

	if types := eventTypes(*published); len(types) != 2 || types[0] != "user_message" || types[1] != "user_message" {
		t.Fatalf("published %v, want two user messages and no second turn_started", types)
	}
	first := (*published)[0].(protocol.UserMessageEvent)
	if first.MessageID != "u-1000" || first.ClientMessageID == nil || *first.ClientMessageID != "01JBXQ8G7M4K2P9R3T5V7W9Y1Z" {
		t.Fatalf("the prompt echo must carry the client id: %+v", first)
	}
	second := (*published)[1].(protocol.UserMessageEvent)
	if second.MessageID != "u-2000" || second.ClientMessageID != nil {
		t.Fatalf("a steer carries no client id: %+v", second)
	}
}

// A turn nobody prompted -- an extension driving the agent -- is bracketed
// like any other, with an empty client_message_id.
func TestUnpromptedTurnIsBracketed(t *testing.T) {
	s, published, _ := testSession(t)
	s.onEvent("agent_start", json.RawMessage(`{"type":"agent_start"}`))
	if s.runState() != protocol.StateRunning {
		t.Fatalf("run state is %q, want running", s.runState())
	}
	started := find[protocol.TurnStartedEvent](t, *published)
	if started.ClientMessageID != "" {
		t.Fatalf("an unprompted turn has no client id: %+v", started)
	}
	s.onEvent("agent_settled", json.RawMessage(`{"type":"agent_settled"}`))
	if s.runState() != protocol.StateIdle {
		t.Fatalf("run state after settle is %q, want idle", s.runState())
	}
}

// The assistant stream: deltas keyed by content index under the message id
// pi's timestamp gives, the settled message carrying the whole content, and
// the window reading and the turn's running total taken from the message's
// usage.
func TestAssistantStreamAssembly(t *testing.T) {
	s, published, _ := testSession(t)
	startTurn(s, "01JBXQ8G7M4K2P9R3T5V7W9Y1Z")

	s.onEvent("message_start", json.RawMessage(`{"type":"message_start","message":{"role":"assistant","content":[],"timestamp":5000}}`))
	s.onEvent("message_update", json.RawMessage(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"Let me look."}}`))
	s.onEvent("message_update", json.RawMessage(`{"type":"message_update","assistantMessageEvent":{"type":"toolcall_start","contentIndex":1,"id":"call_1","toolName":"bash"}}`))
	s.onEvent("message_update", json.RawMessage(`{"type":"message_update","assistantMessageEvent":{"type":"toolcall_delta","contentIndex":1,"delta":"{\"command\":"}}`))
	s.onEvent("message_update", json.RawMessage(`{"type":"message_update","assistantMessageEvent":{"type":"toolcall_end","contentIndex":1,"toolCall":{"type":"toolCall","id":"call_1","name":"bash","arguments":{"command":"ls"}}}}`))
	s.onEvent("message_end", json.RawMessage(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Let me look."},{"type":"toolCall","id":"call_1","name":"bash","arguments":{"command":"ls"}}],"usage":{"input":1000,"output":50,"cacheRead":200,"cacheWrite":0,"totalTokens":1250,"cost":{"total":0.0125}},"stopReason":"toolUse","timestamp":5000}}`))

	want := []string{"text_delta", "tool_use_start", "tool_input_delta", "tool_use_end", "assistant_message", "context_usage", "turn_usage"}
	if got := eventTypes(*published); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("published %v, want %v", got, want)
	}
	delta := find[protocol.TextDeltaEvent](t, *published)
	if delta.MessageID != "a-5000" || delta.BlockIndex != 0 {
		t.Fatalf("text delta is %+v", delta)
	}
	start := find[protocol.ToolUseStartEvent](t, *published)
	if start.MessageID != "a-5000" || start.BlockIndex != 1 || start.ToolUseID != "call_1" || start.ToolKind != protocol.ToolShell {
		t.Fatalf("tool use start is %+v", start)
	}
	end := find[protocol.ToolUseEndEvent](t, *published)
	if end.ToolUseID != "call_1" || string(end.Input) != `{"command":"ls"}` {
		t.Fatalf("tool use end is %+v", end)
	}
	message := find[protocol.AssistantMessageEvent](t, *published)
	if message.MessageID != "a-5000" || len(message.Blocks) != 2 {
		t.Fatalf("assistant message is %+v", message)
	}
	if _, ok := message.Blocks[1].(protocol.ToolUseBlock); !ok {
		t.Fatalf("second block should be the tool use, got %T", message.Blocks[1])
	}
	usage := find[protocol.ContextUsageEvent](t, *published)
	if usage.Usage.TotalTokens != 1250 || usage.Usage.MaxTokens != 272000 {
		t.Fatalf("context usage is %+v", usage.Usage)
	}
	spent := find[protocol.TurnUsageEvent](t, *published)
	if spent.Usage.InputTokens != 1000 || spent.Usage.OutputTokens != 50 || spent.Usage.CacheReadTokens != 200 {
		t.Fatalf("turn usage is %+v", spent.Usage)
	}
}

// pi streams a tool's cumulative output; the client wants what changed.
func TestToolOutputSnapshotsBecomeDeltas(t *testing.T) {
	s, published, _ := testSession(t)
	startTurn(s, "01JBXQ8G7M4K2P9R3T5V7W9Y1Z")

	update := func(text string) json.RawMessage {
		return json.RawMessage(`{"type":"tool_execution_update","toolCallId":"call_1","toolName":"bash","partialResult":{"content":[{"type":"text","text":"` + text + `"}]}}`)
	}
	s.onEvent("tool_execution_update", update("ab"))
	s.onEvent("tool_execution_update", update("abcd"))
	s.onEvent("tool_execution_update", update("abcd"))
	s.onEvent("tool_execution_update", update("zzz"))
	s.onEvent("message_end", json.RawMessage(`{"type":"message_end","message":{"role":"toolResult","toolCallId":"call_1","toolName":"bash","content":[{"type":"text","text":"zzz"}],"isError":false,"timestamp":6000}}`))

	deltas := []string{}
	for _, e := range *published {
		if d, ok := e.(protocol.ToolOutputDeltaEvent); ok {
			deltas = append(deltas, d.Text)
		}
	}
	if strings.Join(deltas, "|") != "ab|cd|zzz" {
		t.Fatalf("deltas are %v", deltas)
	}
	result := find[protocol.ToolResultEvent](t, *published)
	if result.ToolUseID != "call_1" || result.IsError {
		t.Fatalf("tool result is %+v", result)
	}
}

func TestTurnEndClassification(t *testing.T) {
	t.Run("an error stop reason fails the turn", func(t *testing.T) {
		s, published, _ := testSession(t)
		startTurn(s, "01JBXQ8G7M4K2P9R3T5V7W9Y1Z")
		s.onEvent("message_end", json.RawMessage(`{"type":"message_end","message":{"role":"assistant","content":[],"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"total":0}},"stopReason":"error","errorMessage":"429 rate limit","timestamp":7000}}`))
		s.onEvent("agent_settled", json.RawMessage(`{"type":"agent_settled"}`))

		failure := find[protocol.ErrorEvent](t, *published)
		if failure.Fatal || failure.Code != protocol.ErrRateLimited {
			t.Fatalf("stream error is %+v", failure)
		}
		finished := find[protocol.TurnFinishedEvent](t, *published)
		if finished.Summary.Status != protocol.TurnFailed || finished.ClientMessageID != "01JBXQ8G7M4K2P9R3T5V7W9Y1Z" {
			t.Fatalf("turn finished is %+v", finished)
		}
		if s.runState() != protocol.StateIdle {
			t.Fatalf("run state is %q, want idle", s.runState())
		}
	})

	t.Run("an interrupt ends the turn as interrupted without an error", func(t *testing.T) {
		s, published, sink := testSession(t)
		startTurn(s, "01JBXQ8G7M4K2P9R3T5V7W9Y1Z")
		s.mu.Lock()
		s.interrupted = true // what interrupt() records before asking pi to abort
		s.mu.Unlock()
		s.onEvent("message_end", json.RawMessage(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"half"}],"usage":{"input":10,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":12,"cost":{"total":0.001}},"stopReason":"aborted","timestamp":8000}}`))
		s.onEvent("agent_settled", json.RawMessage(`{"type":"agent_settled"}`))

		for _, e := range *published {
			if _, ok := e.(protocol.ErrorEvent); ok {
				t.Fatalf("an abort is not an error: %v", eventTypes(*published))
			}
		}
		finished := find[protocol.TurnFinishedEvent](t, *published)
		if finished.Summary.Status != protocol.TurnInterrupted || finished.Summary.CostUSD == nil || *finished.Summary.CostUSD != 0.001 {
			t.Fatalf("turn finished is %+v", finished.Summary)
		}
		if len(sink.written()) != 0 {
			t.Fatalf("nothing should have been written to pi, got %v", sink.written())
		}
	})

	t.Run("a process dying mid-turn is fatal and cancels dialogs", func(t *testing.T) {
		s, published, _ := testSession(t)
		startTurn(s, "01JBXQ8G7M4K2P9R3T5V7W9Y1Z")
		s.onUIRequest(json.RawMessage(`{"type":"extension_ui_request","id":"d1","method":"editor","title":"Edit the plan"}`))
		s.onExit(errors.New("exit status 1: boom"))

		failure := find[protocol.ErrorEvent](t, *published)
		if !failure.Fatal || failure.Code != protocol.ErrHarnessExited || !strings.Contains(failure.Message, "boom") {
			t.Fatalf("stream error is %+v", failure)
		}
		resolved := find[protocol.InteractionResolvedEvent](t, *published)
		if resolved.RequestID != "d1" || resolved.Decision != nil {
			t.Fatalf("the dialog should be resolved without a decision: %+v", resolved)
		}
		if s.runState() != protocol.StateIdle || len(s.liveState().Pending) != 0 {
			t.Fatalf("state %q with %d pending", s.runState(), len(s.liveState().Pending))
		}
	})
}

// An extension's confirm dialog is a question with Yes and No; the answer goes
// back to pi as extension_ui_response and the thread resumes running.
func TestExtensionDialogRoundTrip(t *testing.T) {
	s, published, sink := testSession(t)
	startTurn(s, "01JBXQ8G7M4K2P9R3T5V7W9Y1Z")

	s.onUIRequest(json.RawMessage(`{"type":"extension_ui_request","id":"d1","method":"confirm","title":"Dangerous!","message":"Allow rm -rf?"}`))
	if s.runState() != protocol.StateWaitingInput {
		t.Fatalf("run state is %q, want waiting_input", s.runState())
	}
	requested := find[protocol.InteractionRequestEvent](t, *published)
	question, ok := requested.Request.Payload.(protocol.QuestionPayload)
	if !ok || len(question.Questions) != 1 || len(question.Questions[0].Options) != 2 || question.Questions[0].Options[0].Label != "Yes" {
		t.Fatalf("payload is %+v", requested.Request.Payload)
	}
	if pending := s.liveState().Pending; len(pending) != 1 || pending[0].RequestID != "d1" {
		t.Fatalf("live state pending is %+v", pending)
	}

	if err := s.respond("nope", protocol.InteractionDecision{Type: protocol.DecisionDeny}); err == nil {
		t.Fatal("an unknown request id must be refused")
	}
	decision := protocol.InteractionDecision{Type: protocol.DecisionAnswer,
		Answers: []protocol.QuestionAnswer{{QuestionID: "d1", Answers: []string{"Yes"}}}}
	if err := s.respond("d1", decision); err != nil {
		t.Fatalf("respond: %v", err)
	}
	written := sink.written()
	if len(written) != 1 || written[0] != `{"confirmed":true,"id":"d1","type":"extension_ui_response"}` {
		t.Fatalf("written to pi: %v", written)
	}
	resolved := find[protocol.InteractionResolvedEvent](t, *published)
	if resolved.RequestID != "d1" || resolved.Decision == nil || resolved.Decision.Type != protocol.DecisionAnswer {
		t.Fatalf("resolved is %+v", resolved)
	}
	if s.runState() != protocol.StateRunning {
		t.Fatalf("run state is %q, want running again", s.runState())
	}

	// A dialog still open when the turn settles is cancelled: pi's editor
	// has no timeout of its own and would otherwise block the extension.
	s.onUIRequest(json.RawMessage(`{"type":"extension_ui_request","id":"d2","method":"editor","title":"Notes"}`))
	s.onEvent("agent_settled", json.RawMessage(`{"type":"agent_settled"}`))
	written = sink.written()
	if len(written) != 2 || written[1] != `{"cancelled":true,"id":"d2","type":"extension_ui_response"}` {
		t.Fatalf("written to pi: %v", written)
	}
	var cancelled *protocol.InteractionResolvedEvent
	for _, e := range *published {
		if r, ok := e.(protocol.InteractionResolvedEvent); ok && r.RequestID == "d2" {
			cancelled = &r
		}
	}
	if cancelled == nil || cancelled.Decision != nil {
		t.Fatalf("d2 should be resolved without a decision: %+v", cancelled)
	}
}

// A model pi refuses must not stay in the row, and a level it clamps must be
// read back: both come from the get_state that follows every option change.
func TestApplyOptionsReadsBackWhatPiKept(t *testing.T) {
	s, _, _ := testSession(t)
	proc := s.proc
	rec := chat.ThreadRecord{ThreadID: "pi:thread", AgentKind: protocol.KindPi, Cwd: t.TempDir(),
		Options: protocol.ThreadOptions{Model: ptr("openai-codex/gpt-5.5"), Settings: map[string]string{"thinking": "max"}}}
	if err := s.deps.Registry.Put(rec); err != nil {
		t.Fatal(err)
	}

	// Answer each request as pi would, in the order the session sends them.
	answers := []string{
		`{"type":"response","command":"set_model","success":true,"data":{}}`,
		`{"type":"response","command":"set_thinking_level","success":true}`,
		`{"type":"response","command":"get_state","success":true,"data":{"model":{"id":"gpt-5.5","provider":"openai-codex","contextWindow":272000},"thinkingLevel":"xhigh","sessionFile":"/tmp/s.jsonl"}}`,
	}
	done := make(chan error, 1)
	go func() { done <- s.applyOptions(context.Background(), rec) }()
	for i := 1; i <= len(answers); i++ {
		id := "req_" + strconv.Itoa(i)
		for {
			proc.pendingMu.Lock()
			_, ok := proc.pending[id]
			proc.pendingMu.Unlock()
			if ok {
				break
			}
			time.Sleep(time.Millisecond)
		}
		var r response
		_ = json.Unmarshal([]byte(answers[i-1]), &r)
		r.ID = id
		line, _ := json.Marshal(r)
		proc.resolve(line)
	}
	if err := <-done; err != nil {
		t.Fatalf("applyOptions: %v", err)
	}
	stored, _ := s.deps.Registry.Get("pi:thread")
	if stored.Options.Setting("thinking") != "xhigh" || stored.NativeID != "/tmp/s.jsonl" {
		t.Fatalf("row was not corrected from get_state: %+v", stored)
	}
	s.mu.Lock()
	window := s.contextWindow
	s.mu.Unlock()
	if window != 272000 {
		t.Fatalf("context window is %d", window)
	}
}
