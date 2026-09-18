package pi

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
// before any command reaches this backend. The image limits are sized so that
// a prompt echoed back by pi, base64 and all, fits one stdout line.
var capabilities = protocol.AgentCapabilities{
	MaxImagesPerPrompt: 10,
	MaxImageBytes:      5 << 20,
	SupportsSteer:      true, // steer queues a message the running turn picks up
	ReportsCost:        true, // every assistant message carries usage.cost
	SupportsInterrupt:  true,
	// --system-prompt stands in for pi's own coding-assistant prompt;
	// --append-system-prompt adds to it.
	SystemPromptSupport: protocol.SystemPromptReplace,
}

// thinkingLevels is pi's one knob, named and valued as `pi --thinking` takes
// it. pi clamps a level the model cannot do, and the row follows what it kept.
var thinkingLevels = protocol.OptionGroup{ID: "thinking", Label: "thinking",
	Options: protocol.Choices("off", "minimal", "low", "medium", "high", "xhigh", "max")}

// runtimeTTL is how long a probe's answer is reused. The model list only
// changes when the owner signs in to another provider or edits models.json.
const runtimeTTL = 2 * time.Minute

type Options struct {
	// Bin is the pi executable; empty looks it up on PATH.
	Bin string
	// DefaultCwd is where a thread runs when the owner picked no directory.
	DefaultCwd string
	// IdleTimeout stops a subprocess that has had nothing to do; the next
	// prompt resumes the thread from its session file.
	IdleTimeout time.Duration
}

// Backend drives the `pi` CLI: one subprocess per live thread, reaped when
// idle, resumed transparently on the next prompt.
type Backend struct {
	chat.NoBackgroundTasks
	opts Options
	deps chat.Deps

	mu       sync.Mutex
	sessions map[string]*session

	runtimeMu   sync.Mutex
	runtime     protocol.AgentRuntimeInfo
	runtimeAt   time.Time
	runtimeOnce bool

	stop chan struct{}
}

func New(opts Options, deps chat.Deps) *Backend {
	if opts.Bin == "" {
		opts.Bin = "pi"
	}
	if opts.IdleTimeout == 0 {
		opts.IdleTimeout = 15 * time.Minute
	}
	b := &Backend{
		NoBackgroundTasks: chat.NoBackgroundTasks{AgentKind: protocol.KindPi},
		opts:              opts,
		deps:              deps,
		sessions:          map[string]*session{},
		stop:              make(chan struct{}),
	}
	go b.reapIdle()
	return b
}

func (b *Backend) Kind() protocol.AgentKind                 { return protocol.KindPi }
func (b *Backend) Label() string                            { return "Pi" }
func (b *Backend) Capabilities() protocol.AgentCapabilities { return capabilities }

// --- runtime probe ---

// RuntimeInfo starts a throwaway subprocess to ask pi for its models, its
// defaults and its commands. A failure is an UnavailableReason, never an
// error: one broken kind must not hide the others.
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
	info := protocol.AgentRuntimeInfo{
		DefaultCwd: b.opts.DefaultCwd,
		Defaults:   protocol.ThreadOptions{Settings: map[string]string{}},
		Models:     []protocol.ModelOption{},
		Groups:     []protocol.OptionGroup{thinkingLevels},
		Commands:   []protocol.SlashCommandInfo{},
	}
	if _, err := exec.LookPath(b.opts.Bin); err != nil {
		info.UnavailableReason = ptr("the `pi` CLI is not on PATH; install it with `npm i -g @earendil-works/pi-coding-agent` or set AGENT_WEB_PI_PATH")
		return info
	}
	cwd := b.opts.DefaultCwd
	if stat, err := os.Stat(cwd); err != nil || !stat.IsDir() {
		cwd = os.TempDir()
	}
	// --no-session keeps the probe out of the owner's session list.
	proc, err := start(launch{bin: b.opts.Bin, args: []string{"--mode", "rpc", "--no-session"}, cwd: cwd}, handlers{
		onEvent:     func(string, json.RawMessage) {},
		onUIRequest: func(json.RawMessage) {},
		onExit:      func(error) {},
	})
	if err != nil {
		info.UnavailableReason = ptr(err.Error())
		return info
	}
	defer proc.stop()

	probeCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()

	// The model list is already filtered to providers with credentials, so
	// an empty list is pi's way of saying nobody is signed in.
	raw, err := proc.request(probeCtx, map[string]any{"type": "get_available_models"}, startupTimeout)
	if err != nil {
		info.UnavailableReason = ptr("pi did not answer get_available_models: " + err.Error())
		return info
	}
	var catalog struct {
		Models []model `json:"models"`
	}
	if json.Unmarshal(raw, &catalog) != nil {
		info.UnavailableReason = ptr("pi returned a model list this build cannot read")
		return info
	}
	if len(catalog.Models) == 0 {
		info.UnavailableReason = ptr("no provider is signed in; run `pi` and use /login, or export a provider API key")
		return info
	}
	for _, m := range catalog.Models {
		option := protocol.ModelOption{ID: m.option(), Label: m.option()}
		if m.Name != "" && m.Name != m.ID {
			option.Description = ptr(m.Name)
		}
		info.Models = append(info.Models, option)
	}

	raw, err = proc.request(probeCtx, map[string]any{"type": "get_state"}, requestTimeout)
	if err != nil {
		info.UnavailableReason = ptr("pi did not answer get_state: " + err.Error())
		return info
	}
	var state sessionState
	if json.Unmarshal(raw, &state) != nil {
		info.UnavailableReason = ptr("pi returned a state this build cannot read")
		return info
	}
	if state.Model != nil {
		info.Defaults.Model = ptr(state.Model.option())
	}
	if state.ThinkingLevel != "" {
		info.Defaults.Settings["thinking"] = state.ThinkingLevel
	}

	raw, err = proc.request(probeCtx, map[string]any{"type": "get_commands"}, requestTimeout)
	if err != nil {
		info.UnavailableReason = ptr("pi did not answer get_commands: " + err.Error())
		return info
	}
	var commands struct {
		Commands []rpcCommand `json:"commands"`
	}
	if json.Unmarshal(raw, &commands) != nil {
		info.UnavailableReason = ptr("pi returned a command list this build cannot read")
		return info
	}
	for _, c := range commands.Commands {
		info.Commands = append(info.Commands, protocol.SlashCommandInfo{Name: c.Name, Description: c.Description})
	}
	return info
}

// launcher builds the command line for one thread. A new thread pins pi's
// session id to the thread's own uuid; a thread whose process was reaped
// reopens the session file pi named, which is how the conversation continues.
func (b *Backend) launcher(rec chat.ThreadRecord, resume bool) launch {
	args := []string{"--mode", "rpc"}
	if resume && rec.NativeID != "" {
		args = append(args, "--session", rec.NativeID)
	} else {
		args = append(args, "--session-id", localID(rec.ThreadID))
	}
	if rec.Options.Model != nil {
		args = append(args, "--model", *rec.Options.Model)
	}
	if level := rec.Options.Setting("thinking"); level != "" {
		args = append(args, "--thinking", level)
	}
	// pi's RPC has set_model and set_thinking_level and nothing for the system
	// prompt, so this is the only way in -- which is also why the thread keeps
	// the prompt it was created with across every relaunch.
	switch rec.SystemPrompt.Mode {
	case protocol.SystemPromptReplace:
		args = append(args, "--system-prompt", rec.SystemPrompt.Text)
	case protocol.SystemPromptAppend:
		args = append(args, "--append-system-prompt", rec.SystemPrompt.Text)
	}
	return launch{bin: b.opts.Bin, args: args, cwd: rec.Cwd}
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
	s := newSession(threadID, b.deps, b.launcher)
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

// Transcript reads the session file pi named on the first launch. A thread
// that never started has no file and no history.
func (b *Backend) Transcript(_ context.Context, rec chat.ThreadRecord, before string, limit int) (protocol.TranscriptPage, error) {
	if rec.NativeID == "" {
		return protocol.TranscriptPage{Entries: []protocol.TranscriptEntry{}}, nil
	}
	return readTranscript(rec.ThreadID, rec.NativeID, before, limit)
}

func (b *Backend) Prompt(ctx context.Context, rec chat.ThreadRecord, cmd protocol.ClientCommand) error {
	return b.orCreate(rec.ThreadID).prompt(ctx, rec, cmd)
}

func (b *Backend) Interrupt(ctx context.Context, threadID string) error {
	if s, ok := b.session(threadID); ok {
		return s.interrupt(ctx)
	}
	return nil
}

func (b *Backend) Steer(ctx context.Context, threadID, text string) error {
	s, ok := b.session(threadID)
	if !ok {
		return protocol.Errorf(protocol.CodeNoRunningTurn, "no turn is running")
	}
	return s.steer(ctx, text)
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

func (b *Backend) Discard(_ context.Context, rec chat.ThreadRecord) error {
	b.mu.Lock()
	s, ok := b.sessions[rec.ThreadID]
	delete(b.sessions, rec.ThreadID)
	b.mu.Unlock()
	if ok {
		s.close()
	}
	if rec.NativeID == "" {
		return nil
	}
	if err := os.Remove(rec.NativeID); err != nil && !os.IsNotExist(err) {
		return protocol.Errorf(protocol.CodeInternal, "cannot delete session file %s: %v", rec.NativeID, err)
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
// also the id pi is told to use for a new session.
func localID(threadID string) string {
	for i := 0; i < len(threadID); i++ {
		if threadID[i] == ':' {
			return threadID[i+1:]
		}
	}
	return threadID
}
