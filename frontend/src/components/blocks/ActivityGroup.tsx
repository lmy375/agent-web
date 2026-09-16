import { ChevronRight, LoaderCircle, Terminal, Wrench } from 'lucide-react'
import { useId, useState } from 'react'
import { useI18n } from '@/i18n'
import { activityStats, type ActivityEntry } from '@/lib/transcriptPresentation'
import { cn } from '@/lib/utils'
import type { Item } from '@/store/transcript'
import { Thinking } from './Thinking'
import { ToolCard } from './ToolCard'

export function ActivityGroup({ entries, active, renderChildren }: {
  entries: ActivityEntry[]
  active: boolean
  renderChildren: (items: Item[], active: boolean) => React.ReactNode
}) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const panelId = useId()
  const stats = activityStats(entries, active)
  const running = stats.running
  const onlyCommands = stats.commands === stats.tools
  const Icon = running ? LoaderCircle : onlyCommands ? Terminal : Wrench

  if (!stats.tools) {
    return <Thinking text={entries.map((entry) => entry.kind === 'thinking' ? entry.text : '').join('\n\n')} streaming={running} />
  }

  const summaryKey = onlyCommands
    ? running ? 'activity.runningCommands' : 'activity.commands'
    : running ? 'activity.runningTools' : 'activity.tools'
  const summary = t(stats.tools === 1 ? `${summaryKey}One` : summaryKey, { count: stats.tools })

  return (
    <section className="my-5 min-w-0">
      <button
        type="button"
        aria-expanded={open}
        aria-controls={panelId}
        onClick={() => setOpen((value) => !value)}
        className="flex w-full min-w-0 flex-wrap items-center gap-x-2 gap-y-1 rounded-lg py-1.5 text-left text-[14px] text-ink-soft hover:text-ink"
      >
        <Icon size={17} strokeWidth={1.6} className={cn('shrink-0', running && 'animate-spin text-running')} aria-hidden="true" />
        <span>{summary}</span>
        {stats.failed > 0 && <span className="text-xs text-failed">{t('activity.failures', { count: stats.failed })}</span>}
        {!active && stats.pending > 0 && <span className="text-xs text-ink-faint">{t('activity.incomplete', { count: stats.pending })}</span>}
        <ChevronRight size={14} className={cn('shrink-0 transition-transform', open && 'rotate-90')} aria-hidden="true" />
      </button>
      <div id={panelId} hidden={!open} className="mt-1 space-y-1 border-l border-rule pl-3 md:pl-4">
        {open && entries.map((entry) => entry.kind === 'thinking'
          ? <Thinking key={entry.id} text={entry.text} streaming={active && entry.streaming} />
          : <ToolCard key={entry.id} block={entry.block} active={active} renderChildren={renderChildren} />)}
      </div>
    </section>
  )
}
