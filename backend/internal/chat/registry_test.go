package chat_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/lmy375/agent-web/backend/internal/chat"
	"github.com/lmy375/agent-web/backend/internal/protocol"
)

func newRegistry(t *testing.T) *chat.Registry {
	t.Helper()
	registry, err := chat.NewRegistry(filepath.Join(t.TempDir(), "threads.db"))
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	return registry
}

func record(id string, updatedAt time.Time) chat.ThreadRecord {
	return chat.ThreadRecord{
		ThreadID: id, AgentKind: protocol.KindClaudeCode, Cwd: "/tmp",
		CreatedAt: updatedAt, UpdatedAt: updatedAt,
	}
}

// deadline fails the test rather than letting it hang for the whole package
// timeout, because the failure this guards against is a deadlock.
func deadline(t *testing.T, what string, run func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); run() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not return: the registry is holding its connection across a backend call", what)
	}
}

// TestDirectoryOrdersOnTheInstant guards the one thing about the directory that
// the driver decides rather than this package: it writes a timestamp as text,
// and the keyset index sorts that text. The format it picked drops trailing
// zeros, so a whole second and the same second plus a half differ in length --
// the case that orders wrongly if the separator after the fraction ever sorts
// above a digit. A driver or format change that breaks this fails here.
func TestDirectoryOrdersOnTheInstant(t *testing.T) {
	registry := newRegistry(t)
	whole := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	later := whole.Add(500 * time.Millisecond)

	if err := registry.Put(record("claude_code:whole", whole)); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := registry.Put(record("claude_code:later", later)); err != nil {
		t.Fatalf("put: %v", err)
	}

	page, hasMore := registry.Page(nil, 1)
	if len(page) != 1 || page[0].ThreadID != "claude_code:later" {
		t.Fatalf("newest row should be the later instant, got %+v", page)
	}
	if !hasMore {
		t.Fatal("expected an older row to remain")
	}

	cursor := protocol.ThreadKeyset{UpdatedAt: page[0].UpdatedAt, ThreadID: page[0].ThreadID}
	rest, hasMore := registry.Page(&cursor, 1)
	if len(rest) != 1 || rest[0].ThreadID != "claude_code:whole" {
		t.Fatalf("cursor should hand back the earlier instant, got %+v", rest)
	}
	if hasMore {
		t.Fatal("expected no rows past the last one")
	}
}

// TestKeysetBreaksTiesOnThreadID covers the second half of the cursor: rows
// sharing an instant page by descending id, with no row seen twice or skipped.
func TestKeysetBreaksTiesOnThreadID(t *testing.T) {
	registry := newRegistry(t)
	same := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"claude_code:a", "claude_code:b", "claude_code:c"} {
		if err := registry.Put(record(id, same)); err != nil {
			t.Fatalf("put: %v", err)
		}
	}

	seen := []string{}
	var cursor *protocol.ThreadKeyset
	for {
		page, hasMore := registry.Page(cursor, 2)
		for _, row := range page {
			seen = append(seen, row.ThreadID)
		}
		if !hasMore {
			break
		}
		last := page[len(page)-1]
		cursor = &protocol.ThreadKeyset{UpdatedAt: last.UpdatedAt, ThreadID: last.ThreadID}
	}

	want := []string{"claude_code:c", "claude_code:b", "claude_code:a"}
	if len(seen) != len(want) {
		t.Fatalf("paged %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("paged %v, want %v", seen, want)
		}
	}
}

// TestSummariesAreComposedWithNothingOpen is the one invariant the single
// connection turns from a race into a hang: a backend asked for a run state may
// read the registry back, so no statement or transaction may still be open when
// a row is published.
func TestSummariesAreComposedWithNothingOpen(t *testing.T) {
	registry := newRegistry(t)
	registry.Wire(
		func(threadID string) protocol.ThreadRunState {
			if _, ok := registry.Get(threadID); !ok {
				return protocol.StateIdle
			}
			return protocol.StateRunning
		},
		func(protocol.ServerEvent) {},
	)

	rec := record("claude_code:one", time.Now().UTC())
	deadline(t, "Put", func() {
		if err := registry.Put(rec); err != nil {
			t.Errorf("put: %v", err)
		}
	})
	deadline(t, "Update", func() {
		title := "named"
		registry.Update(rec.ThreadID, func(r *chat.ThreadRecord) { r.Title = &title })
	})
	deadline(t, "Republish", func() { registry.Republish(rec.ThreadID) })
	deadline(t, "Page", func() {
		if page, _ := registry.Page(nil, 10); len(page) != 1 || page[0].RunState != protocol.StateRunning {
			t.Errorf("expected one running row, got %+v", page)
		}
	})
	deadline(t, "Remove", func() {
		if err := registry.Remove(rec.ThreadID); err != nil {
			t.Errorf("remove: %v", err)
		}
	})
}

// TestRecordsSurviveAReopen proves the row is really on disk, and that an
// unnamed thread comes back unnamed rather than as an empty title.
func TestRecordsSurviveAReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.db")
	registry, err := chat.NewRegistry(path)
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	mode := protocol.ModePlan
	rec := record("claude_code:one", time.Now().UTC())
	rec.NativeID, rec.Options = "native-1", protocol.ThreadOptions{Mode: &mode}
	if err := registry.Put(rec); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := registry.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := chat.NewRegistry(path)
	if err != nil {
		t.Fatalf("reopen registry: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	got, ok := reopened.Get(rec.ThreadID)
	if !ok {
		t.Fatal("the record did not survive a reopen")
	}
	if got.Title != nil {
		t.Fatalf("an unnamed thread came back with a title: %q", *got.Title)
	}
	if got.NativeID != "native-1" || got.Options.Mode == nil || *got.Options.Mode != protocol.ModePlan {
		t.Fatalf("record came back changed: %+v", got)
	}
	if !got.UpdatedAt.Equal(rec.UpdatedAt) {
		t.Fatalf("updated_at came back as %v, want %v", got.UpdatedAt, rec.UpdatedAt)
	}
}

// olderTable is the threads table as an earlier build declared it: no title
// column. AutoMigrate has to add one without disturbing the row already there.
type olderTable struct {
	ThreadID  string    `gorm:"primaryKey"`
	AgentKind string    `gorm:"not null"`
	NativeID  string    `gorm:"not null"`
	Cwd       string    `gorm:"not null"`
	Options   string    `gorm:"not null"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

func (olderTable) TableName() string { return "threads" }

// TestOpeningMigratesAnOlderTable is the point of migrating on every start: a
// column added to the model reaches a database that predates it, and the rows
// already in that database are still there afterwards.
func TestOpeningMigratesAnOlderTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.db")
	old, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open the older database: %v", err)
	}
	if err := old.AutoMigrate(&olderTable{}); err != nil {
		t.Fatalf("create the older table: %v", err)
	}
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	row := olderTable{
		ThreadID: "claude_code:one", AgentKind: "claude_code", NativeID: "native-1",
		Cwd: "/tmp", Options: `{"model":"m1"}`, CreatedAt: stamp, UpdatedAt: stamp,
	}
	if err := old.Create(&row).Error; err != nil {
		t.Fatalf("seed the older table: %v", err)
	}
	pool, err := old.DB()
	if err != nil {
		t.Fatalf("reach the pool: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("close the older database: %v", err)
	}

	registry, err := chat.NewRegistry(path)
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	t.Cleanup(func() { _ = registry.Close() })

	rec, ok := registry.Get("claude_code:one")
	if !ok {
		t.Fatal("the row that predates the migration is gone")
	}
	if rec.Title != nil {
		t.Fatalf("the added column should read as unset, got %q", *rec.Title)
	}
	if rec.NativeID != "native-1" || rec.Options.Model == nil || *rec.Options.Model != "m1" {
		t.Fatalf("the row came back changed: %+v", rec)
	}

	// The added column is writable, which a column that migrated only in name
	// would not be.
	title := "named"
	registry.Update(rec.ThreadID, func(r *chat.ThreadRecord) { r.Title = &title })
	if got, _ := registry.Get(rec.ThreadID); got.Title == nil || *got.Title != title {
		t.Fatalf("title did not stick: %+v", got)
	}
}
