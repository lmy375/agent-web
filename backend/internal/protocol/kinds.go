// Package protocol is the kind-neutral vocabulary every agent backend adapts
// to. Each member notes the native value it maps to: Claude is the `claude`
// CLI's stream-json protocol, Codex is `codex app-server` JSON-RPC, OpenCode
// is the `opencode serve` HTTP API.
package protocol

// AgentKind identifies one harness. A thread id is "<kind>:<native id>", and
// the service routes on that prefix alone.
type AgentKind string

const (
	KindClaudeCode AgentKind = "claude_code" // `claude -p --input-format stream-json`
	KindCodex      AgentKind = "codex"       // `codex app-server`, one process, many threads
	KindOpenCode   AgentKind = "opencode"    // `opencode serve`, HTTP + SSE
)

// ThreadRunState is the directory row's live state. A dead harness process is
// resumed by the next prompt, never shown as a state.
type ThreadRunState string

const (
	StateStarting     ThreadRunState = "starting"
	StateIdle         ThreadRunState = "idle"
	StateRunning      ThreadRunState = "running"
	StateWaitingInput ThreadRunState = "waiting_input"
	// StateBackground is a thread with no turn under way whose harness is
	// still carrying background work. Claude starts a turn of its own when a
	// background task reports back, so this is not an idle conversation.
	StateBackground ThreadRunState = "background"
)

// TaskStatus is how a background task ended.
// Claude: task_notification.status.
type TaskStatus string

const (
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskStopped   TaskStatus = "stopped"
)

// TurnStatus is how one turn ended.
// Claude: ResultMessage.subtype. Codex: turn.status. OpenCode: message finish.
type TurnStatus string

const (
	TurnCompleted   TurnStatus = "completed"
	TurnInterrupted TurnStatus = "interrupted"
	TurnFailed      TurnStatus = "failed"
)

// ToolKind is a rendering category; tool_use.name stays the harness-native name.
type ToolKind string

const (
	ToolShell     ToolKind = "shell"      // Claude: Bash | Codex: commandExecution | OpenCode: bash
	ToolFileEdit  ToolKind = "file_edit"  // Claude: Edit, MultiEdit, NotebookEdit | Codex: fileChange | OpenCode: edit, patch
	ToolFileWrite ToolKind = "file_write" // Claude: Write | Codex: fileChange (new file) | OpenCode: write
	ToolFileRead  ToolKind = "file_read"  // Claude: Read, Glob | OpenCode: read, glob, list
	ToolSearch    ToolKind = "search"     // Claude: Grep | OpenCode: grep
	ToolTodo      ToolKind = "todo"       // Claude: TodoWrite | Codex: turn/plan/updated | OpenCode: todowrite
	ToolMCP       ToolKind = "mcp"        // Claude: mcp__* | Codex: mcpToolCall | OpenCode: mcp tools
	ToolSubagent  ToolKind = "subagent"   // Claude: Task | Codex: collabAgentToolCall | OpenCode: task
	ToolWeb       ToolKind = "web"        // Claude: WebSearch, WebFetch | Codex: webSearch | OpenCode: webfetch
	ToolOther     ToolKind = "other"
)

// InteractionKind is what the harness is blocking on.
type InteractionKind string

const (
	InteractionPermission InteractionKind = "permission" // Claude: can_use_tool | Codex: */requestApproval | OpenCode: permission.updated
	InteractionQuestion   InteractionKind = "question"   // Claude: AskUserQuestion | Codex: tool/requestUserInput
	InteractionPlan       InteractionKind = "plan"       // Claude: ExitPlanMode, answered with allow / deny
)

// RememberScope is how long an allow decision sticks.
type RememberScope string

const (
	ScopeSession RememberScope = "session" // Claude: session | Codex: acceptForSession | OpenCode: always (session)
	ScopeAlways  RememberScope = "always"  // Claude: userSettings | Codex: execpolicy amendment
)

// ContextBoundaryReason is why the context was compacted.
type ContextBoundaryReason string

const (
	CompactAuto   ContextBoundaryReason = "auto_compaction"   // Claude: compact_boundary trigger=auto | Codex: contextCompaction item
	CompactManual ContextBoundaryReason = "manual_compaction" // Claude: trigger=manual | Codex: thread/compact/start
)

// NoticeKind picks the banner; message is the harness's own wording.
type NoticeKind string

const (
	NoticeRateLimit NoticeKind = "rate_limit" // Claude: RateLimitEvent | Codex: account/rateLimits/updated
	NoticeAccount   NoticeKind = "account"    // Claude: credential probe | Codex: account/updated
)

// StreamErrorCode says why an error event was emitted. The client renders and
// decides to retry by code, never by parsing the message.
type StreamErrorCode string

const (
	ErrHarnessExited   StreamErrorCode = "harness_exited"   // process exit; fatal
	ErrCredential      StreamErrorCode = "credential"       // Claude: auth failure | Codex: login required
	ErrRateLimited     StreamErrorCode = "rate_limited"     //
	ErrContextOverflow StreamErrorCode = "context_overflow" // prompt longer than the window
	ErrUnrenderable    StreamErrorCode = "unrenderable"     // a harness message the schemas cannot map
	ErrStreamOverflow  StreamErrorCode = "stream_overflow"  // the subscriber fell behind the bounded queue; fatal
	ErrOther           StreamErrorCode = "other"
)
