package protocol

import (
	"encoding/json"
	"time"
)

// EventBase carries the three fields every server event has. SSE framing is
// "event: <type>\ndata: <json>\n\n"; the stream carries increments only. A
// client that (re)connects opens /events first, then fetches the thread detail
// and /messages, deduplicating by id -- the other order drops whatever lands
// in the gap, a permission prompt included.
type EventBase struct {
	Type     string    `json:"type"`
	Ts       time.Time `json:"ts"`
	ThreadID string    `json:"thread_id"`
}

type ServerEvent interface {
	base() EventBase
}

func (e EventBase) base() EventBase { return e }

func newBase(kind, threadID string) EventBase {
	return EventBase{Type: kind, Ts: time.Now().UTC(), ThreadID: threadID}
}

// EventType is the discriminator, for a caller that only has the interface.
func EventType(e ServerEvent) string { return e.base().Type }

// EventThreadID is which thread the event belongs to.
func EventThreadID(e ServerEvent) string { return e.base().ThreadID }

// SSEFrame encodes one event as a text/event-stream frame.
func SSEFrame(e ServerEvent) []byte {
	payload, err := json.Marshal(e)
	if err != nil {
		// Every event is a plain struct of JSON-safe fields; a failure here is
		// a programming error, and a dropped frame beats a torn stream.
		return nil
	}
	frame := make([]byte, 0, len(payload)+32)
	frame = append(frame, "event: "...)
	frame = append(frame, e.base().Type...)
	frame = append(frame, "\ndata: "...)
	frame = append(frame, payload...)
	return append(frame, "\n\n"...)
}

// ThreadUpdatedEvent says the directory row changed: run state (including
// "starting" while a harness restarts), a knob the harness itself moved, the
// title, or updated_at. The client replaces its row; the state machine lives on
// the server. Also carried on the global directory stream.
type ThreadUpdatedEvent struct {
	EventBase
	Summary ThreadSummary `json:"summary"`
}

func ThreadUpdated(s ThreadSummary) ThreadUpdatedEvent {
	return ThreadUpdatedEvent{newBase("thread_updated", s.ThreadID), s}
}

// ThreadDeletedEvent only ever reaches the global directory stream; a per-thread
// stream ends instead.
type ThreadDeletedEvent struct {
	EventBase
}

func ThreadDeleted(threadID string) ThreadDeletedEvent {
	return ThreadDeletedEvent{newBase("thread_deleted", threadID)}
}

// ContextUsageEvent: Claude get_context_usage() after each turn, Codex
// thread/tokenUsage/updated, OpenCode message tokens.
type ContextUsageEvent struct {
	EventBase
	Usage ContextUsage `json:"usage"`
}

func ContextUsageChanged(threadID string, u ContextUsage) ContextUsageEvent {
	return ContextUsageEvent{newBase("context_usage", threadID), u}
}

// TurnStartedEvent opens a turn. Claude: emitted by the session before the
// first message of a prompt. Codex: turn/started.
type TurnStartedEvent struct {
	EventBase
	ClientMessageID string `json:"client_message_id"`
}

func TurnStarted(threadID, clientMessageID string) TurnStartedEvent {
	return TurnStartedEvent{newBase("turn_started", threadID), clientMessageID}
}

// TurnFinishedEvent closes it, carrying the prompt that opened the turn so a
// pair needs no bracketing.
type TurnFinishedEvent struct {
	EventBase
	ClientMessageID string      `json:"client_message_id"`
	Summary         TurnSummary `json:"summary"`
}

func TurnFinished(threadID, clientMessageID string, s TurnSummary) TurnFinishedEvent {
	return TurnFinishedEvent{newBase("turn_finished", threadID), clientMessageID, s}
}

// UserMessageEvent: with a client_message_id it echoes an owner prompt, with a
// parent_tool_use_id it is a subagent prompt, with neither the harness
// synthesized it.
type UserMessageEvent struct {
	EventBase
	MessageID       string      `json:"message_id"`
	Blocks          []UserBlock `json:"blocks"`
	ClientMessageID *string     `json:"client_message_id"`
	ParentToolUseID *string     `json:"parent_tool_use_id"`
}

func (UserMessageEvent) transcriptEntry() {}

// AssistantMessageEvent settles one assistant message. Deltas stream first;
// this event carries the final blocks and the client replaces its assembly.
// Granularity is the backend's; the client appends by message_id.
type AssistantMessageEvent struct {
	EventBase
	MessageID       string         `json:"message_id"`
	Blocks          []ContentBlock `json:"blocks"`
	ParentToolUseID *string        `json:"parent_tool_use_id"`
}

func (AssistantMessageEvent) transcriptEntry() {}

type TextDeltaEvent struct {
	EventBase
	MessageID       string  `json:"message_id"`
	BlockIndex      int     `json:"block_index"`
	Text            string  `json:"text"`
	ParentToolUseID *string `json:"parent_tool_use_id"`
}

func TextDelta(threadID, messageID string, index int, text string, parent *string) TextDeltaEvent {
	return TextDeltaEvent{newBase("text_delta", threadID), messageID, index, text, parent}
}

type ThinkingDeltaEvent struct {
	EventBase
	MessageID       string  `json:"message_id"`
	BlockIndex      int     `json:"block_index"`
	Text            string  `json:"text"`
	ParentToolUseID *string `json:"parent_tool_use_id"`
}

func ThinkingDelta(threadID, messageID string, index int, text string, parent *string) ThinkingDeltaEvent {
	return ThinkingDeltaEvent{newBase("thinking_delta", threadID), messageID, index, text, parent}
}

type ToolUseStartEvent struct {
	EventBase
	MessageID       string   `json:"message_id"`
	BlockIndex      int      `json:"block_index"`
	ToolUseID       string   `json:"tool_use_id"`
	Name            string   `json:"name"`
	ToolKind        ToolKind `json:"tool_kind"`
	ParentToolUseID *string  `json:"parent_tool_use_id"`
}

// ToolInputDeltaEvent is Claude's input_json_delta; Codex and OpenCode deliver
// the whole input at once and never emit it.
type ToolInputDeltaEvent struct {
	EventBase
	ToolUseID   string `json:"tool_use_id"`
	PartialJSON string `json:"partial_json"`
}

type ToolUseEndEvent struct {
	EventBase
	ToolUseID string          `json:"tool_use_id"`
	Input     json.RawMessage `json:"input"`
}

// ToolOutputDeltaEvent is Codex's commandExecution/outputDelta; Claude has none.
type ToolOutputDeltaEvent struct {
	EventBase
	ToolUseID string `json:"tool_use_id"`
	Text      string `json:"text"`
}

type ToolResultEvent struct {
	EventBase
	ToolUseID string         `json:"tool_use_id"`
	Content   []ContentBlock `json:"content"`
	IsError   bool           `json:"is_error"`
}

func (ToolResultEvent) transcriptEntry() {}

type InteractionRequestEvent struct {
	EventBase
	Request InteractionRequest `json:"request"`
}

func InteractionRequested(threadID string, r InteractionRequest) InteractionRequestEvent {
	return InteractionRequestEvent{newBase("interaction_request", threadID), r}
}

// InteractionResolvedEvent says the request left the pending list. Decision is
// what answered it, possibly from another device; nil means the turn ended first.
type InteractionResolvedEvent struct {
	EventBase
	RequestID string               `json:"request_id"`
	Decision  *InteractionDecision `json:"decision"`
}

func InteractionResolved(threadID, requestID string, d *InteractionDecision) InteractionResolvedEvent {
	return InteractionResolvedEvent{newBase("interaction_resolved", threadID), requestID, d}
}

type ContextBoundaryEvent struct {
	EventBase
	Reason       ContextBoundaryReason `json:"reason"`
	TokensBefore *int                  `json:"tokens_before"` // Claude: pre_tokens
}

func (ContextBoundaryEvent) transcriptEntry() {}

// NoticeEvent is owner-facing text that is neither a message nor an error.
type NoticeEvent struct {
	EventBase
	Kind    NoticeKind `json:"kind"`
	Message string     `json:"message"`
}

func Notice(threadID string, kind NoticeKind, message string) NoticeEvent {
	return NoticeEvent{newBase("notice", threadID), kind, message}
}

// ErrorEvent ends the stream when fatal; a reconnect reopens it.
type ErrorEvent struct {
	EventBase
	Code    StreamErrorCode `json:"code"`
	Message string          `json:"message"`
	Fatal   bool            `json:"fatal"`
}

func StreamError(threadID string, code StreamErrorCode, message string, fatal bool) ErrorEvent {
	return ErrorEvent{newBase("error", threadID), code, message, fatal}
}

// TranscriptEntry is what both harnesses persist: user messages, assistant
// messages, tool results and context boundaries. System entries are the
// backend's to filter.
type TranscriptEntry interface {
	ServerEvent
	transcriptEntry()
}

// TranscriptPage is one page of settled transcript in chronological order;
// NextCursor points at the older page.
type TranscriptPage struct {
	Entries    []TranscriptEntry `json:"entries"`
	NextCursor *string           `json:"next_cursor"`
}

// Constructors for the events whose payloads are wide enough that a positional
// literal would be unreadable at the call site.

func UserMessage(threadID, messageID string, blocks []UserBlock, clientMessageID, parent *string) UserMessageEvent {
	return UserMessageEvent{newBase("user_message", threadID), messageID, blocks, clientMessageID, parent}
}

func AssistantMessage(threadID, messageID string, blocks []ContentBlock, parent *string) AssistantMessageEvent {
	return AssistantMessageEvent{newBase("assistant_message", threadID), messageID, blocks, parent}
}

func ToolUseStart(threadID, messageID string, index int, toolUseID, name string, kind ToolKind, parent *string) ToolUseStartEvent {
	return ToolUseStartEvent{newBase("tool_use_start", threadID), messageID, index, toolUseID, name, kind, parent}
}

func ToolInputDelta(threadID, toolUseID, partialJSON string) ToolInputDeltaEvent {
	return ToolInputDeltaEvent{newBase("tool_input_delta", threadID), toolUseID, partialJSON}
}

func ToolUseEnd(threadID, toolUseID string, input json.RawMessage) ToolUseEndEvent {
	return ToolUseEndEvent{newBase("tool_use_end", threadID), toolUseID, input}
}

func ToolOutputDelta(threadID, toolUseID, text string) ToolOutputDeltaEvent {
	return ToolOutputDeltaEvent{newBase("tool_output_delta", threadID), toolUseID, text}
}

func ToolResult(threadID, toolUseID string, content []ContentBlock, isError bool) ToolResultEvent {
	return ToolResultEvent{newBase("tool_result", threadID), toolUseID, content, isError}
}

func ContextBoundary(threadID string, reason ContextBoundaryReason, tokensBefore *int) ContextBoundaryEvent {
	return ContextBoundaryEvent{newBase("context_boundary", threadID), reason, tokensBefore}
}

// At returns a copy of the event with its timestamp replaced, for a transcript
// entry replayed from disk where the harness recorded the real time.
func (e UserMessageEvent) At(ts time.Time) UserMessageEvent           { e.Ts = ts; return e }
func (e AssistantMessageEvent) At(ts time.Time) AssistantMessageEvent { e.Ts = ts; return e }
func (e ToolResultEvent) At(ts time.Time) ToolResultEvent             { e.Ts = ts; return e }
func (e ContextBoundaryEvent) At(ts time.Time) ContextBoundaryEvent   { e.Ts = ts; return e }
