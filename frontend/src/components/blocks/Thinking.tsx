import { ChevronRight, LoaderCircle } from 'lucide-react'
import { useI18n } from '@/i18n'
import { useId, useMemo, useState } from 'react'
import { plainText } from '@/lib/transcriptPresentation'
import { cn } from '@/lib/utils'
import { Markdown } from './Markdown'

export function Thinking({ text, streaming }: { text: string; streaming: boolean }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const contentId = useId()
  const preview = useMemo(() => plainText(text), [text])
  if (!text.trim() && !streaming) return null
  return (
    <div className="my-3 min-w-0">
      <button
        type="button"
        aria-expanded={open}
        aria-controls={contentId}
        onClick={() => setOpen((value) => !value)}
        className="flex w-full min-w-0 items-start gap-2 rounded-lg py-1.5 text-left text-[13px] leading-7 text-ink-faint hover:text-ink-soft"
      >
        {streaming && <LoaderCircle size={14} className="mt-1.5 shrink-0 animate-spin" aria-hidden="true" />}
        <span className="min-w-0 flex-1 truncate">{preview || t('workingEllipsis')}</span>
        <ChevronRight size={13} className={cn('mt-1.5 shrink-0 transition-transform', open && 'rotate-90')} aria-hidden="true" />
      </button>
      {/* Reasoning is markdown like any other prose, and it sits outside the
          button so that a link in it stays clickable and a quote stays
          selectable. */}
      {open && text.trim() && (
        <div id={contentId} className="min-w-0 pb-1 text-[13px] leading-7 text-ink-soft">
          <Markdown>{text}</Markdown>
        </div>
      )}
    </div>
  )
}
