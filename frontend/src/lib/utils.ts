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
