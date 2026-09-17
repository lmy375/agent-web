import type { Item, ToolBlock } from '../store/transcript'

export type ActivityEntry =
  | { kind: 'thinking'; id: string; text: string; streaming: boolean }
  | { kind: 'tool'; id: string; block: ToolBlock }

export type TranscriptRow =
  | { kind: 'item'; id: string; item: Exclude<Item, { kind: 'assistant' }> }
  | { kind: 'text'; id: string; text: string }
  | { kind: 'activity'; id: string; entries: ActivityEntry[] }

/** Message boundaries are a transport detail. Only visible prose or a user/event
 *  boundary should separate consecutive tool calls in the conversation. */
export function presentTranscript(items: Item[]): TranscriptRow[] {
  const rows: TranscriptRow[] = []
  function activity(entry: ActivityEntry) {
    const last = rows.at(-1)
    if (last?.kind === 'activity') last.entries.push(entry)
    else rows.push({ kind: 'activity', id: entry.id, entries: [entry] })
  }

  for (const item of items) {
    if (item.kind !== 'assistant') {
      rows.push({ kind: 'item', id: item.id, item })
      continue
    }
    item.blocks.forEach((block, index) => {
      const id = `${item.id}:${index}`
      if (block.type === 'text') {
        if (block.text.trim()) rows.push({ kind: 'text', id, text: block.text })
      } else if (block.type === 'thinking') {
        if (block.text.trim() || item.streaming) {
          activity({ kind: 'thinking', id, text: block.text, streaming: item.streaming && !block.final })
        }
      } else {
        activity({ kind: 'tool', id: `tool:${block.id}`, block })
      }
    })
    if (item.streaming && item.blocks.length === 0) {
      activity({ kind: 'thinking', id: `${item.id}:0`, text: '', streaming: true })
    }
  }
  return rows
}

/** The one line a collapsed group shows: the most recent thought, which while
 *  the group is running reads as a live status line. Only the newest line of it
 *  is taken -- the rest is what expanding the group is for. */
export function latestThought(entries: ActivityEntry[]): string {
  for (let i = entries.length - 1; i >= 0; i--) {
    const entry = entries[i]
    if (entry.kind !== 'thinking') continue
    const line = entry.text.split('\n').map((part) => part.trim()).filter(Boolean).at(-1)
    if (line) return plainText(line)
  }
  return ''
}

/**
 * Reasoning is markdown, and a one-line preview has no room for its syntax.
 * Emphasis with a single `*` or `_` is left alone: identifiers carry more
 * underscores than a thought carries italics.
 */
function plainText(markdown: string): string {
  return markdown
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/^\s{0,3}(?:#{1,6}|>|[-*+])\s+/gm, '')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/(\*\*|__)([\s\S]+?)\1/g, '$2')
    .replace(/`+([^`]+)`+/g, '$1')
    .replace(/\s+/g, ' ')
    .trim()
}

export function activityStats(entries: ActivityEntry[], active = false) {
  const stats = { tools: 0, commands: 0, failed: 0, pending: 0, running: false }
  function visit(tool: ToolBlock, mayRun: boolean) {
    stats.tools++
    if (tool.toolKind === 'shell') stats.commands++
    if (tool.result?.isError) stats.failed++
    if (!tool.result) {
      stats.pending++
      if (mayRun) stats.running = true
    }
    for (const child of tool.children) {
      if (child.kind === 'alert') stats.failed++
      if (child.kind === 'assistant') {
        for (const block of child.blocks) {
          if (block.type === 'tool') visit(block, mayRun && !tool.result)
          else if (mayRun && !tool.result && block.type === 'thinking' && !block.final && child.streaming) stats.running = true
        }
      }
    }
  }
  for (const entry of entries) {
    if (entry.kind === 'tool') visit(entry.block, active)
    else if (active && entry.streaming) stats.running = true
  }
  return stats
}
