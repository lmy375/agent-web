// Package chat is the seam between the HTTP/SSE shell and the agent harnesses.
// A backend owns one harness's process lifecycle, its transcript and its live
// per-thread state; the service owns dispatch, every kind-neutral policy (cwd
// validation, image limits, prompt idempotency, decision matching, the steer
// gate), the thread directory and fan-out.
package chat

import (
	"context"

	"github.com/lmy375/agent-web/backend/internal/protocol"
)

// LiveState is the part of a thread's state that is not in the transcript:
// Claude's pending can_use_tool futures, Codex's unanswered server requests,
// the last token accounting, the last turn. Every field is a current value
// rather than an event, which is why ThreadDetail is a resource.
type LiveState struct {
	Pending      []protocol.InteractionRequest
	ContextUsage *protocol.ContextUsage
	LastTurn     *protocol.TurnSummary
}

// Backend is what one agent kind implements. Every method takes a thread that
// may have no process at all: only Prompt is allowed to start or resume a
// harness, and a process that died is resumed by the next Prompt rather than
// surfaced as a state.
//
// Each command is its own method rather than one Handle(command): adding a
// command then fails to compile in every backend that has not implemented it,
// instead of falling through a switch into a silent 204.
type Backend interface {
	Kind() protocol.AgentKind
	// Label is the human name of the harness, for a client that has no i18n
	// entry for a kind it has never seen.
	Label() string
	// Capabilities is a constant; the service enforces it before a command
	// reaches this backend.
	Capabilities() protocol.AgentCapabilities
	// RuntimeInfo probes the harness and must not fail: a broken probe is an
	// UnavailableReason, so one kind being down never hides the others.
	RuntimeInfo(ctx context.Context) protocol.AgentRuntimeInfo

	// RunState answers for a thread with no process at all, which is Idle.
	RunState(threadID string) protocol.ThreadRunState
	LiveState(threadID string) LiveState

	Transcript(ctx context.Context, rec ThreadRecord, before string, limit int) (protocol.TranscriptPage, error)

	// Prompt starts or resumes the harness as needed. The service has already
	// applied the image limits and dropped replays.
	Prompt(ctx context.Context, rec ThreadRecord, cmd protocol.ClientCommand) error
	Interrupt(ctx context.Context, threadID string) error
	// Steer is gated by Capabilities().SupportsSteer in the service, so only a
	// kind that steers needs a real implementation; the rest embed NoSteer.
	Steer(ctx context.Context, threadID, text string) error
	// SetOptions receives the record with its new options already merged and
	// stored. On a cold thread there is nothing else to do.
	SetOptions(ctx context.Context, rec ThreadRecord) error
	// Respond answers a pending request. The service has matched the decision
	// to the request kind; a request that resolved in between is still this
	// backend's INTERACTION_NOT_PENDING to raise.
	Respond(ctx context.Context, threadID, requestID string, d protocol.InteractionDecision) error

	// Discard stops any process for the thread and removes harness-side state.
	Discard(ctx context.Context, rec ThreadRecord) error
	// Close stops everything this backend owns; the server calls it on shutdown.
	Close()
}

// NoSteer is embedded by a backend whose harness cannot steer a running turn.
type NoSteer struct{ AgentKind protocol.AgentKind }

func (n NoSteer) Steer(context.Context, string, string) error {
	return protocol.Errorf(protocol.CodeCapabilityUnsupported, "%s cannot steer a turn", n.AgentKind)
}

// Deps is what the shell hands a backend: a way to stream an event and a way to
// read and mutate the directory row. A mutation republishes the row itself, so
// no backend composes a ThreadSummary.
type Deps struct {
	Publish  func(protocol.ServerEvent)
	Registry *Registry
}

// StateChanged republishes a thread's directory row after its run state moved.
func (d Deps) StateChanged(threadID string) { d.Registry.Republish(threadID) }
