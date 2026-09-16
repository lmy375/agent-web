import { translate, type Locale } from '../i18n/core.ts'
import type { ToolKind } from '@/store/protocol'
import { truncate } from './utils.ts'

const text = (v: unknown) => (typeof v === 'string' ? v : v == null ? '' : JSON.stringify(v))

/** The first argument that carries meaning, tried in the order the three
 *  harnesses happen to name it. */
function firstOf(input: Record<string, unknown>, ...names: string[]): string {
  for (const name of names) {
    const value = text(input[name])
    if (value) return value
  }
  return ''
}

/**
 * The one line a tool call is worth before it is expanded. It reads the kind the
 * protocol assigned, so a tool no build has ever seen still summarizes usefully.
 */
export function toolSummary(kind: ToolKind, name: string, input: Record<string, unknown>, locale: Locale = 'en'): string {
  switch (kind) {
    case 'shell':
      return firstOf(input, 'command', 'description')
    case 'file_read':
    case 'file_write':
    case 'file_edit':
      return firstOf(input, 'file_path', 'filePath', 'path', 'notebook_path', 'filename')
    case 'search': {
      const pattern = firstOf(input, 'pattern', 'query', 'q')
      const where = firstOf(input, 'path', 'glob', 'include')
      return where ? translate(locale, 'searchIn', { pattern, where }) : pattern
    }
    case 'web':
      return firstOf(input, 'url', 'query')
    case 'subagent':
      return firstOf(input, 'description', 'prompt') || name
    case 'todo': {
      const todos = input.todos
      return Array.isArray(todos) ? translate(locale, 'todoItems', { count: todos.length }) : ''
    }
    default: {
      const single = firstOf(input, 'command', 'query', 'path', 'file_path', 'url', 'text')
      return single || truncate(JSON.stringify(input ?? {}), 110)
    }
  }
}

/** Descriptions make activity readable; approval panels still use toolSummary
 *  so the actual command being authorized is always visible. */
export function toolPreview(kind: ToolKind, name: string, input: Record<string, unknown>, locale: Locale = 'en'): string {
  if (kind === 'shell' && typeof input.description === 'string' && input.description.trim()) {
    return input.description.trim()
  }
  return toolSummary(kind, name, input, locale)
}

/** Plain text of a tool result, for the collapsed preview. */
export function resultText(content: { type: string; text?: string }[]): string {
  return content
    .filter((c) => c.type === 'text')
    .map((c) => c.text ?? '')
    .join('\n')
    .trim()
}
