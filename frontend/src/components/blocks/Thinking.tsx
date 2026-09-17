import { ChevronRight, LoaderCircle } from 'lucide-react'
import { useI18n } from '@/i18n'
import { useId, useState } from 'react'
import { cn } from '@/lib/utils'
import { Markdown } from './Markdown'

/**
 * Reasoning is prose like any other, so it is rendered once, as markdown, and
 * collapsing only clips it to its first line -- a separate plain-text summary
 * would say the same thing twice. The toggle covers the whole block while it is
 * clipped and shrinks to the chevron once it is open, so expanded reasoning can
 * still be selected and its links followed.
 */
export function Thinking({ text, streaming }: { text: string; streaming: boolean }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const contentId = useId()
  if (!text.trim() && !streaming) return null
  return (
    <div className="relative my-3 flex min-w-0 items-start gap-2">
      {streaming && <LoaderCircle size={14} className="mt-1.5 shrink-0 animate-spin text-ink-faint" aria-hidden="true" />}
      <div
        id={contentId}
        className={cn(
          'min-w-0 flex-1 pr-6 text-[13px] leading-7',
          open ? 'text-ink-soft' : 'max-h-7 overflow-hidden text-ink-faint',
        )}
      >
        {text.trim() ? <Markdown>{text}</Markdown> : t('workingEllipsis')}
      </div>
      <button
        type="button"
        aria-expanded={open}
        aria-controls={contentId}
        aria-label={t(open ? 'hideReasoning' : 'reasoning')}
        onClick={() => setOpen((value) => !value)}
        className={cn(
          'absolute right-0 top-0 flex justify-end text-ink-faint hover:text-ink-soft',
          open ? 'h-7 w-6' : 'bottom-0 left-0',
        )}
      >
        <ChevronRight size={13} className={cn('mt-1.5 shrink-0 transition-transform', open && 'rotate-90')} aria-hidden="true" />
      </button>
    </div>
  )
}
