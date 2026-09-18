/**
 * The wire protocol, mirrored from backend/internal/protocol. Nothing here is
 * specific to an agent: the client renders whatever a descriptor offers, so a
 * check on `agent_kind` anywhere outside the identity chip is a defect.
 */

export type AgentKind = 'claude_code' | 'codex' | 'opencode' | 'pi'
export type RunState = 'starting' | 'idle' | 'running' | 'waiting_input' | 'background'
export type TaskStatus = 'completed' | 'failed' | 'stopped'
export type TurnStatus = 'completed' | 'interrupted' | 'failed'
export type ToolKind =
  | 'shell' | 'file_edit' | 'file_write' | 'file_read' | 'search'
  | 'todo' | 'mcp' | 'subagent' | 'web' | 'other'
export type ImageMediaType = 'image/png' | 'image/jpeg' | 'image/gif' | 'image/webp'

export interface ThreadOptions {
  model: string | null
  /** Keyed by OptionGroup.id, with the harness's own values. */
  settings: Record<string, string>
}

export interface ThreadSummary {
  thread_id: string
  agent_kind: AgentKind
  title: string | null
  cwd: string
  updated_at: string
  run_state: RunState
  options: ThreadOptions
}

export interface ContextUsage {
  total_tokens: number
  max_tokens: number
  categories: { name: string; tokens: number }[]
  auto_compact_threshold_tokens: number | null
}

export interface Usage {
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  reasoning_tokens: number | null
}

export interface TurnSummary {
  status: TurnStatus
  usage: Usage | null
  cost_usd: number | null
}

/** One piece of work the harness carries outside a turn: a backgrounded shell
 *  command, a subagent, a workflow. Ambient tasks are housekeeping and are
 *  listed without counting as work the thread is doing. */
export interface BackgroundTask {
  task_id: string
  /** The harness's own vocabulary, e.g. local_bash, local_agent, local_workflow. */
  task_type: string
  description: string
  ambient: boolean
}

export interface ThreadDetail {
  summary: ThreadSummary
  pending: InteractionRequest[]
  context_usage: ContextUsage | null
  last_turn: TurnSummary | null
  background_tasks: BackgroundTask[]
}

export interface ThreadList {
  threads: ThreadSummary[]
  next_cursor: string | null
}

// --- descriptors ---

export interface ModelOption {
  id: string
  label: string
  description: string | null
}

export interface OptionChoice {
  value: string
  label: string
  description: string | null
}

/** One knob a harness offers, named and valued the way that harness names and
 *  values it. Nothing here is translated or folded together. */
export interface OptionGroup {
  id: string
  label: string
  options: OptionChoice[]
}

export interface SlashCommandInfo {
  name: string
  description: string
  argument_hint: string
}

export interface AgentCapabilities {
  max_images_per_prompt: number
  max_image_bytes: number
  supports_steer: boolean
  reports_cost: boolean
  supports_interrupt: boolean
}

export interface AgentRuntimeInfo {
  unavailable_reason: string | null
  models: ModelOption[]
  groups: OptionGroup[]
  default_cwd: string
  defaults: ThreadOptions
  commands: SlashCommandInfo[]
}

export interface AgentDescriptor {
  kind: AgentKind
  label: string
  capabilities: AgentCapabilities
  runtime: AgentRuntimeInfo
}

// --- content blocks ---

export interface TextBlock { type: 'text'; text: string }
export interface ThinkingBlock { type: 'thinking'; text: string }
export interface ImageBlock { type: 'image'; media_type: ImageMediaType; data_base64: string }
export interface ToolUseBlock {
  type: 'tool_use'
  id: string
  name: string
  tool_kind: ToolKind
  input: Record<string, unknown>
}
export interface ToolResultBlock {
  type: 'tool_result'
  tool_use_id: string
  content: (TextBlock | ImageBlock)[]
  is_error: boolean
}

export type ContentBlock = TextBlock | ThinkingBlock | ImageBlock | ToolUseBlock | ToolResultBlock
export type UserBlock = TextBlock | ImageBlock

// --- interactions ---

export interface PermissionSuggestion { rule: string; scope: 'session' | 'always' }

export interface PermissionPayload {
  kind: 'permission'
  tool_use_id: string
  tool_name: string
  tool_kind: ToolKind
  tool_input: Record<string, unknown>
  suggestions: PermissionSuggestion[]
}

export interface Question {
  id: string
  prompt: string
  options: { label: string; description: string | null }[]
  multi_select: boolean
}

export interface QuestionPayload { kind: 'question'; questions: Question[] }
export interface PlanPayload { kind: 'plan'; plan_markdown: string }

export type InteractionPayload = PermissionPayload | QuestionPayload | PlanPayload

export interface InteractionRequest {
  request_id: string
  created_at: string
  payload: InteractionPayload
}

export type InteractionDecision =
  | { type: 'allow'; updated_input?: Record<string, unknown>; remember?: PermissionSuggestion }
  | { type: 'deny'; message?: string; interrupt?: boolean }
  | { type: 'answer'; answers: { question_id: string; answers: string[] }[] }

// --- server events ---

interface EventBase { ts: string; thread_id: string }

export type ServerEvent =
  | (EventBase & { type: 'thread_updated'; summary: ThreadSummary })
  | (EventBase & { type: 'thread_deleted' })
  | (EventBase & { type: 'context_usage'; usage: ContextUsage })
  | (EventBase & { type: 'turn_started'; client_message_id: string })
  | (EventBase & { type: 'turn_finished'; client_message_id: string; summary: TurnSummary })
  | (EventBase & { type: 'user_message'; message_id: string; blocks: UserBlock[]; client_message_id: string | null; parent_tool_use_id: string | null })
  | (EventBase & { type: 'assistant_message'; message_id: string; blocks: ContentBlock[]; parent_tool_use_id: string | null })
  | (EventBase & { type: 'text_delta'; message_id: string; block_index: number; text: string; parent_tool_use_id: string | null })
  | (EventBase & { type: 'thinking_delta'; message_id: string; block_index: number; text: string; parent_tool_use_id: string | null })
  | (EventBase & { type: 'tool_use_start'; message_id: string; block_index: number; tool_use_id: string; name: string; tool_kind: ToolKind; parent_tool_use_id: string | null })
  | (EventBase & { type: 'tool_input_delta'; tool_use_id: string; partial_json: string })
  | (EventBase & { type: 'tool_use_end'; tool_use_id: string; input: Record<string, unknown> })
  | (EventBase & { type: 'tool_output_delta'; tool_use_id: string; text: string })
  | (EventBase & { type: 'tool_result'; tool_use_id: string; content: (TextBlock | ImageBlock)[]; is_error: boolean })
  | (EventBase & { type: 'interaction_request'; request: InteractionRequest })
  | (EventBase & { type: 'interaction_resolved'; request_id: string; decision: InteractionDecision | null })
  | (EventBase & { type: 'context_boundary'; reason: 'auto_compaction' | 'manual_compaction'; tokens_before: number | null })
  | (EventBase & { type: 'background_tasks'; tasks: BackgroundTask[] })
  | (EventBase & { type: 'background_task_finished'; task_id: string; status: TaskStatus; summary: string })
  | (EventBase & { type: 'notice'; kind: 'rate_limit' | 'account'; message: string })
  | (EventBase & { type: 'error'; code: StreamErrorCode; message: string; fatal: boolean })

export type StreamErrorCode =
  | 'harness_exited' | 'credential' | 'rate_limited'
  | 'context_overflow' | 'unrenderable' | 'stream_overflow' | 'other'

/** What both harnesses persist. A page of these renders exactly like the stream. */
export type TranscriptEntry = Extract<
  ServerEvent,
  { type: 'user_message' | 'assistant_message' | 'tool_result' | 'context_boundary' | 'background_task_finished' }
>

export interface TranscriptPage {
  entries: TranscriptEntry[]
  next_cursor: string | null
}

// --- commands ---

export type ClientCommand =
  | { type: 'prompt'; client_message_id: string; text: string; images?: ImageBlock[] }
  | { type: 'interrupt' }
  | { type: 'steer'; text: string }
  | { type: 'set_options'; options: Partial<ThreadOptions> }
  | { type: 'interaction_response'; request_id: string; decision: InteractionDecision }
  | { type: 'stop_task'; task_id: string }
