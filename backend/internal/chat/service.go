package chat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lmy375/agent-web/backend/internal/fsx"
	"github.com/lmy375/agent-web/backend/internal/protocol"
)

// rememberedPrompts is how many client_message_ids the replay check keeps.
// Recognising a replay from process memory only is deliberate: this is one
// process, and a restart forgetting an id costs one duplicated turn, never a
// lost one.
const rememberedPrompts = 4096

// promptLedger records which client_message_id carried which payload.
type promptLedger struct {
	mu      sync.Mutex
	order   []string
	digests map[string]string
}

func newPromptLedger() *promptLedger { return &promptLedger{digests: map[string]string{}} }

// seen reports whether this is the same prompt again, and fails when the same
// id arrives carrying a different one.
func (l *promptLedger) seen(cmd protocol.ClientCommand) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	digest := cmd.PayloadDigest()
	remembered, ok := l.digests[cmd.ClientMessageID]
	if !ok {
		return false, nil
	}
	if remembered != digest {
		return false, protocol.Errorf(protocol.CodeClientMessageConflict,
			"%s already carried a different prompt", cmd.ClientMessageID)
	}
	return true, nil
}

func (l *promptLedger) record(cmd protocol.ClientCommand) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.digests[cmd.ClientMessageID]; ok {
		return
	}
	if len(l.order) >= rememberedPrompts {
		delete(l.digests, l.order[0])
		l.order = l.order[1:]
	}
	l.order = append(l.order, cmd.ClientMessageID)
	l.digests[cmd.ClientMessageID] = cmd.PayloadDigest()
}

// Service dispatches by agent kind and is the single place every kind-neutral
// policy lives, so a backend only ever sees input that is already legal.
type Service struct {
	backends map[protocol.AgentKind]Backend
	registry *Registry
	hub      *Hub
	prompts  *promptLedger
}

func NewService(registry *Registry, hub *Hub, backends ...Backend) *Service {
	s := &Service{
		backends: map[protocol.AgentKind]Backend{},
		registry: registry,
		hub:      hub,
		prompts:  newPromptLedger(),
	}
	for _, b := range backends {
		s.backends[b.Kind()] = b
	}
	registry.Wire(s.runState, hub.Publish)
	return s
}

func (s *Service) Hub() *Hub { return s.hub }

func (s *Service) Close() {
	for _, b := range s.backends {
		b.Close()
	}
}

// runState asks the owning backend; a thread whose kind is gone is idle.
func (s *Service) runState(threadID string) protocol.ThreadRunState {
	rec, ok := s.registry.Get(threadID)
	if !ok {
		return protocol.StateIdle
	}
	backend, ok := s.backends[rec.AgentKind]
	if !ok {
		return protocol.StateIdle
	}
	return backend.RunState(threadID)
}

func (s *Service) backendFor(threadID string) (Backend, ThreadRecord, error) {
	rec, err := s.registry.Require(threadID)
	if err != nil {
		return nil, ThreadRecord{}, err
	}
	backend, ok := s.backends[rec.AgentKind]
	if !ok {
		return nil, ThreadRecord{}, protocol.Errorf(protocol.CodeAgentUnavailable,
			"no backend for agent kind %s", rec.AgentKind)
	}
	return backend, rec, nil
}

// DescribeAgents probes every kind at once; a probe that fails becomes an
// UnavailableReason inside its own descriptor.
func (s *Service) DescribeAgents(ctx context.Context) []protocol.AgentDescriptor {
	out := make([]protocol.AgentDescriptor, len(s.backends))
	kinds := s.sortedBackends()
	var wg sync.WaitGroup
	for i, b := range kinds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = protocol.AgentDescriptor{
				Kind:         b.Kind(),
				Label:        b.Label(),
				Capabilities: b.Capabilities(),
				Runtime:      b.RuntimeInfo(ctx),
			}
		}()
	}
	wg.Wait()
	return out
}

// sortedBackends gives the descriptor list a stable order across restarts.
func (s *Service) sortedBackends() []Backend {
	out := make([]Backend, 0, len(s.backends))
	for _, b := range s.backends {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind() < out[j].Kind() })
	return out
}

func (s *Service) ListThreads(cursor string, limit int) (protocol.ThreadList, error) {
	var keyset *protocol.ThreadKeyset
	if cursor != "" {
		parsed, err := protocol.ParseKeyset(cursor)
		if err != nil {
			return protocol.ThreadList{}, err
		}
		keyset = &parsed
	}
	threads, hasMore := s.registry.Page(keyset, limit)
	list := protocol.ThreadList{Threads: threads}
	if hasMore && len(threads) > 0 {
		next := threads[len(threads)-1].Keyset().Encode()
		list.NextCursor = &next
	}
	return list, nil
}

func (s *Service) CreateThread(ctx context.Context, req protocol.CreateThreadRequest) (protocol.ThreadSummary, error) {
	backend, ok := s.backends[req.AgentKind]
	if !ok {
		return protocol.ThreadSummary{}, protocol.Errorf(protocol.CodeAgentUnavailable,
			"no backend for agent kind %s", req.AgentKind)
	}
	if err := backend.Capabilities().CheckOptions(req.Options); err != nil {
		return protocol.ThreadSummary{}, err
	}
	runtime := backend.RuntimeInfo(ctx)
	if runtime.UnavailableReason != nil {
		return protocol.ThreadSummary{}, protocol.Errorf(protocol.CodeAgentUnavailable, "%s", *runtime.UnavailableReason)
	}
	cwd := runtime.DefaultCwd
	if req.Cwd != "" {
		validated, err := ValidateCwd(req.Cwd)
		if err != nil {
			return protocol.ThreadSummary{}, err
		}
		cwd = validated
	}
	rec := ThreadRecord{
		ThreadID:  string(req.AgentKind) + ":" + newID(),
		AgentKind: req.AgentKind,
		Cwd:       cwd,
		Options:   runtime.Defaults.Merge(req.Options),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := s.registry.Put(rec); err != nil {
		return protocol.ThreadSummary{}, err
	}
	return s.registry.Summary(rec), nil
}

func (s *Service) GetThread(threadID string) (protocol.ThreadDetail, error) {
	backend, rec, err := s.backendFor(threadID)
	if err != nil {
		return protocol.ThreadDetail{}, err
	}
	live := backend.LiveState(threadID)
	if live.Pending == nil {
		live.Pending = []protocol.InteractionRequest{}
	}
	return protocol.ThreadDetail{
		Summary:      s.registry.Summary(rec),
		Pending:      live.Pending,
		ContextUsage: live.ContextUsage,
		LastTurn:     live.LastTurn,
	}, nil
}

func (s *Service) UpdateThread(threadID string, req protocol.UpdateThreadRequest) (protocol.ThreadSummary, error) {
	if _, _, err := s.backendFor(threadID); err != nil {
		return protocol.ThreadSummary{}, err
	}
	if req.Title == "" || len(req.Title) > 200 {
		return protocol.ThreadSummary{}, protocol.Errorf(protocol.CodeBadRequest, "title must be 1-200 characters")
	}
	title := req.Title
	s.registry.Update(threadID, func(rec *ThreadRecord) { rec.Title = &title })
	rec, err := s.registry.Require(threadID)
	if err != nil {
		return protocol.ThreadSummary{}, err
	}
	return s.registry.Summary(rec), nil
}

func (s *Service) DeleteThread(ctx context.Context, threadID string) error {
	backend, rec, err := s.backendFor(threadID)
	if err != nil {
		return err
	}
	if err := backend.Discard(ctx, rec); err != nil {
		return err
	}
	return s.registry.Remove(threadID)
}

func (s *Service) Transcript(ctx context.Context, threadID, before string, limit int) (protocol.TranscriptPage, error) {
	backend, rec, err := s.backendFor(threadID)
	if err != nil {
		return protocol.TranscriptPage{}, err
	}
	page, err := backend.Transcript(ctx, rec, before, limit)
	if err != nil {
		return protocol.TranscriptPage{}, err
	}
	if page.Entries == nil {
		page.Entries = []protocol.TranscriptEntry{}
	}
	return page, nil
}

// Handle is the one place a command is matched: kind-neutral policy here, the
// harness call in the backend.
func (s *Service) Handle(ctx context.Context, threadID string, cmd protocol.ClientCommand) error {
	if err := cmd.Validate(); err != nil {
		return err
	}
	backend, rec, err := s.backendFor(threadID)
	if err != nil {
		return err
	}
	switch cmd.Type {
	case protocol.CmdPrompt:
		if err := backend.Capabilities().CheckImages(cmd.Images); err != nil {
			return err
		}
		replay, err := s.prompts.seen(cmd)
		if err != nil || replay {
			return err
		}
		if err := backend.Prompt(ctx, rec, cmd); err != nil {
			return err
		}
		s.prompts.record(cmd)
		// The first prompt names the thread. Only one harness summarizes a
		// conversation, and a title that appears at once and then holds still
		// is worth more in a directory than a better one that arrives later.
		s.registry.Update(threadID, func(r *ThreadRecord) {
			if r.Title == nil {
				if title := titleFrom(cmd.Text); title != "" {
					r.Title = &title
				}
			}
		})
		return nil

	case protocol.CmdInterrupt:
		return backend.Interrupt(ctx, threadID)

	case protocol.CmdSteer:
		if !backend.Capabilities().SupportsSteer {
			return protocol.Errorf(protocol.CodeCapabilityUnsupported, "%s cannot steer a turn", backend.Kind())
		}
		// Steering is an edit to a turn in flight. The client only offers it
		// while one is running, so reaching here idle is the race where the
		// turn ended first -- and saying so beats a harness-level failure.
		if state := backend.RunState(threadID); state != protocol.StateRunning {
			return protocol.Errorf(protocol.CodeNoRunningTurn, "there is no running turn to steer")
		}
		return backend.Steer(ctx, threadID, cmd.Text)

	case protocol.CmdSetOptions:
		if err := backend.Capabilities().CheckOptions(cmd.Options); err != nil {
			return err
		}
		s.registry.Update(threadID, func(r *ThreadRecord) { r.Options = r.Options.Merge(cmd.Options) })
		updated, err := s.registry.Require(threadID)
		if err != nil {
			return err
		}
		return backend.SetOptions(ctx, updated)

	case protocol.CmdInteractionResponse:
		pending := backend.LiveState(threadID).Pending
		var request *protocol.InteractionRequest
		for i := range pending {
			if pending[i].RequestID == cmd.RequestID {
				request = &pending[i]
			}
		}
		if request == nil {
			return protocol.Errorf(protocol.CodeInteractionNotPending, "no pending interaction %s", cmd.RequestID)
		}
		if !request.Accepts(cmd.Decision) {
			return protocol.Errorf(protocol.CodeDecisionMismatch,
				"%s does not take a %s", request.Payload.Kind(), cmd.Decision.Type)
		}
		return backend.Respond(ctx, threadID, cmd.RequestID, cmd.Decision)
	}
	return protocol.Errorf(protocol.CodeBadRequest, "unknown command type %q", cmd.Type)
}

func (s *Service) SearchFiles(threadID, query string, limit int) (protocol.FileSearchResult, error) {
	_, rec, err := s.backendFor(threadID)
	if err != nil {
		return protocol.FileSearchResult{}, err
	}
	paths, truncated, err := fsx.Search(rec.Cwd, query, limit)
	if err != nil {
		return protocol.FileSearchResult{}, protocol.Errorf(protocol.CodeInternal, "search failed: %v", err)
	}
	return protocol.FileSearchResult{Paths: paths, Truncated: truncated}, nil
}

// ValidateCwd holds an owner-picked directory to one rule: absolute after
// canonicalization, existing, and a directory.
func ValidateCwd(path string) (string, error) {
	resolved, err := fsx.Canonicalize(path)
	if err != nil {
		return "", protocol.Errorf(protocol.CodeCwdInvalid, "no such directory: %s", path)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", protocol.Errorf(protocol.CodeCwdInvalid, "not a directory: %s", path)
	}
	return resolved, nil
}

// titleFrom is the first line of a prompt, short enough for a sidebar row.
func titleFrom(text string) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(text), "\n", 2)[0])
	if runes := []rune(line); len(runes) > 60 {
		return strings.TrimSpace(string(runes[:59])) + "\u2026"
	}
	return line
}

func newID() string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	// RFC 4122 version 4, so the id is also usable as a `claude --session-id`.
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	hexed := hex.EncodeToString(raw)
	return hexed[0:8] + "-" + hexed[8:12] + "-" + hexed[12:16] + "-" + hexed[16:20] + "-" + hexed[20:]
}
