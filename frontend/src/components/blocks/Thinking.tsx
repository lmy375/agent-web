import { ChevronRight, LoaderCircle } from 'lucide-react'
import { useI18n } from '@/i18n'
import { useId, useState } from 'react'
import { cn } from '@/lib/utils'

export function Thinking({ text, streaming }: { text: string; streaming: boolean }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const contentId = useId()
  if (!text.trim() && !streaming) return null
  return (
    <button
      type="button"
      aria-expanded={open}
      aria-controls={contentId}
      onClick={() => setOpen((value) => !value)}
      className="my-3 flex w-full min-w-0 items-start gap-2 rounded-lg py-1.5 text-left text-[13px] leading-7 text-ink-faint hover:text-ink-soft"
    >
      {streaming && <LoaderCircle size={14} className="mt-1.5 shrink-0 animate-spin" aria-hidden="true" />}
      <span id={contentId} className={cn('min-w-0 flex-1', open ? 'whitespace-pre-wrap break-words text-ink-soft' : 'truncate')}>
        {text || t('workingEllipsis')}
      </span>
      <ChevronRight size={13} className={cn('mt-1.5 shrink-0 transition-transform', open && 'rotate-90')} aria-hidden="true" />
    </button>
  )
}
