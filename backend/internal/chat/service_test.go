package chat_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/lmy375/agent-web/backend/internal/chat"
	"github.com/lmy375/agent-web/backend/internal/protocol"
)

// fakeBackend is a harness that records what reached it. The point of these
// tests is that a backend never sees illegal input: every rule below is one the
// service applies once so that no adapter has to.
type fakeBackend struct {
	kind     protocol.AgentKind
	caps     protocol.AgentCapabilities
	groups   []protocol.OptionGroup
	prompts  []protocol.ClientCommand
	pending  []protocol.InteractionRequest
	steers   []string
	options  []protocol.ThreadOptions
	answered []protocol.InteractionDecision
	stopped  []string
}

func (f *fakeBackend) Kind() protocol.AgentKind                 { return f.kind }
func (f *fakeBackend) Label() string                            { return string(f.kind) }
func (f *fakeBackend) Capabilities() protocol.AgentCapabilities { return f.caps }

func (f *fakeBackend) RuntimeInfo(context.Context) protocol.AgentRuntimeInfo {
	return protocol.AgentRuntimeInfo{
		DefaultCwd: ".",
		Defaults: protocol.ThreadOptions{
			Model:    ptr("m1"),
			Settings: map[string]string{"permission-mode": "default"},
		},
		Models:   []protocol.ModelOption{{ID: "m1", Label: "M1"}},
		Groups:   f.groups,
		Commands: []protocol.SlashCommandInfo{},
	}
}

func (f *fakeBackend) RunState(string) protocol.ThreadRunState { return protocol.StateIdle }
func (f *fakeBackend) LiveState(string) chat.LiveState         { return chat.LiveState{Pending: f.pending} }

func (f *fakeBackend) Transcript(context.Context, chat.ThreadRecord, string, int) (protocol.TranscriptPage, error) {
	return protocol.TranscriptPage{}, nil
}

func (f *fakeBackend) Prompt(_ context.Context, _ chat.ThreadRecord, cmd protocol.ClientCommand) error {
	f.prompts = append(f.prompts, cmd)
	return nil
}
func (f *fakeBackend) Interrupt(context.Context, string) error { return nil }
func (f *fakeBackend) Steer(_ context.Context, _, text string) error {
	f.steers = append(f.steers, text)
	return nil
}
func (f *fakeBackend) SetOptions(_ context.Context, rec chat.ThreadRecord) error {
	f.options = append(f.options, rec.Options)
	return nil
}
func (f *fakeBackend) Respond(_ context.Context, _, _ string, d protocol.InteractionDecision) error {
	f.answered = append(f.answered, d)
	return nil
}
func (f *fakeBackend) StopTask(_ context.Context, _, taskID string) error {
	f.stopped = append(f.stopped, taskID)
	return nil
}
func (f *fakeBackend) Discard(context.Context, chat.ThreadRecord) error { return nil }
func (f *fakeBackend) Close()                                           {}

func newService(t *testing.T, backends ...chat.Backend) *chat.Service {
	t.Helper()
	registry, err := chat.NewRegistry(filepath.Join(t.TempDir(), "threads.db"))
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	return chat.NewService(registry, chat.NewHub(), backends...)
}

func claudeLike(kind protocol.AgentKind) *fakeBackend {
	return &fakeBackend{kind: kind, caps: protocol.AgentCapabilities{
		MaxImagesPerPrompt: 2,
		MaxImageBytes:      16,
		SupportsSteer:      false,
	}, groups: []protocol.OptionGroup{
		{ID: "permission-mode", Label: "permission-mode", Options: protocol.Choices("default", "plan")},
		{ID: "effort", Label: "effort", Options: protocol.Choices("high")},
	}}
}

func newThread(t *testing.T, svc *chat.Service, kind protocol.AgentKind) string {
	t.Helper()
	summary, err := svc.CreateThread(context.Background(),
		protocol.CreateThreadRequest{AgentKind: kind, Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	return summary.ThreadID
}

func code(t *testing.T, err error) protocol.ErrorCode {
	t.Helper()
	detail, ok := err.(*protocol.Error)
	if !ok {
		t.Fatalf("expected a protocol error, got %v", err)
	}
	return detail.Code
}

// TestCapabilityGatesRunBeforeTheBackend covers the promise the whole
// abstraction rests on: a command that a kind cannot honour is refused by the
// service, so no adapter re-implements a limit or silently ignores one.
func TestCapabilityGatesRunBeforeTheBackend(t *testing.T) {
	backend := claudeLike(protocol.KindClaudeCode)
	svc := newService(t, backend)
	id := newThread(t, svc, protocol.KindClaudeCode)
	ctx := context.Background()

	steer := protocol.ClientCommand{Type: protocol.CmdSteer, Text: "go left"}
	if got := code(t, svc.Handle(ctx, id, steer)); got != protocol.CodeCapabilityUnsupported {
		t.Errorf("steer on a kind that cannot steer: got %s", got)
	}
	if len(backend.steers) != 0 {
		t.Error("the steer reached a backend that does not support it")
	}

	setMode := protocol.ClientCommand{Type: protocol.CmdSetOptions,
		Options: protocol.ThreadOptions{Settings: map[string]string{"permission-mode": "acceptEdits"}}}
	if got := code(t, svc.Handle(ctx, id, setMode)); got != protocol.CodeOptionInvalid {
		t.Errorf("unoffered mode: got %s", got)
	}

	setEffort := protocol.ClientCommand{Type: protocol.CmdSetOptions,
		Options: protocol.ThreadOptions{Settings: map[string]string{"effort": "max"}}}
	if got := code(t, svc.Handle(ctx, id, setEffort)); got != protocol.CodeOptionInvalid {
		t.Errorf("unoffered effort: got %s", got)
	}

	setUnknown := protocol.ClientCommand{Type: protocol.CmdSetOptions,
		Options: protocol.ThreadOptions{Settings: map[string]string{"sandbox": "read-only"}}}
	if got := code(t, svc.Handle(ctx, id, setUnknown)); got != protocol.CodeOptionInvalid {
		t.Errorf("a knob this kind does not have: got %s", got)
	}
	if len(backend.options) != 0 {
		t.Error("an invalid option reached the backend")
	}

	tooMany := prompt("01JBXQ8G7M4K2P9R3T5V7W9Y1Z", "hi")
	tooMany.Images = []protocol.ImageBlock{smallImage(), smallImage(), smallImage()}
	if got := code(t, svc.Handle(ctx, id, tooMany)); got != protocol.CodeTooManyImages {
		t.Errorf("image count over the limit: got %s", got)
	}

	tooBig := prompt("01JBXQ8G7M4K2P9R3T5V7W9Y20", "hi")
	tooBig.Images = []protocol.ImageBlock{{Type: "image", MediaType: "image/png",
		DataBase64: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}}
	if got := code(t, svc.Handle(ctx, id, tooBig)); got != protocol.CodeImageTooLarge {
		t.Errorf("image over the byte limit: got %s", got)
	}
	if len(backend.prompts) != 0 {
		t.Error("a prompt over the image limits reached the backend")
	}
}

// TestPromptIdempotency covers the retry story: the same prompt sent twice runs
// once, and the same id carrying something else is a conflict rather than a
// second turn nobody asked for.
func TestPromptIdempotency(t *testing.T) {
	backend := claudeLike(protocol.KindClaudeCode)
	svc := newService(t, backend)
	id := newThread(t, svc, protocol.KindClaudeCode)
	ctx := context.Background()

	first := prompt("01JBXQ8G7M4K2P9R3T5V7W9Y1Z", "do the thing")
	if err := svc.Handle(ctx, id, first); err != nil {
		t.Fatalf("first prompt: %v", err)
	}
	if err := svc.Handle(ctx, id, first); err != nil {
		t.Fatalf("replayed prompt should be a no-op: %v", err)
	}
	if len(backend.prompts) != 1 {
		t.Errorf("the replay started %d turns", len(backend.prompts))
	}

	conflicting := prompt("01JBXQ8G7M4K2P9R3T5V7W9Y1Z", "something else entirely")
	if got := code(t, svc.Handle(ctx, id, conflicting)); got != protocol.CodeClientMessageConflict {
		t.Errorf("same id, different payload: got %s", got)
	}
}

// TestInteractionAnswersMustMatchTheRequest keeps a client from resolving a
// prompt with a decision that means nothing for it -- answering options to a
// permission request, say -- and from answering one that is already gone.
func TestInteractionAnswersMustMatchTheRequest(t *testing.T) {
	backend := claudeLike(protocol.KindClaudeCode)
	backend.pending = []protocol.InteractionRequest{{
		RequestID: "req-1", CreatedAt: time.Now(),
		Payload: protocol.PermissionPayload{PayloadKind: protocol.InteractionPermission,
			ToolName: "Bash", ToolInput: json.RawMessage("{}")},
	}}
	svc := newService(t, backend)
	id := newThread(t, svc, protocol.KindClaudeCode)
	ctx := context.Background()

	mismatched := protocol.ClientCommand{Type: protocol.CmdInteractionResponse, RequestID: "req-1",
		Decision: protocol.InteractionDecision{Type: protocol.DecisionAnswer,
			Answers: []protocol.QuestionAnswer{{QuestionID: "q", Answers: []string{"yes"}}}}}
	if got := code(t, svc.Handle(ctx, id, mismatched)); got != protocol.CodeDecisionMismatch {
		t.Errorf("answering a permission request: got %s", got)
	}

	unknown := protocol.ClientCommand{Type: protocol.CmdInteractionResponse, RequestID: "gone",
		Decision: protocol.InteractionDecision{Type: protocol.DecisionAllow}}
	if got := code(t, svc.Handle(ctx, id, unknown)); got != protocol.CodeInteractionNotPending {
		t.Errorf("answering a request that is not pending: got %s", got)
	}

	allow := protocol.ClientCommand{Type: protocol.CmdInteractionResponse, RequestID: "req-1",
		Decision: protocol.InteractionDecision{Type: protocol.DecisionAllow}}
	if err := svc.Handle(ctx, id, allow); err != nil {
		t.Fatalf("a matching allow should reach the backend: %v", err)
	}
	if len(backend.answered) != 1 {
		t.Error("the matching decision never reached the backend")
	}
}

// TestDirectoryIsOneListAcrossKinds is the user-visible point of the whole
// abstraction: threads from different harnesses page as one list, newest first.
func TestDirectoryIsOneListAcrossKinds(t *testing.T) {
	claude, codex := claudeLike(protocol.KindClaudeCode), claudeLike(protocol.KindCodex)
	svc := newService(t, claude, codex)

	created := []string{}
	for _, kind := range []protocol.AgentKind{
		protocol.KindClaudeCode, protocol.KindCodex, protocol.KindClaudeCode, protocol.KindCodex,
	} {
		created = append(created, newThread(t, svc, kind))
		time.Sleep(2 * time.Millisecond) // distinct updated_at, which is the sort key
	}

	first, err := svc.ListThreads("", 2)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(first.Threads) != 2 || first.NextCursor == nil {
		t.Fatalf("expected a full first page with a cursor, got %+v", first)
	}
	if first.Threads[0].ThreadID != created[3] || first.Threads[1].ThreadID != created[2] {
		t.Errorf("the first page is not newest-first: %s, %s",
			first.Threads[0].ThreadID, first.Threads[1].ThreadID)
	}

	second, err := svc.ListThreads(*first.NextCursor, 2)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second.Threads) != 2 || second.NextCursor != nil {
		t.Fatalf("expected the rest with no cursor, got %+v", second)
	}
	if second.Threads[0].ThreadID != created[1] || second.Threads[1].ThreadID != created[0] {
		t.Errorf("the second page skipped or repeated a thread: %+v", second.Threads)
	}
	// Both kinds have to be in there; a per-kind cursor would have lost one.
	kinds := map[protocol.AgentKind]int{}
	for _, row := range append(first.Threads, second.Threads...) {
		kinds[row.AgentKind]++
	}
	if kinds[protocol.KindClaudeCode] != 2 || kinds[protocol.KindCodex] != 2 {
		t.Errorf("the union list lost a kind: %v", kinds)
	}
}

// TestCreateThreadRejectsAnUnusableDirectory keeps a thread from being filed
// against a path no harness could start in.
func TestCreateThreadRejectsAnUnusableDirectory(t *testing.T) {
	svc := newService(t, claudeLike(protocol.KindClaudeCode))
	_, err := svc.CreateThread(context.Background(), protocol.CreateThreadRequest{
		AgentKind: protocol.KindClaudeCode, Cwd: filepath.Join(t.TempDir(), "nope"),
	})
	if got := code(t, err); got != protocol.CodeCwdInvalid {
		t.Errorf("missing directory: got %s", got)
	}
}

func prompt(id, text string) protocol.ClientCommand {
	return protocol.ClientCommand{Type: protocol.CmdPrompt, ClientMessageID: id, Text: text}
}

func smallImage() protocol.ImageBlock {
	return protocol.ImageBlock{Type: "image", MediaType: "image/png", DataBase64: "AAAA"}
}

func ptr[T any](v T) *T { return &v }
