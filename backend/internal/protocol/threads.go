package protocol

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// ThreadOptions are the knobs set_options can change. Settings is keyed by
// OptionGroup.ID and its values are the harness's own, carried through
// untouched. A nil model or an absent setting means: on create, the descriptor
// default; in set_options, unchanged; on a listed thread, a knob this kind
// does not have (every knob it has is filled).
type ThreadOptions struct {
	Model    *string           `json:"model"`
	Settings map[string]string `json:"settings"`
}

// Setting is the value of one knob, or "" when this kind has no such knob.
func (o ThreadOptions) Setting(id string) string { return o.Settings[id] }

// Set stores one knob's value, allocating the map on first use.
func (o *ThreadOptions) Set(id, value string) {
	if o.Settings == nil {
		o.Settings = map[string]string{}
	}
	o.Settings[id] = value
}

// Merge returns these options with every field next fills applied. Settings
// merge key by key, so changing one knob never clears the others.
func (o ThreadOptions) Merge(next ThreadOptions) ThreadOptions {
	if next.Model != nil {
		o.Model = next.Model
	}
	if len(next.Settings) > 0 {
		merged := make(map[string]string, len(o.Settings)+len(next.Settings))
		for id, value := range o.Settings {
			merged[id] = value
		}
		for id, value := range next.Settings {
			merged[id] = value
		}
		o.Settings = merged
	}
	return o
}

// UnmarshalJSON also reads the flat `effort` of rows stored before settings
// existed: those values were already the harness's own spelling, so they move
// across unchanged. Their `mode` was a vocabulary of ours that no harness
// shares, so it is dropped and the kind's own default applies again.
func (o *ThreadOptions) UnmarshalJSON(data []byte) error {
	type plain ThreadOptions
	var raw struct {
		plain
		Effort *string `json:"effort"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*o = ThreadOptions(raw.plain)
	if raw.Effort != nil && o.Settings["effort"] == "" {
		if o.Settings == nil {
			o.Settings = map[string]string{}
		}
		o.Settings["effort"] = *raw.Effort
	}
	return nil
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

// BackgroundTask is one piece of work the harness carries outside a turn: a
// backgrounded shell command, a subagent, a workflow. Claude reports the whole
// live set on every membership change, so this is always current rather than
// assembled from start and end events.
type BackgroundTask struct {
	TaskID string `json:"task_id"`
	// TaskType is the harness's own vocabulary: Claude spells a backgrounded
	// Bash call local_bash, a subagent local_agent, a workflow local_workflow.
	TaskType    string `json:"task_type"`
	Description string `json:"description"`
	// Ambient tasks are housekeeping -- watchers and the like -- and are shown
	// in the task list without counting as work the thread is doing.
	Ambient bool `json:"ambient"`
}

// ThreadDetail is what a client fetches after opening /events: the summary plus
// the live state the transcript does not hold. None of these are events in any
// harness; they are current values, so they are a resource.
type ThreadDetail struct {
	Summary         ThreadSummary        `json:"summary"`
	Pending         []InteractionRequest `json:"pending"`
	ContextUsage    *ContextUsage        `json:"context_usage"`
	CurrentTurn     *RunningTurn         `json:"current_turn"`
	LastTurn        *TurnSummary         `json:"last_turn"`
	BackgroundTasks []BackgroundTask     `json:"background_tasks"`
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
