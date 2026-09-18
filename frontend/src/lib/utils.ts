import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function stripSystemReminders(text: string): string {
  return text.replace(/<system-reminder>[\s\S]*?<\/system-reminder>/g, '').trim()
}

export function truncate(s: string, n = 80): string {
  return s.length > n ? s.slice(0, n - 1) + '…' : s
}

/** Token counts, short enough to sit in a status line: 940, 12k, 181.6k, 2.4M. */
export function compactTokens(n: number): string {
  if (n < 1000) return String(n)
  const [value, unit] = n < 1_000_000 ? [n / 1000, 'k'] : [n / 1_000_000, 'M']
  return String(Math.round(value * 10) / 10) + unit
}

/**
 * A harness spells its own knobs, so they arrive as parameter names --
 * `acceptEdits`, `on-request`, `permission-mode` -- which read as typos in a row
 * of controls. Only the shown label is rewritten; the value handed back is still
 * the harness's own word, exactly as it was declared.
 */
export function displayGroup<T extends { label: string; options: { label: string }[] }>(group: T): T {
  return {
    ...group,
    label: sentence(group.label),
    options: group.options.map((option) => ({ ...option, label: sentence(option.label) })),
  }
}

/** `acceptEdits` -> `Accept edits`, `danger-full-access` -> `Danger full access`. */
function sentence(label: string): string {
  const words = label
    .replace(/[-_]+/g, ' ')
    .replace(/([a-z\d])([A-Z])/g, '$1 $2')
    .trim()
    .toLowerCase()
  return words.charAt(0).toUpperCase() + words.slice(1)
}

/** Last segment of a POSIX path; "/" for the filesystem root. */
export function baseName(path: string): string {
  const trimmed = path.replace(/\/+$/, '')
  return trimmed.slice(trimmed.lastIndexOf('/') + 1) || '/'
}

/** `/Users/me/code` -> `~/code`, so paths stay readable in a narrow sidebar. */
export function homeRelative(path: string, home?: string | null): string {
  if (!home) return path
  if (path === home) return '~'
  return path.startsWith(home + '/') ? '~' + path.slice(home.length) : path
}

/** localStorage that never throws. It is unavailable in private mode and some
 *  embedded webviews, and every value here is a convenience, never real state. */
export const storage = {
  get(key: string): string | null {
    try {
      return localStorage.getItem(key)
    } catch {
      return null
    }
  },
  set(key: string, value: string) {
    try {
      localStorage.setItem(key, value)
    } catch {
      /* the preference just will not survive a reload */
    }
  },
}
