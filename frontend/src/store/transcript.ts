/**
 * Folds the event stream into the tree the transcript renders. The same
 * function handles live events and replayed history, which is the point of the
 * protocol: a settled transcript entry is the same shape as the event that
 * announced it, so nothing renders differently after a reload.
 */
import type {
  ContentBlock, ImageBlock, ServerEvent, TextBlock, ToolKind, TranscriptEntry, UserBlock,
} from './protocol'

export interface ToolBlock {
  type: 'tool'
  id: string
  name: string
  toolKind: ToolKind
  input: Record<string, unknown>
  /** Streaming argument JSON, before `tool_use_end` settles `input`. */
  partialJson?: string
  /** Streamed stdout, for a harness that sends it. */
  output?: string
  result?: { content: (TextBlock | ImageBlock)[]; isError: boolean }
  /** A subagent's own transcript, routed here by `parent_tool_use_id`. */
  children: Item[]
}

export type UiBlock =
  | { type: 'text'; text: string; final: boolean }
  | { type: 'thinking'; text: string; final: boolean }
  | ToolBlock

export type Item =
  | { kind: 'user'; id: string; blocks: UserBlock[] }
  | { kind: 'assistant'; id: string; blocks: UiBlock[]; streaming: boolean }
  | { kind: 'note'; id: string; label: string; contextBoundary?: boolean; tokensBefore?: number | null }
  | { kind: 'alert'; id: string; code: string; message: string; fatal: boolean }

export interface Transcript {
  items: Item[]
  /** Ids already folded in, so replayed history never doubles a live event. */
  seen: Set<string>
}

export const emptyTranscript = (): Transcript => ({ items: [], seen: new Set() })

let counter = 0
const localID = () => `local-${++counter}`

/** Text the harnesses wrap around their own injected context, never the owner's. */
const SYSTEM_REMINDER = /<system-reminder>[\s\S]*?<\/system-reminder>/g

function findTool(items: Item[], toolUseID: string): ToolBlock | null {
  for (const item of items) {
    if (item.kind !== 'assistant') continue
    for (const block of item.blocks) {
      if (block.type !== 'tool') continue
      if (block.id === toolUseID) return block
      const nested = findTool(block.children, toolUseID)
      if (nested) return nested
    }
  }
  return null
}

/** The list an event belongs to: the transcript, or one subagent's children. */
function listFor(t: Transcript, parent: string | null | undefined): Item[] | null {
  if (!parent) return t.items
  return findTool(t.items, parent)?.children ?? null
}

function assistantItem(list: Item[], messageID: string, streaming: boolean) {
  for (let i = list.length - 1; i >= 0; i--) {
    const item = list[i]
    if (item.kind === 'assistant' && item.id === messageID) return item
  }
  const created: Item = { kind: 'assistant', id: messageID, blocks: [], streaming }
  list.push(created)
  return created as Extract<Item, { kind: 'assistant' }>
}

/** Place a streamed block at the index the harness numbered it. */
function blockAt(item: Extract<Item, { kind: 'assistant' }>, index: number, make: () => UiBlock): UiBlock {
  const existing = item.blocks[index]
  if (existing) return existing
  while (item.blocks.length < index) item.blocks.push({ type: 'text', text: '', final: true })
  const created = make()
  item.blocks[index] = created
  return created
}

function toUiBlock(block: ContentBlock): UiBlock | null {
  switch (block.type) {
    case 'text': {
      const text = block.text.replace(SYSTEM_REMINDER, '').trim()
      return text ? { type: 'text', text, final: true } : null
    }
    case 'thinking':
      return block.text ? { type: 'thinking', text: block.text, final: true } : null
    case 'tool_use':
      return { type: 'tool', id: block.id, name: block.name, toolKind: block.tool_kind, input: block.input, children: [] }
    default:
      return null
  }
}

/**
 * Settle an assistant message: what the final message carries replaces what was
 * streamed, and what it does not mention is kept rather than deleted. A harness
 * can stream reasoning and then omit it from the settled message, and a tool
 * call already has a result attached that the message never had.
 */
function settle(item: Extract<Item, { kind: 'assistant' }>, blocks: ContentBlock[]) {
  const settled: UiBlock[] = []
  for (const raw of blocks) {
    const next = toUiBlock(raw)
    if (!next) continue
    if (next.type === 'tool') {
      const existing = findTool([item], next.id)
      if (existing) {
        existing.input = next.input
        existing.partialJson = undefined
        settled.push(existing)
        continue
      }
    }
    settled.push(next)
  }
  const keptThinking = settled.some((b) => b.type === 'thinking')
    ? []
    : item.blocks.filter((b) => b.type === 'thinking' && b.text.trim())
  const keptTools = item.blocks.filter((b) => b.type === 'tool' && !settled.includes(b))
  // Reasoning came before the answer, and a tool the message forgot came after.
  item.blocks = [...keptThinking, ...settled, ...keptTools]
  item.streaming = false
}

function userBlocks(blocks: UserBlock[]): UserBlock[] {
  const out: UserBlock[] = []
  for (const block of blocks) {
    if (block.type !== 'text') {
      out.push(block)
      continue
    }
    const text = block.text.replace(SYSTEM_REMINDER, '').trim()
    if (text) out.push({ type: 'text', text })
  }
  return out
}

export function applyEvent(t: Transcript, event: ServerEvent): Transcript {
  switch (event.type) {
    case 'user_message': {
      if (t.seen.has(event.message_id)) return t
      t.seen.add(event.message_id)
      const list = listFor(t, event.parent_tool_use_id)
      const blocks = userBlocks(event.blocks)
      if (!list || !blocks.length) return t
      list.push({ kind: 'user', id: event.message_id, blocks })
      return { ...t }
    }

    case 'assistant_message': {
      const list = listFor(t, event.parent_tool_use_id)
      if (!list) return t
      if (t.seen.has(event.message_id) && !list.some((i) => i.kind === 'assistant' && i.id === event.message_id)) return t
      t.seen.add(event.message_id)
      settle(assistantItem(list, event.message_id, false), event.blocks)
      return { ...t }
    }

    case 'text_delta':
    case 'thinking_delta': {
      const list = listFor(t, event.parent_tool_use_id)
      if (!list) return t
      const item = assistantItem(list, event.message_id, true)
      item.streaming = true
      const kind = event.type === 'text_delta' ? 'text' : 'thinking'
      const block = blockAt(item, event.block_index, () => ({ type: kind, text: '', final: false }))
      if (block.type === kind) block.text += event.text
      return { ...t }
    }

    case 'tool_use_start': {
      const list = listFor(t, event.parent_tool_use_id)
      if (!list || findTool(t.items, event.tool_use_id)) return t
      const item = assistantItem(list, event.message_id, true)
      blockAt(item, event.block_index, () => ({
        type: 'tool', id: event.tool_use_id, name: event.name,
        toolKind: event.tool_kind, input: {}, partialJson: '', children: [],
      }))
      return { ...t }
    }

    case 'tool_input_delta': {
      const tool = findTool(t.items, event.tool_use_id)
      if (!tool) return t
      tool.partialJson = (tool.partialJson ?? '') + event.partial_json
      return { ...t }
    }

    case 'tool_use_end': {
      const tool = findTool(t.items, event.tool_use_id)
      if (!tool) return t
      tool.input = event.input
      tool.partialJson = undefined
      return { ...t }
    }

    case 'tool_output_delta': {
      const tool = findTool(t.items, event.tool_use_id)
      if (!tool) return t
      tool.output = (tool.output ?? '') + event.text
      return { ...t }
    }

    case 'tool_result': {
      const tool = findTool(t.items, event.tool_use_id)
      if (!tool) return t
      tool.result = { content: event.content, isError: event.is_error }
      return { ...t }
    }

    case 'context_boundary': {
      t.items.push({ kind: 'note', id: localID(), label: '', contextBoundary: true, tokensBefore: event.tokens_before })
      return { ...t }
    }

    case 'notice': {
      t.items.push({ kind: 'note', id: localID(), label: event.message })
      return { ...t }
    }

    case 'error': {
      t.items.push({ kind: 'alert', id: localID(), code: event.code, message: event.message, fatal: event.fatal })
      return { ...t }
    }

    case 'turn_finished': {
      let changed = false
      const stop = (items: Item[]) => {
        for (const item of items) {
          if (item.kind !== 'assistant') continue
          if (item.streaming) {
            item.streaming = false
            changed = true
          }
          for (const block of item.blocks) if (block.type === 'tool') stop(block.children)
        }
      }
      stop(t.items)
      return changed ? { ...t } : t
    }

    default:
      return t
  }
}

/**
 * Fold a page of older history in front of what is already on screen. The page
 * is read after the stream is open, so anything it repeats is dropped.
 */
export function prependHistory(t: Transcript, entries: TranscriptEntry[]): Transcript {
  const older = emptyTranscript()
  for (const entry of entries) applyEvent(older, entry)
  const fresh = older.items.filter((item) => !(item.kind !== 'note' && item.kind !== 'alert' && t.seen.has(item.id)))
  for (const id of older.seen) t.seen.add(id)
  return { items: [...fresh, ...t.items], seen: t.seen }
}
