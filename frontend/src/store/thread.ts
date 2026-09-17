import { LocalizedError, type DisplayError } from '@/i18n/core'
/** One open thread: its transcript, what it is blocked on, and what it costs. */
import { create } from 'zustand'
import { api, subscribe } from '@/lib/api'
import type {
  BackgroundTask, ClientCommand, ContextUsage, ImageBlock, InteractionDecision, InteractionRequest,
  ServerEvent, ThreadSummary, TurnSummary, UserBlock,
} from './protocol'
import { applyEvent, dropLocalPrompt, emptyTranscript, localPrompt, prependHistory, type Transcript } from './transcript'

interface ThreadState {
  id: string | null
  summary: ThreadSummary | null
  transcript: Transcript
  pending: InteractionRequest[]
  /** Every background task the harness is carrying; each event replaces the set. */
  backgroundTasks: BackgroundTask[]
  contextUsage: ContextUsage | null
  lastTurn: TurnSummary | null
  /** Older history exists; the transcript header offers to load it. */
  olderCursor: string | null
  loading: boolean
  /**
   * A prompt is on screen but the harness has not reported a turn yet, so the
   * composer is already busy and cannot double-send.
   */
  prompting: boolean
  error: DisplayError | null

  open: (id: string) => () => void
  /** False when the command failed; the caller decides what to undo. */
  send: (command: ClientCommand) => Promise<boolean>
  prompt: (text: string, images: ImageBlock[]) => Promise<boolean>
  respond: (requestID: string, decision: InteractionDecision) => Promise<boolean>
  stopTask: (taskID: string) => Promise<boolean>
  loadOlder: () => Promise<void>
  clearError: () => void
}

/** Crockford base32 ULID: the idempotency key of one owner prompt. */
const CROCKFORD = '0123456789ABCDEFGHJKMNPQRSTVWXYZ'
function ulid(): string {
  let time = Date.now()
  const stamp: string[] = []
  for (let i = 0; i < 10; i++) {
    stamp.unshift(CROCKFORD[time % 32])
    time = Math.floor(time / 32)
  }
  const random = crypto.getRandomValues(new Uint8Array(16))
  return stamp.join('') + Array.from(random, (b) => CROCKFORD[b % 32]).join('')
}

export const useThread = create<ThreadState>((set, get) => ({
  id: null,
  summary: null,
  transcript: emptyTranscript(),
  pending: [],
  backgroundTasks: [],
  contextUsage: null,
  lastTurn: null,
  olderCursor: null,
  loading: true,
  prompting: false,
  error: null,

  /**
   * Open a thread. The order is fixed by the protocol: the stream first, then
   * the detail and the history. Fetching first would drop whatever landed in
   * the gap, which is exactly where a permission prompt would be.
   */
  open(id) {
    set({
      id, summary: null, transcript: emptyTranscript(), pending: [], backgroundTasks: [],
      contextUsage: null, lastTurn: null, olderCursor: null, loading: true, prompting: false,
      error: null,
    })

    const unsubscribe = subscribe(id, {
      onEvent(event: ServerEvent) {
        if (get().id !== id) return
        switch (event.type) {
          case 'thread_updated':
            set({ summary: event.summary })
            break
          case 'context_usage':
            set({ contextUsage: event.usage })
            break
          case 'turn_finished':
            set({ lastTurn: event.summary, transcript: applyEvent(get().transcript, event) })
            break
          case 'interaction_request':
            set({ pending: [...get().pending.filter((r) => r.request_id !== event.request.request_id), event.request] })
            break
          case 'interaction_resolved':
            set({ pending: get().pending.filter((r) => r.request_id !== event.request_id) })
            break
          case 'background_tasks':
            set({ backgroundTasks: event.tasks })
            break
          default:
            set({ transcript: applyEvent(get().transcript, event) })
        }
      },
      // Runs on the first connection and on every reconnect, because a dropped
      // stream is exactly when this client and the server can disagree.
      onOpen: () => void resync(),
    })

    async function resync() {
      try {
        const [detail, page] = await Promise.all([api.thread(id), api.messages(id)])
        if (get().id !== id) return
        set({
          summary: detail.summary,
          pending: detail.pending,
          backgroundTasks: detail.background_tasks,
          contextUsage: detail.context_usage,
          lastTurn: detail.last_turn,
          transcript: prependHistory(get().transcript, page.entries),
          olderCursor: page.next_cursor,
          loading: false,
          error: null,
        })
      } catch (error) {
        if (get().id !== id) return
        set({ loading: false, error: error instanceof Error ? error : new LocalizedError('error.openThread') })
      }
    }

    return () => {
      unsubscribe()
      if (get().id === id) set({ id: null })
    }
  },

  async send(command) {
    const id = get().id
    if (!id) return false
    set({ error: null })
    try {
      await api.send(id, command)
      return true
    } catch (error) {
      set({ error: error instanceof Error ? error : new LocalizedError('error.command') })
      return false
    }
  },

  /**
   * The prompt goes on screen before it is sent. Starting a cold harness takes
   * seconds, and until it is up nothing can echo the prompt back, so waiting
   * for the echo would leave the owner looking at an empty composer.
   */
  async prompt(text, images) {
    const id = get().id
    if (!id) return false
    const clientMessageID = ulid()
    const blocks: UserBlock[] = [...images, ...(text ? [{ type: 'text' as const, text }] : [])]
    set({ transcript: localPrompt(get().transcript, clientMessageID, blocks), prompting: true })
    const ok = await get().send({ type: 'prompt', client_message_id: clientMessageID, text, images })
    if (get().id !== id) return ok
    set({ prompting: false })
    if (!ok) set({ transcript: dropLocalPrompt(get().transcript, clientMessageID) })
    return ok
  },

  respond(requestID, decision) {
    return get().send({ type: 'interaction_response', request_id: requestID, decision })
  },

  stopTask(taskID) {
    return get().send({ type: 'stop_task', task_id: taskID })
  },

  clearError() {
    set({ error: null })
  },

  async loadOlder() {
    const { id, olderCursor } = get()
    if (!id || !olderCursor) return
    const page = await api.messages(id, olderCursor)
    if (get().id !== id) return
    set({ transcript: prependHistory(get().transcript, page.entries), olderCursor: page.next_cursor })
  },
}))
