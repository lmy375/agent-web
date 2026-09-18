package claudecode

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

// capabilities is constant for the process lifetime; the service enforces it
// before any command reaches this backend.
var capabilities = protocol.AgentCapabilities{
	MaxImagesPerPrompt: 20,
	MaxImageBytes:      5 << 20,
	SupportsSteer:      false, // the CLI has no mid-turn steer
	ReportsCost:        true,  // result carries total_cost_usd
	SupportsInterrupt:  true,
	// --system-prompt replaces the CLI's own instructions outright;
	// --append-system-prompt adds to them.
	SystemPromptSupport: protocol.SystemPromptReplace,
}

// permissionModes is the one knob the CLI does not describe over the control
// protocol, so its choices are read off `claude --help` and kept in that
// order. The initialize response adds the rest.
var permissionModes = protocol.OptionGroup{ID: "permission-mode", Label: "permission-mode",
	Options: protocol.Choices("acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan")}

// runtimeTTL is how long a probe's answer is reused. The model and command
// lists only change when the owner edits settings or installs a plugin.
const runtimeTTL = 2 * time.Minute

type Options struct {
	// Bin is the claude executable; empty looks it up on PATH.
	Bin string
	// DefaultCwd is where a thread runs when the owner picked no directory.
	DefaultCwd string
	// OAuthToken, when set, is forwarded as CLAUDE_CODE_OAUTH_TOKEN.
	OAuthToken string
	// IdleTimeout stops a subprocess that has had nothing to do; the next
	// prompt resumes the thread from its transcript.
	IdleTimeout time.Duration
}

// Backend drives the `claude` CLI: one subprocess per live thread, reaped when
// idle, resumed transparently on the next prompt.
type Backend struct {
	chat.NoSteer
	opts Options
	deps chat.Deps

	mu       sync.Mutex
	sessions map[string]*session

	runtimeMu   sync.Mutex
	runtime     protocol.AgentRuntimeInfo
	resolved    []resolvedModel
	runtimeAt   time.Time
	runtimeOnce bool

	stop chan struct{}
}

func New(opts Options, deps chat.Deps) *Backend {
	if opts.Bin == "" {
		opts.Bin = "claude"
	}
	if opts.IdleTimeout == 0 {
		opts.IdleTimeout = 15 * time.Minute
	}
	b := &Backend{
		NoSteer:  chat.NoSteer{AgentKind: protocol.KindClaudeCode},
		opts:     opts,
		deps:     deps,
		sessions: map[string]*session{},
		stop:     make(chan struct{}),
	}
	go b.reapIdle()
	return b
}

func (b *Backend) Kind() protocol.AgentKind                 { return protocol.KindClaudeCode }
func (b *Backend) Label() string                            { return "Claude Code" }
func (b *Backend) Capabilities() protocol.AgentCapabilities { return capabilities }

// --- runtime probe ---

// RuntimeInfo starts a throwaway subprocess just to run initialize, which is
// the only way to ask the CLI for its model and command lists. A failure is an
// UnavailableReason, never an error: one broken kind must not hide the others.
func (b *Backend) RuntimeInfo(ctx context.Context) protocol.AgentRuntimeInfo {
	b.runtimeMu.Lock()
	defer b.runtimeMu.Unlock()
	if b.runtimeOnce && time.Since(b.runtimeAt) < runtimeTTL && b.runtime.UnavailableReason == nil {
		return b.runtime
	}
	b.runtime = b.probe(ctx)
	b.runtimeAt, b.runtimeOnce = time.Now(), true
	return b.runtime
}

func (b *Backend) probe(ctx context.Context) protocol.AgentRuntimeInfo {
	defaults := protocol.ThreadOptions{
		Model:    ptr("default"),
		Settings: map[string]string{"permission-mode": "manual", "effort": "high"},
	}
	info := protocol.AgentRuntimeInfo{
		DefaultCwd: b.opts.DefaultCwd,
		Defaults:   defaults,
		Models:     []protocol.ModelOption{},
		Groups:     []protocol.OptionGroup{permissionModes},
		Commands:   []protocol.SlashCommandInfo{},
	}
	if _, err := exec.LookPath(b.opts.Bin); err != nil {
		info.UnavailableReason = ptr("the `claude` CLI is not on PATH; install Claude Code or set AGENT_WEB_CLAUDE_PATH")
		return info
	}

	cwd := b.opts.DefaultCwd
	if stat, err := os.Stat(cwd); err != nil || !stat.IsDir() {
		cwd = os.TempDir()
	}
	// --no-session-persistence keeps the probe out of the owner's session list.
	spec := launch{
		bin: b.opts.Bin, cwd: cwd, env: b.env(),
		args: []string{
			"--print", "--verbose", "--output-format", "stream-json",
			"--input-format", "stream-json", "--no-session-persistence",
		},
	}
	proc, err := start(spec, handlers{
		onMessage:        func(string, json.RawMessage) {},
		onControlRequest: func(string, string, json.RawMessage) {},
		onExit:           func(error) {},
	})
	if err != nil {
		info.UnavailableReason = ptr(err.Error())
		return info
	}
	defer proc.stop()

	probeCtx, cancel := context.WithTimeout(ctx, initializeTimeout)
	defer cancel()
	response, err := proc.control(probeCtx, map[string]any{"subtype": "initialize", "hooks": nil}, initializeTimeout)
	if err != nil {
		info.UnavailableReason = ptr("claude did not answer initialize: " + err.Error())
		return info
	}
	var server serverInfo
	if json.Unmarshal(response, &server) != nil {
		info.UnavailableReason = ptr("claude returned an initialize response this build cannot read")
		return info
	}
	if server.Account == nil {
		info.UnavailableReason = ptr("not signed in; run `claude login` or set AGENT_WEB_CLAUDE_OAUTH_TOKEN")
		return info
	}
	info.Models = server.modelOptions()
	// RuntimeInfo holds runtimeMu across this call, so the resolved models and
	// the list they belong to are replaced together.
	b.resolved = server.resolvedModels()
	info.Groups = server.optionGroups()
	info.Commands = server.slashCommands()
	if server.CurrentPermissionMode != "" {
		info.Defaults.Settings["permission-mode"] = modeAsFlag(server.CurrentPermissionMode)
	}
	return info
}

func (b *Backend) env() []string {
	if b.opts.OAuthToken == "" {
		return nil
	}
	return []string{"CLAUDE_CODE_OAUTH_TOKEN=" + b.opts.OAuthToken}
}

// launcher builds the command line for one thread. Resuming is how a thread
// whose process was reaped picks its conversation back up.
func (b *Backend) launcher(rec chat.ThreadRecord, resume bool) launch {
	args := []string{
		"--print", "--verbose",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--include-partial-messages",
		// Route permission prompts over the control protocol instead of a tool.
		"--permission-prompt-tool", "stdio",
		// Ask for summarized thinking: newer models otherwise stream empty
		// thinking blocks and the UI has nothing to show.
		"--thinking-display", "summarized",
	}
	if mode := rec.Options.Setting("permission-mode"); mode != "" {
		args = append(args, "--permission-mode", mode)
	}
	if rec.Options.Model != nil {
		args = append(args, "--model", *rec.Options.Model)
	}
	if effort := rec.Options.Setting("effort"); effort != "" {
		args = append(args, "--effort", effort)
	}
	// The thread's prompt never changes, so --system-prompt-snapshot is left
	// at its default: what the CLI records on the first request and replays on
	// every resume is the same text this flag carries anyway.
	switch rec.SystemPrompt.Mode {
	case protocol.SystemPromptReplace:
		args = append(args, "--system-prompt", rec.SystemPrompt.Text)
	case protocol.SystemPromptAppend:
		args = append(args, "--append-system-prompt", rec.SystemPrompt.Text)
	}
	if resume && rec.NativeID != "" {
		args = append(args, "--resume="+rec.NativeID)
	} else {
		args = append(args, "--session-id="+localID(rec.ThreadID))
	}
	return launch{bin: b.opts.Bin, args: args, cwd: rec.Cwd, env: b.env()}
}

// selectedModel names, in the vocabulary the model picker uses, the model an
// init message reported by its resolved id. A selection that already resolves
// to that id stands: `default` and `opus[1m]` are the same model, and the id
// alone cannot tell them apart. Any other id means the CLI came up with a
// different model than the row asked for, so the row follows it. An id from no
// known model leaves the row's selection alone.
func (b *Backend) selectedModel(reported, current string) string {
	b.runtimeMu.Lock()
	defer b.runtimeMu.Unlock()
	for _, m := range b.resolved {
		if m.value == current && m.model == reported {
			return current
		}
	}
	for _, m := range b.resolved {
		if m.model == reported {
			return m.value
		}
	}
	return current
}

// --- threads ---

func (b *Backend) session(threadID string) (*session, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.sessions[threadID]
	return s, ok
}

func (b *Backend) orCreate(threadID string) *session {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.sessions[threadID]; ok {
		return s
	}
	s := newSession(threadID, b.deps, b.launcher, b.selectedModel)
	b.sessions[threadID] = s
	return s
}

func (b *Backend) RunState(threadID string) protocol.ThreadRunState {
	if s, ok := b.session(threadID); ok {
		return s.runState()
	}
	return protocol.StateIdle
}

func (b *Backend) LiveState(threadID string) chat.LiveState {
	if s, ok := b.session(threadID); ok {
		return s.liveState()
	}
	return chat.LiveState{}
}

func (b *Backend) Transcript(_ context.Context, rec chat.ThreadRecord, before string, limit int) (protocol.TranscriptPage, error) {
	sessionID := rec.NativeID
	if sessionID == "" {
		sessionID = localID(rec.ThreadID)
	}
	return readTranscript(rec.ThreadID, rec.Cwd, sessionID, before, limit)
}

func (b *Backend) Prompt(ctx context.Context, rec chat.ThreadRecord, cmd protocol.ClientCommand) error {
	// NativeID is set by the init message of the first launch, and it is what
	// makes every later launch a --resume. Pinning it before the CLI has
	// actually created the conversation would make the first launch try to
	// resume a session that does not exist.
	return b.orCreate(rec.ThreadID).prompt(ctx, rec, cmd)
}

func (b *Backend) Interrupt(ctx context.Context, threadID string) error {
	if s, ok := b.session(threadID); ok {
		return s.interrupt(ctx)
	}
	return nil
}

func (b *Backend) SetOptions(ctx context.Context, rec chat.ThreadRecord) error {
	if s, ok := b.session(rec.ThreadID); ok {
		return s.applyOptions(ctx, rec)
	}
	return nil // A cold thread only needed the record, which the service stored.
}

func (b *Backend) Respond(_ context.Context, threadID, requestID string, d protocol.InteractionDecision) error {
	s, ok := b.session(threadID)
	if !ok {
		return protocol.Errorf(protocol.CodeInteractionNotPending, "no pending interaction %s", requestID)
	}
	return s.respond(requestID, d)
}

func (b *Backend) StopTask(ctx context.Context, threadID, taskID string) error {
	s, ok := b.session(threadID)
	if !ok {
		return protocol.Errorf(protocol.CodeTaskNotFound, "no background task %s", taskID)
	}
	return s.stopTask(ctx, taskID)
}

func (b *Backend) Discard(_ context.Context, rec chat.ThreadRecord) error {
	b.mu.Lock()
	s, ok := b.sessions[rec.ThreadID]
	delete(b.sessions, rec.ThreadID)
	b.mu.Unlock()
	if ok {
		s.close()
	}
	sessionID := rec.NativeID
	if sessionID == "" {
		sessionID = localID(rec.ThreadID)
	}
	if path := transcriptPath(rec.Cwd, sessionID); path != "" {
		_ = os.Remove(path)
	}
	return nil
}

func (b *Backend) Close() {
	close(b.stop)
	b.mu.Lock()
	sessions := make([]*session, 0, len(b.sessions))
	for _, s := range b.sessions {
		sessions = append(sessions, s)
	}
	b.sessions = map[string]*session{}
	b.mu.Unlock()
	for _, s := range sessions {
		s.close()
	}
}

// reapIdle stops subprocesses nobody is using. The thread stays in the
// directory and its next prompt resumes it, so this is invisible to the owner.
func (b *Backend) reapIdle() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-b.stop:
			return
		case <-ticker.C:
			b.mu.Lock()
			candidates := make([]*session, 0, len(b.sessions))
			for _, s := range b.sessions {
				candidates = append(candidates, s)
			}
			b.mu.Unlock()
			for _, s := range candidates {
				if idle, ok := s.idleSince(); ok && idle > b.opts.IdleTimeout {
					s.stopProcess()
				}
			}
		}
	}
}

// localID is the thread id without its kind prefix, which for this backend is
// also the uuid the CLI is told to use as its session id.
func localID(threadID string) string {
	for i := 0; i < len(threadID); i++ {
		if threadID[i] == ':' {
			return threadID[i+1:]
		}
	}
	return threadID
}

func ptr[T any](v T) *T { return &v }
