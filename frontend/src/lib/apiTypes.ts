/** Re-export of the wire types the API client speaks, plus the request bodies. */
export type {
  AgentDescriptor, ClientCommand, ServerEvent, ThreadDetail,
  ThreadList, ThreadSummary, TranscriptPage,
} from '@/store/protocol'
import type { AgentKind, ThreadOptions } from '@/store/protocol'

export interface CreateThread {
  agent_kind: AgentKind
  cwd?: string
  options?: Partial<ThreadOptions>
}
