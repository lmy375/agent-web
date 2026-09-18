package chat

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"

	"github.com/lmy375/agent-web/backend/internal/protocol"
)

// ThreadRecord is one directory row plus what the next process start needs, and
// is also the mapped model: the table is this struct and nothing else.
//
// Options has every knob the kind offers filled; it stays one JSON column
// because it is kind-specific and grows with the descriptors, and a column per
// knob would tie the schema to the protocol's enum set. UpdatedAt is this
// server's own clock (create, prompt, turn end, rename), so the keyset cursor
// never depends on a harness file's mtime.
//
// NativeID is the harness's own identifier and is deliberately not the thread
// id's suffix: Claude lets us pin a session id up front, but Codex and OpenCode
// only mint one when the first turn starts, and a cold thread still has to have
// a stable id to appear in the directory.
type ThreadRecord struct {
	ThreadID  string                 `gorm:"primaryKey;index:threads_keyset,priority:2,sort:desc"`
	AgentKind protocol.AgentKind     `gorm:"not null"`
	NativeID  string                 `gorm:"not null"`
	Cwd       string                 `gorm:"not null"`
	Title     *string                // NULL until the owner names the thread
	Options   protocol.ThreadOptions `gorm:"serializer:json;not null"`
	// SystemPrompt is the workspace prompt as it stood when this thread was
	// created, already reduced to what this kind can do with it. A thread keeps
	// what it started with: the harnesses that take a prompt take it when the
	// conversation opens, so following a later edit would mean one set of
	// instructions for the turns before it and another for the turns after.
	// The column has no NOT NULL because rows written before it existed read
	// back as the zero value, which is no prompt.
	SystemPrompt protocol.SystemPrompt `gorm:"serializer:json"`
	CreatedAt    time.Time             `gorm:"not null"`
	UpdatedAt    time.Time             `gorm:"not null;index:threads_keyset,priority:1,sort:desc"`
}

func (ThreadRecord) TableName() string { return "threads" }

// settingsRow is the one row of workspace settings. Its id is pinned to 1, so
// a save replaces the row rather than growing the table.
type settingsRow struct {
	ID           uint                  `gorm:"primaryKey"`
	Locale       protocol.Locale       `gorm:"not null"`
	SystemPrompt protocol.SystemPrompt `gorm:"serializer:json;not null"`
}

func (settingsRow) TableName() string { return "settings" }

const settingsRowID = 1

// Registry is this web UI's SQLite database: the threads it created, so the
// union directory never lists a session someone started in a terminal, and the
// workspace settings, so a preference follows the owner to any browser.
type Registry struct {
	db *gorm.DB

	// Wired once by the service, which is the only thing that knows every
	// backend: a row's run state lives in its harness, not in this database.
	mu       sync.Mutex
	runState func(threadID string) protocol.ThreadRunState
	publish  func(protocol.ServerEvent)
}

// NewRegistry opens the database and migrates it, creating both as needed. A
// file that will not open is the owner's to deal with: the reasons are as often
// a permission or a disk as a damaged file, and a server that moves one aside
// to start anyway throws away a healthy directory to do it.
func NewRegistry(path string) (*Registry, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		// Every failure here already reaches the caller as a value; a library
		// writing to stdout on its own would be the only thing in this server
		// that does.
		Logger: logger.Discard,
		// The stamps GORM writes are a cursor's sort key and reach the client
		// as JSON, so they are UTC wherever the machine happens to be.
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, err
	}
	pool, err := db.DB()
	if err != nil {
		return nil, err
	}
	// One connection serialises every statement, which for a few kilobytes
	// belonging to a single user is cheaper than reasoning about SQLITE_BUSY.
	// WAL and the timeout cover the one case that leaves: a second server
	// started by accident against the same file.
	pool.SetMaxOpenConns(1)
	for _, pragma := range []string{`PRAGMA journal_mode = WAL`, `PRAGMA busy_timeout = 5000`} {
		if err := db.Exec(pragma).Error; err != nil {
			return nil, err
		}
	}
	// Migrating on every start is what keeps a schema change to editing the
	// struct above. It only ever adds tables, columns and indexes, so a binary
	// rolled back still reads the database a newer one left behind.
	if err := db.AutoMigrate(&ThreadRecord{}, &settingsRow{}); err != nil {
		return nil, err
	}
	// SQLite creates its files 0644. The records name every directory their
	// owner works in, so they get 0600; the write-ahead files SQLite creates
	// from here on inherit the database's mode.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Chmod(path+suffix, 0o600); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return &Registry{db: db}, nil
}

func (r *Registry) Close() error {
	pool, err := r.db.DB()
	if err != nil {
		return err
	}
	return pool.Close()
}

// Wire supplies what composing a ThreadSummary needs. Calling it twice is a
// wiring bug, not a runtime case, so it simply overwrites.
func (r *Registry) Wire(runState func(string) protocol.ThreadRunState, publish func(protocol.ServerEvent)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runState, r.publish = runState, publish
}

func (r *Registry) Get(threadID string) (ThreadRecord, bool) {
	var rec ThreadRecord
	err := r.db.First(&rec, "thread_id = ?", threadID).Error
	return rec, err == nil
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
	if err := r.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&rec).Error; err != nil {
		return err
	}
	r.publishRow(rec)
	return nil
}

// Update mutates one record in place, bumps UpdatedAt and publishes the row --
// so it is for real record changes, and a run state that moved is Republish.
// A thread the harness knows about but this database does not is silently
// ignored: a backend may report on a thread the owner deleted a moment ago.
func (r *Registry) Update(threadID string, mutate func(*ThreadRecord)) {
	if rec, ok := r.apply(threadID, mutate); ok {
		r.publishRow(rec)
	}
}

// apply is the read-modify-write half of Update, kept separate so the
// transaction has committed before anything is published: it holds the single
// connection, and composing a summary reads this registry back.
func (r *Registry) apply(threadID string, mutate func(*ThreadRecord)) (ThreadRecord, bool) {
	var rec ThreadRecord
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&rec, "thread_id = ?", threadID).Error; err != nil {
			return err
		}
		mutate(&rec)
		// Save writes every column and lets GORM stamp UpdatedAt, which is what
		// lifts the thread back to the top of the directory.
		return tx.Save(&rec).Error
	})
	return rec, err == nil
}

// Touch bumps UpdatedAt so the thread rises to the top of the directory.
func (r *Registry) Touch(threadID string) { r.Update(threadID, func(*ThreadRecord) {}) }

// Republish re-emits a row whose run state moved. The record itself is
// unchanged, so nothing is written to disk.
func (r *Registry) Republish(threadID string) {
	if rec, ok := r.Get(threadID); ok {
		r.publishRow(rec)
	}
}

func (r *Registry) Remove(threadID string) error {
	result := r.db.Delete(&ThreadRecord{}, "thread_id = ?", threadID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return nil
	}
	r.mu.Lock()
	publish := r.publish
	r.mu.Unlock()
	if publish != nil {
		publish(protocol.ThreadDeleted(threadID))
	}
	return nil
}

// publishRow emits a row's current state. It must be called with no transaction
// still open: composing a summary asks a backend for the run state, and a
// backend answering that reads this registry back -- which on the single
// connection this database uses would deadlock rather than merely race.
func (r *Registry) publishRow(rec ThreadRecord) {
	r.mu.Lock()
	publish := r.publish
	r.mu.Unlock()
	if publish != nil {
		publish(protocol.ThreadUpdated(r.Summary(rec)))
	}
}

// Summary composes a directory row. The run state comes from the thread's
// backend, so this must never be called from inside a transaction.
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
	// The row-value comparison is exactly ThreadKeyset.Before. One row past the
	// limit is read so that "older ones exist" needs no second query.
	query := r.db.Order("updated_at DESC, thread_id DESC").Limit(limit + 1)
	if cursor != nil {
		query = query.Where("(updated_at, thread_id) < (?, ?)", cursor.UpdatedAt, cursor.ThreadID)
	}
	var records []ThreadRecord
	if err := query.Find(&records).Error; err != nil {
		return []protocol.ThreadSummary{}, false
	}
	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}
	// Find has already read every row and freed the connection, which is what
	// lets Summary below re-enter this registry.
	out := make([]protocol.ThreadSummary, 0, len(records))
	for _, rec := range records {
		out = append(out, r.Summary(rec))
	}
	return out, hasMore
}

// Settings reads the workspace settings. An install where nothing has been
// saved yet has no row, which is the defaults rather than a failure.
func (r *Registry) Settings() (protocol.WorkspaceSettings, error) {
	var row settingsRow
	if err := r.db.First(&row, settingsRowID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return protocol.DefaultSettings(), nil
		}
		return protocol.WorkspaceSettings{}, err
	}
	return protocol.WorkspaceSettings{Locale: row.Locale, SystemPrompt: row.SystemPrompt}, nil
}

// SaveSettings replaces the single row. The caller has already validated it.
func (r *Registry) SaveSettings(settings protocol.WorkspaceSettings) error {
	row := settingsRow{ID: settingsRowID, Locale: settings.Locale, SystemPrompt: settings.SystemPrompt}
	return r.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error
}
