package codex

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/lmy375/agent-web/backend/internal/chat"
	"github.com/lmy375/agent-web/backend/internal/protocol"
)

var capabilities = protocol.AgentCapabilities{
	// No plan: Codex has no plan mode, and a descriptor that lists a mode the
	// harness cannot enter is worse than one that omits it.
	Modes: []protocol.PermissionMode{
		protocol.ModeAsk, protocol.ModeAutoEdit, protocol.ModeFullAuto, protocol.ModeDontAsk,
	},
	Efforts: []protocol.EffortOption{
		{ID: "minimal", Label: "Minimal"}, {ID: "low", Label: "Low"},
		{ID: "medium", Label: "Medium"}, {ID: "high", Label: "High"},
		{ID: "xhigh", Label: "Extra high"},
	},
	MaxImagesPerPrompt: 10,
	MaxImageBytes:      5 << 20,
	SupportsSteer:      true, // turn/steer
	ReportsCost:        false,
	SupportsInterrupt:  true,
}

type Options struct {
	Bin         string
	DefaultCwd  string
	IdleTimeout time.Duration
}

// thread is one conversation inside the shared app-server.
type thread struct {
	mu           sync.Mutex
	state        protocol.ThreadRunState
	turn         string // client_message_id of the running turn
	turnID       string
	interrupted  bool
	pending      map[string]*approval
	contextUsage *protocol.ContextUsage
	// lastUsage is the most recent per-turn accounting; Codex reports it as a
	// notification rather than with turn/completed, so it is kept here.
	lastUsage *protocol.Usage
	lastTurn  *protocol.TurnSummary
	// tools maps a Codex item id to the tool_use id the client already saw.
	tools map[string]bool
}

func newThread() *thread {
	return &thread{state: protocol.StateIdle, pending: map[string]*approval{}, tools: map[string]bool{}}
}

// approval is one unanswered server request, with the JSON-RPC id to answer on.
type approval struct {
	rpcID   json.RawMessage
	method  string
	request protocol.InteractionRequest
}

// Backend drives one `codex app-server` for every thread. The server is started
// on the first probe and kept: it is one process for the whole surface, not one
// per conversation, so there is nothing per-thread to reap.
type Backend struct {
	opts Options
	deps chat.Deps

	mu       sync.Mutex
	server   *client
	starting sync.Mutex
	threads  map[string]*thread
	byNative map[string]string // codex threadId -> our thread id

	runtimeMu sync.Mutex
	runtime   protocol.AgentRuntimeInfo
	runtimeAt time.Time
}

func New(opts Options, deps chat.Deps) *Backend {
	if opts.Bin == "" {
		opts.Bin = "codex"
	}
	return &Backend{
		opts: opts, deps: deps,
		threads: map[string]*thread{}, byNative: map[string]string{},
	}
}

func (b *Backend) Kind() protocol.AgentKind                 { return protocol.KindCodex }
func (b *Backend) Label() string                            { return "Codex" }
func (b *Backend) Capabilities() protocol.AgentCapabilities { return capabilities }

// --- the shared server ---

// ensureServer starts and initializes the app-server if it is not running. The
// starting mutex means a burst of prompts produces one process, not several.
func (b *Backend) ensureServer(ctx context.Context) (*client, error) {
	b.mu.Lock()
	existing := b.server
	b.mu.Unlock()
	if existing != nil {
		return existing, nil
	}

	b.starting.Lock()
	defer b.starting.Unlock()
	b.mu.Lock()
	existing = b.server
	b.mu.Unlock()
	if existing != nil {
		return existing, nil
	}

	cwd := b.opts.DefaultCwd
	if stat, err := os.Stat(cwd); err != nil || !stat.IsDir() {
		cwd = os.TempDir()
	}
	server, err := dial(b.opts.Bin, cwd, rpcHandlers{
		onNotification: b.onNotification,
		onRequest:      b.onRequest,
		onExit:         b.onServerExit,
	})
	if err != nil {
		return nil, protocol.Errorf(protocol.CodeAgentUnavailable, "%v", err)
	}

	var info struct {
		CodexHome string `json:"codexHome"`
	}
	params := map[string]any{"clientInfo": map[string]any{"name": "agent-web", "version": "0.1.0"}}
	if err := server.call(ctx, "initialize", params, &info); err != nil {
		server.stop()
		return nil, protocol.Errorf(protocol.CodeAgentUnavailable, "codex did not initialize: %v", err)
	}
	// The server does nothing until the handshake is acknowledged.
	if err := server.notify("initialized", map[string]any{}); err != nil {
		server.stop()
		return nil, protocol.Errorf(protocol.CodeAgentUnavailable, "%v", err)
	}

	b.mu.Lock()
	b.server = server
	b.mu.Unlock()
	return server, nil
}

// onServerExit forgets every live thread: the app-server dying takes all of
// them with it, and each is resumed by its next prompt.
func (b *Backend) onServerExit(err error) {
	b.mu.Lock()
	b.server = nil
	threads := b.threads
	b.threads = map[string]*thread{}
	b.mu.Unlock()

	message := "the codex app-server exited"
	if err != nil {
		message = err.Error()
	}
	for id, t := range threads {
		t.mu.Lock()
		running := t.turn != ""
		t.mu.Unlock()
		if running {
			b.deps.Publish(protocol.StreamError(id, protocol.ErrHarnessExited, message, true))
		}
		b.deps.StateChanged(id)
	}
}

// --- runtime probe ---

func (b *Backend) RuntimeInfo(ctx context.Context) protocol.AgentRuntimeInfo {
	b.runtimeMu.Lock()
	defer b.runtimeMu.Unlock()
	if b.runtime.UnavailableReason == nil && !b.runtimeAt.IsZero() && time.Since(b.runtimeAt) < 2*time.Minute {
		return b.runtime
	}
	b.runtime = b.probe(ctx)
	b.runtimeAt = time.Now()
	return b.runtime
}

func (b *Backend) probe(ctx context.Context) protocol.AgentRuntimeInfo {
	mode := protocol.ModeAsk
	info := protocol.AgentRuntimeInfo{
		DefaultCwd: b.opts.DefaultCwd,
		Defaults:   protocol.ThreadOptions{Mode: &mode, Effort: strPtr("medium")},
		Models:     []protocol.ModelOption{},
		// The app-server exposes no slash command list; the composer simply
		// offers none for this kind.
		Commands: []protocol.SlashCommandInfo{},
	}
	if _, err := exec.LookPath(b.opts.Bin); err != nil {
		info.UnavailableReason = strPtr("the `codex` CLI is not on PATH; install Codex or set AGENT_WEB_CODEX_PATH")
		return info
	}
	server, err := b.ensureServer(ctx)
	if err != nil {
		info.UnavailableReason = strPtr(err.Error())
		return info
	}
	var models struct {
		Data []modelEntry `json:"data"`
	}
	if err := server.call(ctx, "model/list", map[string]any{}, &models); err != nil {
		info.UnavailableReason = strPtr("codex could not list models: " + err.Error())
		return info
	}
	info.Models = modelOptions(models.Data)
	if len(info.Models) > 0 {
		info.Defaults.Model = &info.Models[0].ID
	}
	return info
}

// --- thread state ---

func (b *Backend) thread(threadID string) (*thread, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.threads[threadID]
	return t, ok
}

func (b *Backend) RunState(threadID string) protocol.ThreadRunState {
	if t, ok := b.thread(threadID); ok {
		t.mu.Lock()
		defer t.mu.Unlock()
		return t.state
	}
	return protocol.StateIdle
}

func (b *Backend) LiveState(threadID string) chat.LiveState {
	t, ok := b.thread(threadID)
	if !ok {
		return chat.LiveState{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	pending := make([]protocol.InteractionRequest, 0, len(t.pending))
	for _, a := range t.pending {
		pending = append(pending, a.request)
	}
	return chat.LiveState{Pending: pending, ContextUsage: t.contextUsage, LastTurn: t.lastTurn}
}

func (b *Backend) setState(threadID string, state protocol.ThreadRunState) {
	t, ok := b.thread(threadID)
	if !ok {
		return
	}
	t.mu.Lock()
	changed := t.state != state
	t.state = state
	t.mu.Unlock()
	if changed {
		b.deps.StateChanged(threadID)
	}
}

// --- commands ---

// Prompt starts the turn, opening or resuming the Codex thread first. A thread
// only gets a native id here, on its first prompt: thread/start is the moment
// Codex has a conversation at all.
func (b *Backend) Prompt(ctx context.Context, rec chat.ThreadRecord, cmd protocol.ClientCommand) error {
	if t, ok := b.thread(rec.ThreadID); ok {
		t.mu.Lock()
		busy := t.turn != ""
		t.mu.Unlock()
		if busy {
			return protocol.Errorf(protocol.CodeThreadBusy, "a turn is already running")
		}
	}
	server, err := b.ensureServer(ctx)
	if err != nil {
		return err
	}
	nativeID, err := b.openThread(ctx, server, rec)
	if err != nil {
		return err
	}

	t, _ := b.thread(rec.ThreadID)
	t.mu.Lock()
	t.turn, t.interrupted = cmd.ClientMessageID, false
	t.mu.Unlock()

	params := map[string]any{
		"threadId":            nativeID,
		"clientUserMessageId": cmd.ClientMessageID,
		"input":               turnInput(cmd),
	}
	native := nativeMode(rec.Options.Mode)
	params["approvalPolicy"] = native.approvalPolicy
	params["sandboxPolicy"] = native.policy()
	if rec.Options.Model != nil {
		params["model"] = *rec.Options.Model
	}
	if rec.Options.Effort != nil {
		params["effort"] = *rec.Options.Effort
	}

	if err := server.call(ctx, "turn/start", params, nil); err != nil {
		t.mu.Lock()
		t.turn = ""
		t.mu.Unlock()
		return protocol.Errorf(protocol.CodeAgentUnavailable, "turn/start failed: %v", err)
	}
	b.setState(rec.ThreadID, protocol.StateRunning)
	return nil
}

// openThread makes sure the app-server has this conversation loaded, starting
// it the first time and resuming it after a server restart.
func (b *Backend) openThread(ctx context.Context, server *client, rec chat.ThreadRecord) (string, error) {
	b.mu.Lock()
	_, live := b.threads[rec.ThreadID]
	b.mu.Unlock()
	if live && rec.NativeID != "" {
		return rec.NativeID, nil
	}

	native := nativeMode(rec.Options.Mode)
	params := map[string]any{
		"cwd":            rec.Cwd,
		"approvalPolicy": native.approvalPolicy,
		"sandbox":        native.sandboxMode,
	}
	if rec.Options.Model != nil {
		params["model"] = *rec.Options.Model
	}

	method := "thread/start"
	if rec.NativeID != "" {
		method, params["threadId"] = "thread/resume", rec.NativeID
	}
	var response struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := server.call(ctx, method, params, &response); err != nil {
		return "", protocol.Errorf(protocol.CodeAgentUnavailable, "%s failed: %v", method, err)
	}
	nativeID := response.Thread.ID
	if nativeID == "" {
		nativeID = rec.NativeID
	}

	b.mu.Lock()
	b.threads[rec.ThreadID] = newThread()
	b.byNative[nativeID] = rec.ThreadID
	b.mu.Unlock()
	if nativeID != rec.NativeID {
		b.deps.Registry.Update(rec.ThreadID, func(r *chat.ThreadRecord) { r.NativeID = nativeID })
	}
	return nativeID, nil
}

// turnInput is the content array turn/start takes.
func turnInput(cmd protocol.ClientCommand) []any {
	input := make([]any, 0, len(cmd.Images)+1)
	for _, image := range cmd.Images {
		input = append(input, map[string]any{
			"type": "image",
			"url":  "data:" + image.MediaType + ";base64," + image.DataBase64,
		})
	}
	if cmd.Text != "" {
		input = append(input, map[string]any{"type": "text", "text": cmd.Text})
	}
	return input
}

func (b *Backend) Interrupt(ctx context.Context, threadID string) error {
	server, native, err := b.live(threadID)
	if err != nil || server == nil {
		return err
	}
	t, _ := b.thread(threadID)
	t.mu.Lock()
	running, turnID := t.turn != "", t.turnID
	if running {
		t.interrupted = true
	}
	t.mu.Unlock()
	if !running {
		return nil
	}
	params := map[string]any{"threadId": native}
	if turnID != "" {
		params["turnId"] = turnID
	}
	if err := server.call(ctx, "turn/interrupt", params, nil); err != nil {
		return protocol.Errorf(protocol.CodeAgentUnavailable, "interrupt failed: %v", err)
	}
	return nil
}

func (b *Backend) Steer(ctx context.Context, threadID, text string) error {
	server, native, err := b.live(threadID)
	if err != nil {
		return err
	}
	t, open := b.thread(threadID)
	if server == nil || !open {
		return protocol.Errorf(protocol.CodeNoRunningTurn, "there is no running turn to steer")
	}
	t.mu.Lock()
	turnID := t.turnID
	t.mu.Unlock()
	// The server requires the turn being steered as a precondition, so a steer
	// aimed at a turn that has already ended is refused rather than applied to
	// whatever is running now.
	if turnID == "" {
		return protocol.Errorf(protocol.CodeNoRunningTurn, "there is no running turn to steer")
	}
	params := map[string]any{
		"threadId":       native,
		"expectedTurnId": turnID,
		"input":          []any{map[string]any{"type": "text", "text": text}},
	}
	if err := server.call(ctx, "turn/steer", params, nil); err != nil {
		return protocol.Errorf(protocol.CodeAgentUnavailable, "steer failed: %v", err)
	}
	return nil
}

// SetOptions has nothing to push: Codex takes model, effort and the sandbox
// pair as turn/start arguments, so the stored record is already the whole
// change and the next turn carries it.
func (b *Backend) SetOptions(context.Context, chat.ThreadRecord) error { return nil }

func (b *Backend) Respond(_ context.Context, threadID, requestID string, decision protocol.InteractionDecision) error {
	t, ok := b.thread(threadID)
	if !ok {
		return protocol.Errorf(protocol.CodeInteractionNotPending, "no pending interaction %s", requestID)
	}
	t.mu.Lock()
	a, found := t.pending[requestID]
	delete(t.pending, requestID)
	running := t.turn != ""
	t.mu.Unlock()
	if !found {
		return protocol.Errorf(protocol.CodeInteractionNotPending, "no pending interaction %s", requestID)
	}

	b.mu.Lock()
	server := b.server
	b.mu.Unlock()
	if server == nil {
		return protocol.Errorf(protocol.CodeAgentUnavailable, "the codex app-server is gone")
	}
	if err := server.respond(a.rpcID, approvalResult(a.method, decision)); err != nil {
		return protocol.Errorf(protocol.CodeAgentUnavailable, "%v", err)
	}
	b.deps.Publish(protocol.InteractionResolved(threadID, requestID, &decision))
	if running {
		b.setState(threadID, protocol.StateRunning)
	}
	return nil
}

func (b *Backend) Transcript(ctx context.Context, rec chat.ThreadRecord, before string, limit int) (protocol.TranscriptPage, error) {
	empty := protocol.TranscriptPage{Entries: []protocol.TranscriptEntry{}}
	if rec.NativeID == "" {
		return empty, nil
	}
	server, err := b.ensureServer(ctx)
	if err != nil {
		return empty, err
	}
	params := map[string]any{"threadId": rec.NativeID, "limit": limit, "sortDirection": "desc"}
	if before != "" {
		params["cursor"] = before
	}
	var response struct {
		Data []struct {
			Item json.RawMessage `json:"item"`
		} `json:"data"`
		NextCursor *string `json:"nextCursor"`
	}
	if err := server.call(ctx, "thread/items/list", params, &response); err != nil {
		// A thread the server has never loaded has no items to list; that is
		// an empty page, not a failure the owner should see.
		return empty, nil
	}
	page := protocol.TranscriptPage{Entries: []protocol.TranscriptEntry{}, NextCursor: response.NextCursor}
	// The listing comes back newest first; the protocol's page is chronological.
	for i := len(response.Data) - 1; i >= 0; i-- {
		page.Entries = append(page.Entries, b.transcriptEntries(rec.ThreadID, response.Data[i].Item)...)
	}
	return page, nil
}

func (b *Backend) transcriptEntries(threadID string, raw json.RawMessage) []protocol.TranscriptEntry {
	parsed, ok := decodeItem(raw)
	if !ok {
		return nil
	}
	switch {
	case parsed.Type == "userMessage":
		return []protocol.TranscriptEntry{protocol.UserMessage(threadID, parsed.ID, parsed.userContent(), nil, nil)}
	case parsed.Type == "agentMessage":
		blocks := []protocol.ContentBlock{protocol.Text(parsed.textContent())}
		return []protocol.TranscriptEntry{protocol.AssistantMessage(threadID, parsed.ID, blocks, nil)}
	case parsed.Type == "reasoning":
		blocks := []protocol.ContentBlock{protocol.Thinking(parsed.textContent())}
		return []protocol.TranscriptEntry{protocol.AssistantMessage(threadID, parsed.ID, blocks, nil)}
	case parsed.Type == "contextCompaction":
		return []protocol.TranscriptEntry{protocol.ContextBoundary(threadID, protocol.CompactAuto, nil)}
	case parsed.isTool():
		// A finished tool call is two settled entries: the call, as an
		// assistant message, and its output.
		call := protocol.ToolUseBlock{
			Type: "tool_use", ID: parsed.ID, Name: parsed.toolName(),
			ToolKind: parsed.toolKind(), Input: parsed.toolInput(),
		}
		output, failed := parsed.toolOutput()
		return []protocol.TranscriptEntry{
			protocol.AssistantMessage(threadID, parsed.ID+"-call", []protocol.ContentBlock{call}, nil),
			protocol.ToolResult(threadID, parsed.ID, []protocol.ContentBlock{protocol.Text(output)}, failed),
		}
	}
	return nil
}

func (b *Backend) Discard(ctx context.Context, rec chat.ThreadRecord) error {
	b.mu.Lock()
	delete(b.threads, rec.ThreadID)
	delete(b.byNative, rec.NativeID)
	server := b.server
	b.mu.Unlock()
	if server != nil && rec.NativeID != "" {
		_ = server.call(ctx, "thread/delete", map[string]any{"threadId": rec.NativeID}, nil)
	}
	return nil
}

func (b *Backend) Close() {
	b.mu.Lock()
	server := b.server
	b.server = nil
	b.mu.Unlock()
	if server != nil {
		server.stop()
	}
}

// live returns the shared server and a thread's native id, or a nil server when
// the thread has no conversation open -- which is not an error for a command
// that simply has nothing to act on.
func (b *Backend) live(threadID string) (*client, string, error) {
	rec, ok := b.deps.Registry.Get(threadID)
	if !ok {
		return nil, "", protocol.Errorf(protocol.CodeThreadNotFound, "unknown thread %s", threadID)
	}
	b.mu.Lock()
	server, running := b.server, b.threads[threadID]
	b.mu.Unlock()
	if server == nil || running == nil || rec.NativeID == "" {
		return nil, "", nil
	}
	return server, rec.NativeID, nil
}

func strPtr(s string) *string { return &s }
