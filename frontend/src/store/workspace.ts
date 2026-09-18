import { LocalizedError, type DisplayError } from '@/i18n/core'
/** Everything that is true across threads: who is signed in, which agents this
 *  machine has, and the thread directory with its live updates. */
import { create } from 'zustand'
import { api, subscribe, type AuthStatus, type ServerConfig } from '@/lib/api'
import { useLanguage } from '@/i18n'
import type { AgentDescriptor, AgentKind, ServerEvent, ThreadOptions, ThreadSummary, WorkspaceSettings } from './protocol'

interface WorkspaceState {
  auth: AuthStatus | null
  /** Why the very first call failed. Until it is answered one way or the
   *  other, there is nothing the app can honestly render. */
  unreachable: DisplayError | null
  config: ServerConfig | null
  /** Null until the first load; every reader wants the server's answer rather
   *  than a guess at it. */
  settings: WorkspaceSettings | null
  agents: AgentDescriptor[]
  threads: ThreadSummary[]
  nextCursor: string | null
  loadingMore: boolean
  /** Why the directory could not be read, shown in place of the list. */
  error: DisplayError | null

  checkAuth: () => Promise<void>
  signIn: (password: string) => Promise<DisplayError | null>
  signOut: () => Promise<void>
  load: () => Promise<void>
  loadMore: () => Promise<void>
  watchDirectory: () => () => void
  saveSettings: (settings: WorkspaceSettings) => Promise<void>
  create: (kind: AgentKind, cwd: string, options?: Partial<ThreadOptions>) => Promise<ThreadSummary>
  rename: (id: string, title: string) => Promise<void>
  remove: (id: string) => Promise<void>
  descriptor: (kind: AgentKind) => AgentDescriptor | undefined
}

/** Newest first, which is the only order the directory is ever shown in. */
function sorted(threads: ThreadSummary[]): ThreadSummary[] {
  return [...threads].sort((a, b) => (a.updated_at === b.updated_at ? b.thread_id.localeCompare(a.thread_id) : b.updated_at.localeCompare(a.updated_at)))
}

/** The directory has three sources -- a page, a command's reply, and the live
 *  stream -- and they race: a new thread is published before its own POST has
 *  returned. All three fold in through here, keyed by thread id, so a row can
 *  only ever exist once however it arrived. */
function merge(existing: ThreadSummary[], incoming: ThreadSummary[]): ThreadSummary[] {
  const rows = new Map(existing.map((t) => [t.thread_id, t]))
  for (const row of incoming) rows.set(row.thread_id, row)
  return sorted([...rows.values()])
}

/** An install nobody has chosen a language for leaves the browser's own
 *  preferences deciding, which is what the mirror already resolved. */
function applyLocale(settings: WorkspaceSettings) {
  if (settings.locale) useLanguage.getState().setLocale(settings.locale)
}

export const useWorkspace = create<WorkspaceState>((set, get) => ({
  auth: null,
  unreachable: null,
  config: null,
  settings: null,
  agents: [],
  threads: [],
  nextCursor: null,
  loadingMore: false,
  error: null,

  async checkAuth() {
    try {
      set({ auth: await api.authStatus(), unreachable: null })
    } catch (error) {
      set({ unreachable: error instanceof Error ? error : new LocalizedError('error.unreachable') })
    }
  },

  async signIn(password) {
    try {
      set({ auth: await api.login(password) })
      return null
    } catch (error) {
      return error instanceof Error ? error : new LocalizedError('error.signIn')
    }
  },

  async signOut() {
    set({ auth: await api.logout(), threads: [], agents: [], settings: null })
  },

  async load() {
    try {
      const [config, agents, list, settings] = await Promise.all([api.config(), api.agents(), api.threads(), api.settings()])
      set({ config, agents, settings, threads: sorted(list.threads), nextCursor: list.next_cursor, error: null })
      applyLocale(settings)
    } catch (error) {
      set({ error: error instanceof Error ? error : new LocalizedError('error.unreachable') })
    }
  },

  // The language applies once the server has taken it, so a save that fails
  // leaves the app speaking the language the server still holds.
  async saveSettings(next) {
    const settings = await api.saveSettings(next)
    set({ settings })
    applyLocale(settings)
  },

  async loadMore() {
    const { nextCursor, threads, loadingMore } = get()
    if (!nextCursor || loadingMore) return
    set({ loadingMore: true })
    try {
      const list = await api.threads(nextCursor)
      set({ threads: merge(threads, list.threads), nextCursor: list.next_cursor })
    } finally {
      set({ loadingMore: false })
    }
  },

  // The directory stream is why a thread another tab is running shows as
  // running here, without anyone polling.
  watchDirectory() {
    return subscribe(null, {
      onEvent(event: ServerEvent) {
        if (event.type === 'thread_updated') {
          set({ threads: merge(get().threads, [event.summary]) })
        } else if (event.type === 'thread_deleted') {
          set({ threads: get().threads.filter((t) => t.thread_id !== event.thread_id) })
        }
      },
      onOpen: () => void get().load(),
    })
  },

  async create(kind, cwd, options) {
    const summary = await api.createThread({ agent_kind: kind, cwd, options })
    set({ threads: merge(get().threads, [summary]) })
    return summary
  },

  async rename(id, title) {
    const summary = await api.rename(id, title)
    set({ threads: merge(get().threads, [summary]) })
  },

  async remove(id) {
    await api.remove(id)
    set({ threads: get().threads.filter((t) => t.thread_id !== id) })
  },

  descriptor: (kind) => get().agents.find((a) => a.kind === kind),
}))
