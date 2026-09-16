package protocol

import (
	"encoding/base64"
	"strings"
	"time"
)

// ThreadOptions are the knobs set_options can change. A nil field means: on
// create, the descriptor default; in set_options, unchanged; on a listed
// thread, a knob this kind does not have (every knob it has is filled).
type ThreadOptions struct {
	Model  *string         `json:"model"`
	Mode   *PermissionMode `json:"mode"`
	Effort *string         `json:"effort"` // an EffortOption.ID the descriptor lists
}

// Merge returns these options with every non-nil field of next applied.
func (o ThreadOptions) Merge(next ThreadOptions) ThreadOptions {
	if next.Model != nil {
		o.Model = next.Model
	}
	if next.Mode != nil {
		o.Mode = next.Mode
	}
	if next.Effort != nil {
		o.Effort = next.Effort
	}
	return o
}

// ThreadKeyset is a cursor over (updated_at desc, thread_id desc). Every
// backend receives the same keyset and returns threads strictly before it, so
// the union list pages correctly without per-source cursors.
type ThreadKeyset struct {
	UpdatedAt time.Time
	ThreadID  string
}

func (k ThreadKeyset) Encode() string {
	raw := k.UpdatedAt.UTC().Format(time.RFC3339Nano) + "|" + k.ThreadID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// Before reports whether (updatedAt, threadID) sorts strictly after the cursor
// in the descending order the directory uses, i.e. belongs on a later page.
func (k ThreadKeyset) Before(updatedAt time.Time, threadID string) bool {
	if !updatedAt.Equal(k.UpdatedAt) {
		return updatedAt.Before(k.UpdatedAt)
	}
	return threadID < k.ThreadID
}

func ParseKeyset(cursor string) (ThreadKeyset, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(cursor, "="))
	if err != nil {
		return ThreadKeyset{}, Errorf(CodeCursorInvalid, "unreadable cursor: %s", cursor)
	}
	stamp, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return ThreadKeyset{}, Errorf(CodeCursorInvalid, "unreadable cursor: %s", cursor)
	}
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return ThreadKeyset{}, Errorf(CodeCursorInvalid, "unreadable cursor: %s", cursor)
	}
	return ThreadKeyset{UpdatedAt: at, ThreadID: id}, nil
}

// ThreadSummary is one directory row. ThreadID is "<agent_kind>:<native id>":
// the prefix routes on the server, AgentKind is the same fact for the client so
// no client parses ids.
type ThreadSummary struct {
	ThreadID  string    `json:"thread_id"`
	AgentKind AgentKind `json:"agent_kind"`
	// Nil until the harness summarizes the first turn or the owner renames; the
	// placeholder is the client's i18n, not a server string.
	Title     *string        `json:"title"`
	Cwd       string         `json:"cwd"`
	UpdatedAt time.Time      `json:"updated_at"`
	RunState  ThreadRunState `json:"run_state"`
	Options   ThreadOptions  `json:"options"`
}

func (s ThreadSummary) Keyset() ThreadKeyset {
	return ThreadKeyset{UpdatedAt: s.UpdatedAt, ThreadID: s.ThreadID}
}

// ThreadDetail is what a client fetches after opening /events: the summary plus
// the live state the transcript does not hold. None of these three are events
// in any harness; they are current values, so they are a resource.
type ThreadDetail struct {
	Summary      ThreadSummary        `json:"summary"`
	Pending      []InteractionRequest `json:"pending"`
	ContextUsage *ContextUsage        `json:"context_usage"`
	LastTurn     *TurnSummary         `json:"last_turn"`
}

type ThreadList struct {
	Threads    []ThreadSummary `json:"threads"`
	NextCursor *string         `json:"next_cursor"`
}

type CreateThreadRequest struct {
	AgentKind AgentKind `json:"agent_kind"`
	// Absolute directory the thread runs in; empty is the kind's default_cwd.
	// The service validates it before any backend sees the request.
	Cwd     string        `json:"cwd"`
	Options ThreadOptions `json:"options"`
}

type UpdateThreadRequest struct {
	Title string `json:"title"`
}

// FileSearchResult holds paths relative to the thread's cwd, best match first.
type FileSearchResult struct {
	Paths     []string `json:"paths"`
	Truncated bool     `json:"truncated"`
}
