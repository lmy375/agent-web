import { useCallback, useState } from 'react'
import { storage } from '@/lib/utils'

const STORAGE_KEY = 'agent-web.sidebar-width'
export const SIDEBAR_WIDTH = { min: 220, max: 520, default: 280, step: 16 } as const

/** The viewport is not consulted here: a width saved on a wide monitor is
 *  capped by CSS on a laptop screen and comes back intact when the window grows. */
function clamp(width: number): number {
  return Math.round(Math.min(Math.max(width, SIDEBAR_WIDTH.min), SIDEBAR_WIDTH.max))
}

/** `setWidth` follows the pointer; `commit` is what survives a reload. */
export function useSidebarWidth() {
  const [width, setState] = useState(() => {
    const saved = Number(storage.get(STORAGE_KEY))
    return clamp(saved > 0 ? saved : SIDEBAR_WIDTH.default)
  })
  const setWidth = useCallback((next: number) => setState(clamp(next)), [])
  const commit = useCallback((next: number) => {
    const clamped = clamp(next)
    setState(clamped)
    storage.set(STORAGE_KEY, String(clamped))
  }, [])
  return { width, setWidth, commit }
}
