package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/lmy375/agent-web/backend/internal/protocol"
)

// ThreadRecord is one directory row plus what the next process start needs.
// Options has every knob the kind offers filled; UpdatedAt is this server's own
// clock (create, prompt, turn end, rename), so the keyset cursor never depends
// on a harness file's mtime.
//
// NativeID is the harness's own identifier and is deliberately not the thread
// id's suffix: Claude lets us pin a session id up front, but Codex and OpenCode
// only mint one when the first turn starts, and a cold thread still has to have
// a stable id to appear in the directory.
type ThreadRecord struct {
	ThreadID  string                 `json:"thread_id"` // "<agent_kind>:<local id>"
	AgentKind protocol.AgentKind     `json:"agent_kind"`
	NativeID  string                 `json:"native_id"`
	Cwd       string                 `json:"cwd"`
	Title     *string                `json:"title"`
	Options   protocol.ThreadOptions `json:"options"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

type registryFile struct {
	Threads map[string]ThreadRecord `json:"threads"`
}

// Registry is the JSON file of threads this web UI created, so the union
// directory never lists a session someone started in a terminal. It is loaded
// on first access and written as a whole-file atomic replace; the file stays a
// few kilobytes.
type Registry struct {
	path string

	mu      sync.Mutex
	loaded  bool
	threads map[string]ThreadRecord

	// Wired once by the service, which is the only thing that knows every
	// backend: a row's run state lives in its harness, not in this file.
	runState func(threadID string) protocol.ThreadRunState
	publish  func(protocol.ServerEvent)
}

func NewRegistry(path string) *Registry {
	return &Registry{path: path, threads: map[string]ThreadRecord{}}
}

// Wire supplies what composing a ThreadSummary needs. Calling it twice is a
// wiring bug, not a runtime case, so it simply overwrites.
func (r *Registry) Wire(runState func(string) protocol.ThreadRunState, publish func(protocol.ServerEvent)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runState, r.publish = runState, publish
}

// load reads the file once, on first access, so building the server touches no
// state. A file that will not parse must not keep the server from starting: the
// harness transcripts are the real data, this file is only the index, so it is
// moved aside and the directory starts empty.
func (r *Registry) load() {
	if r.loaded {
		return
	}
	r.loaded = true
	raw, err := os.ReadFile(r.path)
	if err != nil {
		return
	}
	var file registryFile
	if err := json.Unmarshal(raw, &file); err != nil {
		_ = os.Rename(r.path, r.path+".corrupt")
		return
	}
	for id, rec := range file.Threads {
		r.threads[id] = rec
	}
}

func (r *Registry) save() error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(registryFile{Threads: r.threads}, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

func (r *Registry) Get(threadID string) (ThreadRecord, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.load()
	rec, ok := r.threads[threadID]
	return rec, ok
}

func (r *Registry) Require(threadID string) (ThreadRecord, error) {
	rec, ok := r.Get(threadID)
	if !ok {
		return ThreadRecord{}, protocol.Errorf(protocol.CodeThreadNotFound, "unknown thread %s", threadID)
	}
	return rec, nil
}

// Put inserts or replaces a record and publishes the new row.
func (r *Registry) Put(rec ThreadRecord) error {
	r.mu.Lock()
	r.load()
	r.threads[rec.ThreadID] = rec
	err := r.save()
	r.mu.Unlock()
	if err == nil {
		r.publishRow(rec)
	}
	return err
}

// Update mutates one record in place, bumps UpdatedAt and publishes the row --
// so it is for real record changes, and a run state that moved is Republish.
// A thread the harness knows about but this file does not is silently ignored:
// a backend may report on a thread the owner deleted a moment ago.
func (r *Registry) Update(threadID string, mutate func(*ThreadRecord)) {
	r.mu.Lock()
	r.load()
	rec, ok := r.threads[threadID]
	if !ok {
		r.mu.Unlock()
		return
	}
	mutate(&rec)
	rec.UpdatedAt = time.Now().UTC()
	r.threads[threadID] = rec
	_ = r.save()
	r.mu.Unlock()
	r.publishRow(rec)
}

// Touch bumps UpdatedAt so the thread rises to the top of the directory.
func (r *Registry) Touch(threadID string) { r.Update(threadID, func(*ThreadRecord) {}) }

// Republish re-emits a row whose run state moved. The record itself is
// unchanged, so nothing is written to disk.
func (r *Registry) Republish(threadID string) {
	r.mu.Lock()
	r.load()
	rec, ok := r.threads[threadID]
	if !ok {
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	r.publishRow(rec)
}

func (r *Registry) Remove(threadID string) error {
	r.mu.Lock()
	r.load()
	if _, ok := r.threads[threadID]; !ok {
		r.mu.Unlock()
		return nil
	}
	delete(r.threads, threadID)
	err := r.save()
	publish := r.publish
	r.mu.Unlock()
	if err == nil && publish != nil {
		publish(protocol.ThreadDeleted(threadID))
	}
	return err
}

// publishRow emits a row's current state. It must be called with the lock
// released: composing a summary asks a backend for the run state, and a backend
// answering that reads this registry back.
func (r *Registry) publishRow(rec ThreadRecord) {
	r.mu.Lock()
	publish := r.publish
	r.mu.Unlock()
	if publish != nil {
		publish(protocol.ThreadUpdated(r.Summary(rec)))
	}
}

// Summary composes a directory row. The run state comes from the thread's
// backend, so this must never be called under the registry lock.
func (r *Registry) Summary(rec ThreadRecord) protocol.ThreadSummary {
	r.mu.Lock()
	runState := r.runState
	r.mu.Unlock()
	state := protocol.StateIdle
	if runState != nil {
		state = runState(rec.ThreadID)
	}
	return protocol.ThreadSummary{
		ThreadID:  rec.ThreadID,
		AgentKind: rec.AgentKind,
		Title:     rec.Title,
		Cwd:       rec.Cwd,
		UpdatedAt: rec.UpdatedAt,
		RunState:  state,
		Options:   rec.Options,
	}
}

// Page returns rows strictly before the cursor in (updated_at, thread_id)
// descending order, and whether older ones exist.
func (r *Registry) Page(cursor *protocol.ThreadKeyset, limit int) ([]protocol.ThreadSummary, bool) {
	r.mu.Lock()
	r.load()
	records := make([]ThreadRecord, 0, len(r.threads))
	for _, rec := range r.threads {
		if cursor == nil || cursor.Before(rec.UpdatedAt, rec.ThreadID) {
			records = append(records, rec)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if !records[i].UpdatedAt.Equal(records[j].UpdatedAt) {
			return records[i].UpdatedAt.After(records[j].UpdatedAt)
		}
		return records[i].ThreadID > records[j].ThreadID
	})
	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}
	r.mu.Unlock()

	out := make([]protocol.ThreadSummary, 0, len(records))
	for _, rec := range records {
		out = append(out, r.Summary(rec))
	}
	return out, hasMore
}
