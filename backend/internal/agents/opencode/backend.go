package opencode

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lmy375/agent-web/backend/internal/chat"
	"github.com/lmy375/agent-web/backend/internal/protocol"
)

var capabilities = protocol.AgentCapabilities{
	MaxImagesPerPrompt: 10,
	MaxImageBytes:      5 << 20,
	SupportsSteer:      false,
	ReportsCost:        true,
	SupportsInterrupt:  true,
}

type Options struct {
	Bin         string
	DefaultCwd  string
	IdleTimeout time.Duration
}

type thread struct {
	mu          sync.Mutex
	state       protocol.ThreadRunState
	turn        string
	interrupted bool
	// turnStarted is when this turn was claimed; zero means none is running.
	turnStarted time.Time
	// turnUsage is each assistant message's accounting keyed by message id.
	// OpenCode reports a message repeatedly as it settles, and the prompt call
	// returns the same one again, so an id is overwritten rather than added.
	turnUsage    map[string]protocol.Usage
	pending      map[string]protocol.InteractionRequest
	contextUsage *protocol.ContextUsage
	lastTurn     *protocol.TurnSummary
	// parts remembers each streamed part's type, so a delta can be routed
	// without re-reading the message.
	parts map[string]string
	// assembling collects a message's settled parts in arrival order, because
	// OpenCode announces a finished message without repeating its content and
	// the protocol's assistant_message has to carry the final blocks.
	assembling map[string][]part
	// blocks numbers each message's parts in the order they are first seen.
	// OpenCode numbers nothing itself, and two blocks claiming one index make
	// the client drop whichever arrives second.
	blocks map[string]map[string]int
	tools  map[string]bool
}

func newThread() *thread {
	return &thread{
		state: protocol.StateIdle, pending: map[string]protocol.InteractionRequest{},
		turnUsage: map[string]protocol.Usage{},
		parts:     map[string]string{}, assembling: map[string][]part{},
		blocks: map[string]map[string]int{}, tools: map[string]bool{},
	}
}

// Backend hosts one `opencode serve` process for every thread; OpenCode scopes
// a call to a project with a ?directory= parameter, so one server covers every
// working directory the owner picks.
type Backend struct {
	chat.NoSteer
	chat.NoBackgroundTasks
	opts Options
	deps chat.Deps

	mu        sync.Mutex
	server    *serverProcess
	threads   map[string]*thread
	bySession map[string]string // opencode sessionID -> our thread id

	starting  sync.Mutex
	runtimeMu sync.Mutex
	runtime   protocol.AgentRuntimeInfo
	runtimeAt time.Time
}

func New(opts Options, deps chat.Deps) *Backend {
	if opts.Bin == "" {
		opts.Bin = "opencode"
	}
	return &Backend{
		NoSteer:           chat.NoSteer{AgentKind: protocol.KindOpenCode},
		NoBackgroundTasks: chat.NoBackgroundTasks{AgentKind: protocol.KindOpenCode},
		opts:              opts, deps: deps,
		threads: map[string]*thread{}, bySession: map[string]string{},
	}
}

func (b *Backend) Kind() protocol.AgentKind                 { return protocol.KindOpenCode }
func (b *Backend) Label() string                            { return "OpenCode" }
func (b *Backend) Capabilities() protocol.AgentCapabilities { return capabilities }

// --- the shared server ---

func (b *Backend) ensureServer(ctx context.Context) (*serverProcess, error) {
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
	server, err := spawn(ctx, b.opts.Bin, cwd)
	if err != nil {
		return nil, protocol.Errorf(protocol.CodeAgentUnavailable, "%v", err)
	}
	b.mu.Lock()
	b.server = server
	b.mu.Unlock()
	go server.subscribe(b.onEvent)
	return server, nil
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
	info := protocol.AgentRuntimeInfo{
		DefaultCwd: b.opts.DefaultCwd,
		Defaults:   protocol.ThreadOptions{Settings: map[string]string{"agent": defaultAgent}},
		Models:     []protocol.ModelOption{},
		Groups:     []protocol.OptionGroup{},
		Commands:   []protocol.SlashCommandInfo{},
	}
	if err := reachable(b.opts.Bin); err != nil {
		info.UnavailableReason = strPtr(err.Error())
		return info
	}
	server, err := b.ensureServer(ctx)
	if err != nil {
		info.UnavailableReason = strPtr(err.Error())
		return info
	}

	var providers struct {
		Providers []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Models map[string]struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"models"`
		} `json:"providers"`
		Default map[string]string `json:"default"`
	}
	if err := server.request(ctx, "GET", "/config/providers", "", nil, &providers); err != nil {
		info.UnavailableReason = strPtr("opencode could not list providers: " + err.Error())
		return info
	}
	for _, provider := range providers.Providers {
		for _, model := range provider.Models {
			label := provider.Name + " / " + model.Name
			info.Models = append(info.Models, protocol.ModelOption{ID: provider.ID + "/" + model.ID, Label: label})
		}
	}
	if len(info.Models) == 0 {
		info.UnavailableReason = strPtr("no OpenCode provider is configured; run `opencode auth login`")
		return info
	}
	sortModels(info.Models)
	// The owner's configured default for the first provider, when there is one.
	for providerID, modelID := range providers.Default {
		info.Defaults.Model = strPtr(providerID + "/" + modelID)
		break
	}
	if info.Defaults.Model == nil {
		info.Defaults.Model = &info.Models[0].ID
	}
	info.Commands = b.commands(ctx, server)
	// OpenCode has no permission mode and no effort: what it has is agents,
	// and which ones exist is the owner's config, so they are probed rather
	// than assumed.
	if agents := b.agents(ctx, server); len(agents) > 0 {
		info.Groups = append(info.Groups, protocol.OptionGroup{ID: "agent", Label: "agent", Options: agents})
	}
	return info
}

// agents lists the ones the owner can prompt with: OpenCode marks its internal
// ones hidden and its delegates as subagents, and neither belongs in a menu.
func (b *Backend) agents(ctx context.Context, server *serverProcess) []protocol.OptionChoice {
	var list []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Mode        string `json:"mode"`
		Hidden      bool   `json:"hidden"`
	}
	if err := server.request(ctx, "GET", "/agent", "", nil, &list); err != nil {
		return nil
	}
	out := make([]protocol.OptionChoice, 0, len(list))
	for _, agent := range list {
		if agent.Mode != "primary" || agent.Hidden {
			continue
		}
		choice := protocol.OptionChoice{Value: agent.Name, Label: agent.Name}
		if agent.Description != "" {
			choice.Description = &agent.Description
		}
		out = append(out, choice)
	}
	return out
}

func (b *Backend) commands(ctx context.Context, server *serverProcess) []protocol.SlashCommandInfo {
	var list []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Agent       string `json:"agent"`
	}
	if err := server.request(ctx, "GET", "/command", "", nil, &list); err != nil {
		return []protocol.SlashCommandInfo{}
	}
	out := make([]protocol.SlashCommandInfo, 0, len(list))
	for _, c := range list {
		out = append(out, protocol.SlashCommandInfo{Name: c.Name, Description: c.Description})
	}
	return out
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
	for _, r := range t.pending {
		pending = append(pending, r)
	}
	var current *protocol.RunningTurn
	if !t.turnStarted.IsZero() {
		spent := t.spent()
		current = &protocol.RunningTurn{ClientMessageID: t.turn, StartedAt: t.turnStarted, Usage: &spent}
	}
	return chat.LiveState{
		Pending: pending, ContextUsage: t.contextUsage,
		CurrentTurn: current, LastTurn: t.lastTurn,
	}
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
	sessionID, err := b.openSession(ctx, server, rec)
	if err != nil {
		return err
	}

	body := map[string]any{"parts": promptParts(cmd), "agent": agentFor(rec.Options)}
	if rec.Options.Model != nil {
		provider, model, ok := splitModelID(*rec.Options.Model)
		if !ok {
			return protocol.Errorf(protocol.CodeOptionInvalid, "model %q is not <provider>/<model>", *rec.Options.Model)
		}
		body["model"] = map[string]string{"providerID": provider, "modelID": model}
	}

	t, _ := b.thread(rec.ThreadID)
	t.mu.Lock()
	t.turn, t.interrupted = cmd.ClientMessageID, false
	t.turnStarted = time.Now().UTC()
	t.turnUsage = map[string]protocol.Usage{}
	started := t.turnStarted
	t.mu.Unlock()

	b.deps.Publish(protocol.TurnStarted(rec.ThreadID, cmd.ClientMessageID, started))
	id := cmd.ClientMessageID
	b.deps.Publish(protocol.UserMessage(rec.ThreadID, "prompt-"+id, echoBlocks(cmd), &id, nil))
	b.setState(rec.ThreadID, protocol.StateRunning)

	// The prompt call only returns when the whole turn is done, so it runs on
	// its own goroutine and everything the owner sees arrives on the stream.
	go b.runTurn(server, rec, sessionID, body)
	return nil
}

// runTurn owns one request to /message, which OpenCode answers only when the
// turn is over. Its body is the settled assistant message; the increments have
// already gone out over the event stream.
func (b *Backend) runTurn(server *serverProcess, rec chat.ThreadRecord, sessionID string, body map[string]any) {
	var response struct {
		Info  message `json:"info"`
		Parts []part  `json:"parts"`
	}
	// No deadline: a turn can legitimately run for many minutes.
	err := server.request(context.Background(), "POST", "/session/"+sessionID+"/message", rec.Cwd, body, &response)

	t, ok := b.thread(rec.ThreadID)
	if !ok {
		return
	}
	t.mu.Lock()
	clientMessageID, interrupted := t.turn, t.interrupted
	t.turn, t.interrupted = "", false
	t.mu.Unlock()

	status := protocol.TurnCompleted
	switch {
	case interrupted:
		status = protocol.TurnInterrupted
	case err != nil:
		status = protocol.TurnFailed
		b.deps.Publish(protocol.StreamError(rec.ThreadID, protocol.ErrOther, err.Error(), false))
	case len(response.Info.Error) > 0:
		if code, text := sessionError(response.Info.Error); code != "" {
			status = protocol.TurnFailed
			b.deps.Publish(protocol.StreamError(rec.ThreadID, code, text, false))
		} else {
			status = protocol.TurnInterrupted
		}
	}

	summary := protocol.TurnSummary{Status: status}
	if response.Info.Cost > 0 {
		cost := response.Info.Cost
		summary.CostUSD = &cost
	}
	t.mu.Lock()
	// The settled message also arrives over the event stream, so it is folded
	// in by id: whichever of the two is second changes nothing.
	if spent := response.Info.usage(); spent != nil {
		t.turnUsage[response.Info.ID] = *spent
	}
	total := t.spent()
	summary.Usage, summary.StartedAt, summary.FinishedAt = &total, t.turnStarted, time.Now().UTC()
	t.lastTurn = &summary
	t.turnStarted = time.Time{}
	t.mu.Unlock()

	b.deps.Publish(protocol.TurnFinished(rec.ThreadID, clientMessageID, summary))
	b.setState(rec.ThreadID, protocol.StateIdle)
	b.deps.Registry.Touch(rec.ThreadID)
}

// openSession makes sure OpenCode has a session for this thread, creating one
// the first time. A session survives a server restart, so a stored id is
// simply used again.
func (b *Backend) openSession(ctx context.Context, server *serverProcess, rec chat.ThreadRecord) (string, error) {
	b.mu.Lock()
	_, live := b.threads[rec.ThreadID]
	b.mu.Unlock()
	if live && rec.NativeID != "" {
		return rec.NativeID, nil
	}

	sessionID := rec.NativeID
	if sessionID == "" {
		var created struct {
			ID string `json:"id"`
		}
		if err := server.request(ctx, "POST", "/session", rec.Cwd, map[string]any{}, &created); err != nil {
			return "", protocol.Errorf(protocol.CodeAgentUnavailable, "cannot create an opencode session: %v", err)
		}
		sessionID = created.ID
		b.deps.Registry.Update(rec.ThreadID, func(r *chat.ThreadRecord) { r.NativeID = sessionID })
	}

	b.mu.Lock()
	b.threads[rec.ThreadID] = newThread()
	b.bySession[sessionID] = rec.ThreadID
	b.mu.Unlock()
	return sessionID, nil
}

func promptParts(cmd protocol.ClientCommand) []any {
	parts := make([]any, 0, len(cmd.Images)+1)
	for i, image := range cmd.Images {
		parts = append(parts, map[string]any{
			"type": "file", "mime": image.MediaType,
			"filename": fmt.Sprintf("image-%d", i+1),
			"url":      "data:" + image.MediaType + ";base64," + image.DataBase64,
		})
	}
	if cmd.Text != "" {
		parts = append(parts, map[string]any{"type": "text", "text": cmd.Text})
	}
	return parts
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

func (b *Backend) Interrupt(ctx context.Context, threadID string) error {
	server, sessionID, cwd, ok := b.live(threadID)
	if !ok {
		return nil
	}
	t, _ := b.thread(threadID)
	t.mu.Lock()
	running := t.turn != ""
	if running {
		t.interrupted = true
	}
	t.mu.Unlock()
	if !running {
		return nil
	}
	if err := server.request(ctx, "POST", "/session/"+sessionID+"/abort", cwd, map[string]any{}, nil); err != nil {
		return protocol.Errorf(protocol.CodeAgentUnavailable, "abort failed: %v", err)
	}
	return nil
}

// SetOptions has nothing to push: the agent and the model are arguments of the
// next prompt, so the stored record already is the change.
func (b *Backend) SetOptions(context.Context, chat.ThreadRecord) error { return nil }

func (b *Backend) Respond(ctx context.Context, threadID, requestID string, decision protocol.InteractionDecision) error {
	t, ok := b.thread(threadID)
	if !ok {
		return protocol.Errorf(protocol.CodeInteractionNotPending, "no pending interaction %s", requestID)
	}
	t.mu.Lock()
	request, found := t.pending[requestID]
	delete(t.pending, requestID)
	running := t.turn != ""
	t.mu.Unlock()
	if !found {
		return protocol.Errorf(protocol.CodeInteractionNotPending, "no pending interaction %s", requestID)
	}
	server, _, cwd, ok := b.live(threadID)
	if !ok {
		return protocol.Errorf(protocol.CodeAgentUnavailable, "the opencode server is gone")
	}

	var err error
	if request.Payload.Kind() == protocol.InteractionQuestion {
		err = b.answerQuestion(ctx, server, cwd, request, decision)
	} else {
		err = server.request(ctx, "POST", "/permission/"+nativeID(requestID)+"/reply", cwd,
			map[string]any{"reply": permissionReply(decision)}, nil)
	}
	if err != nil {
		return protocol.Errorf(protocol.CodeAgentUnavailable, "%v", err)
	}
	b.deps.Publish(protocol.InteractionResolved(threadID, requestID, &decision))
	if running {
		b.setState(threadID, protocol.StateRunning)
	}
	return nil
}

func (b *Backend) answerQuestion(ctx context.Context, server *serverProcess, cwd string,
	request protocol.InteractionRequest, decision protocol.InteractionDecision) error {
	if decision.Type == protocol.DecisionDeny {
		return server.request(ctx, "POST", "/question/"+nativeID(request.RequestID)+"/reject", cwd, map[string]any{}, nil)
	}
	// OpenCode takes the answers positionally, in the order it asked.
	payload, _ := request.Payload.(protocol.QuestionPayload)
	byID := map[string][]string{}
	for _, a := range decision.Answers {
		byID[a.QuestionID] = a.Answers
	}
	answers := make([][]string, 0, len(payload.Questions))
	for _, q := range payload.Questions {
		chosen := byID[q.ID]
		if chosen == nil {
			chosen = []string{}
		}
		answers = append(answers, chosen)
	}
	return server.request(ctx, "POST", "/question/"+nativeID(request.RequestID)+"/reply", cwd,
		map[string]any{"answers": answers}, nil)
}

func permissionReply(decision protocol.InteractionDecision) string {
	switch {
	case decision.Type != protocol.DecisionAllow:
		return "reject"
	case decision.Remember != nil:
		return "always"
	default:
		return "once"
	}
}

func (b *Backend) Transcript(ctx context.Context, rec chat.ThreadRecord, before string, limit int) (protocol.TranscriptPage, error) {
	page := protocol.TranscriptPage{Entries: []protocol.TranscriptEntry{}}
	if rec.NativeID == "" {
		return page, nil
	}
	server, err := b.ensureServer(ctx)
	if err != nil {
		return page, err
	}
	path := fmt.Sprintf("/session/%s/message", rec.NativeID)
	var messages []struct {
		Info  message `json:"info"`
		Parts []part  `json:"parts"`
	}
	if err := server.request(ctx, "GET", path, rec.Cwd, nil, &messages); err != nil {
		return page, nil // A session the server has not loaded simply has no page.
	}
	for _, m := range messages {
		if m.Info.Role == "user" {
			if blocks := userBlocks(m.Parts); len(blocks) > 0 {
				page.Entries = append(page.Entries, protocol.UserMessage(rec.ThreadID, m.Info.ID, blocks, nil, nil))
			}
			continue
		}
		if blocks := contentBlocks(m.Parts); len(blocks) > 0 {
			page.Entries = append(page.Entries, protocol.AssistantMessage(rec.ThreadID, m.Info.ID, blocks, nil))
		}
		for _, p := range m.Parts {
			if p.Type == "tool" && p.State != nil && p.State.Status != "running" {
				output, failed := toolResult(p)
				page.Entries = append(page.Entries,
					protocol.ToolResult(rec.ThreadID, p.CallID, []protocol.ContentBlock{protocol.Text(output)}, failed))
			}
		}
	}
	return page, nil
}

func (b *Backend) Discard(ctx context.Context, rec chat.ThreadRecord) error {
	b.mu.Lock()
	delete(b.threads, rec.ThreadID)
	delete(b.bySession, rec.NativeID)
	server := b.server
	b.mu.Unlock()
	if server != nil && rec.NativeID != "" {
		_ = server.request(ctx, "DELETE", "/session/"+rec.NativeID, rec.Cwd, nil, nil)
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

func (b *Backend) live(threadID string) (*serverProcess, string, string, bool) {
	rec, ok := b.deps.Registry.Get(threadID)
	if !ok || rec.NativeID == "" {
		return nil, "", "", false
	}
	b.mu.Lock()
	server, running := b.server, b.threads[threadID]
	b.mu.Unlock()
	if server == nil || running == nil {
		return nil, "", "", false
	}
	return server, rec.NativeID, rec.Cwd, true
}

// nativeID strips the prefix this backend adds so an interaction id is unique
// across kinds while the harness still sees its own.
func nativeID(requestID string) string {
	_, native, _ := strings.Cut(requestID, ":")
	return native
}

func strPtr(s string) *string { return &s }

func sortModels(models []protocol.ModelOption) {
	// Provider-then-model order, so the picker is stable across restarts.
	for i := 1; i < len(models); i++ {
		for j := i; j > 0 && models[j].ID < models[j-1].ID; j-- {
			models[j], models[j-1] = models[j-1], models[j]
		}
	}
}
