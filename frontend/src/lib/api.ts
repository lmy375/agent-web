import type {
  AgentDescriptor, ClientCommand, CreateThread, ServerEvent,
  ThreadDetail, ThreadList, ThreadSummary, TranscriptPage,
} from './apiTypes'

export class ApiError extends Error {
  code: string
  status: number
  constructor(code: string, message: string, status: number) {
    super(message)
    this.code = code
    this.status = status
  }
}

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch('/api' + path, {
    ...init,
    headers: init?.body ? { 'Content-Type': 'application/json', ...init?.headers } : init?.headers,
  })
  if (!response.ok) {
    const body = (await response.json().catch(() => null)) as { detail?: { code: string; message: string } } | null
    throw new ApiError(body?.detail?.code ?? 'unknown', body?.detail?.message ?? response.statusText, response.status)
  }
  if (response.status === 204) return undefined as T
  return (await response.json()) as T
}

const post = <T>(path: string, body: unknown) => call<T>(path, { method: 'POST', body: JSON.stringify(body) })

export interface ServerConfig { root_dir: string; home_dir: string }
export interface AuthStatus { password_required: boolean; signed_in: boolean }
export interface DirListing { path: string; parent: string; dirs: { name: string; path: string }[] }

export const api = {
  authStatus: () => call<AuthStatus>('/auth/status'),
  login: (password: string) => post<AuthStatus>('/auth/login', { password }),
  logout: () => post<AuthStatus>('/auth/logout', {}),

  config: () => call<ServerConfig>('/config'),
  agents: () => call<AgentDescriptor[]>('/agents'),
  dirs: (path?: string) => call<DirListing>('/fs/dirs' + (path ? `?path=${encodeURIComponent(path)}` : '')),

  threads: (cursor?: string) => call<ThreadList>('/threads' + (cursor ? `?cursor=${encodeURIComponent(cursor)}` : '')),
  createThread: (body: CreateThread) => post<ThreadSummary>('/threads', body),
  thread: (id: string) => call<ThreadDetail>(`/threads/${encodeURIComponent(id)}`),
  rename: (id: string, title: string) =>
    call<ThreadSummary>(`/threads/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify({ title }) }),
  remove: (id: string) => call<void>(`/threads/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  messages: (id: string, before?: string) =>
    call<TranscriptPage>(`/threads/${encodeURIComponent(id)}/messages` + (before ? `?before=${encodeURIComponent(before)}` : '')),
  send: (id: string, command: ClientCommand) => post<void>(`/threads/${encodeURIComponent(id)}/input`, command),
  searchFiles: (id: string, query: string) =>
    call<{ paths: string[]; truncated: boolean }>(
      `/threads/${encodeURIComponent(id)}/files/search?q=${encodeURIComponent(query)}`,
    ),
}

/**
 * Subscribe to a thread's events, or to the directory stream when `threadID` is
 * null. EventSource reconnects on its own; `onOpen` fires on every connection,
 * which is where a caller resyncs the state the gap may have changed.
 */
export function subscribe(
  threadID: string | null,
  handlers: { onEvent: (event: ServerEvent) => void; onOpen?: () => void },
): () => void {
  const path = threadID === null ? '/api/events' : `/api/threads/${encodeURIComponent(threadID)}/events`
  const source = new EventSource(path)
  // Every event type is its own SSE event name, so one generic listener has to
  // be attached per name rather than to `message`.
  const names: ServerEvent['type'][] = [
    'thread_updated', 'thread_deleted', 'context_usage', 'turn_started', 'turn_finished',
    'user_message', 'assistant_message', 'text_delta', 'thinking_delta',
    'tool_use_start', 'tool_input_delta', 'tool_use_end', 'tool_output_delta', 'tool_result',
    'interaction_request', 'interaction_resolved', 'context_boundary', 'notice', 'error',
  ]
  const listener = (event: MessageEvent<string>) => handlers.onEvent(JSON.parse(event.data) as ServerEvent)
  for (const name of names) source.addEventListener(name, listener as EventListener)
  if (handlers.onOpen) source.addEventListener('open', handlers.onOpen)
  return () => {
    for (const name of names) source.removeEventListener(name, listener as EventListener)
    source.close()
  }
}
