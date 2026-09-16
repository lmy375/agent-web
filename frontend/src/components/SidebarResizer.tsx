import { useEffect, useRef, useState } from 'react'
import type { KeyboardEvent, PointerEvent } from 'react'
import { useI18n } from '@/i18n'
import { SIDEBAR_WIDTH } from '@/lib/sidebarWidth'
import { cn } from '@/lib/utils'

export function SidebarResizer({ width, onResize, onCommit, className }: {
  width: number
  onResize: (width: number) => void
  onCommit: (width: number) => void
  className?: string
}) {
  const { t } = useI18n()
  const drag = useRef<{ pointerId: number; originX: number; originWidth: number } | null>(null)
  const [dragging, setDragging] = useState(false)

  useEffect(() => {
    if (!dragging) return
    document.body.classList.add('is-resizing')
    return () => document.body.classList.remove('is-resizing')
  }, [dragging])

  function widthAt(e: PointerEvent) {
    const d = drag.current
    return d ? d.originWidth + e.clientX - d.originX : width
  }

  function onPointerDown(e: PointerEvent<HTMLDivElement>) {
    if (e.button !== 0) return
    e.preventDefault()
    e.currentTarget.setPointerCapture(e.pointerId)
    // The drag starts from the rendered width, not the preference: the viewport
    // cap may hold the sidebar narrower than `width`, and a handle that has to
    // travel that gap first would feel stuck.
    const rendered = e.currentTarget.offsetParent?.getBoundingClientRect().width ?? width
    drag.current = { pointerId: e.pointerId, originX: e.clientX, originWidth: rendered }
    setDragging(true)
  }

  function onPointerMove(e: PointerEvent<HTMLDivElement>) {
    if (drag.current?.pointerId !== e.pointerId) return
    onResize(widthAt(e))
  }

  function onPointerEnd(e: PointerEvent<HTMLDivElement>) {
    if (drag.current?.pointerId !== e.pointerId) return
    const next = widthAt(e)
    drag.current = null
    setDragging(false)
    onCommit(next)
  }

  function onKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    const next = {
      ArrowLeft: width - SIDEBAR_WIDTH.step,
      ArrowRight: width + SIDEBAR_WIDTH.step,
      Home: SIDEBAR_WIDTH.min,
      End: SIDEBAR_WIDTH.max,
      Enter: SIDEBAR_WIDTH.default,
    }[e.key]
    if (next === undefined) return
    e.preventDefault()
    onCommit(next)
  }

  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={t('resizeSidebar')}
      aria-valuenow={width}
      aria-valuemin={SIDEBAR_WIDTH.min}
      aria-valuemax={SIDEBAR_WIDTH.max}
      tabIndex={0}
      title={t('resizeSidebar')}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerEnd}
      onPointerCancel={onPointerEnd}
      onDoubleClick={() => onCommit(SIDEBAR_WIDTH.default)}
      onKeyDown={onKeyDown}
      className={cn('group/resizer absolute inset-y-0 -right-1 z-10 w-2 cursor-col-resize touch-none select-none', className)}
    >
      <span
        aria-hidden="true"
        className={cn(
          'absolute inset-y-0 right-[3px] w-px bg-ink-faint/60 opacity-0 transition-opacity duration-150 group-hover/resizer:opacity-100 group-focus-visible/resizer:opacity-100',
          dragging && 'opacity-100',
        )}
      />
    </div>
  )
}
