import { ChevronRight, FilePenLine, FileSearch, Globe, ListTodo, Network, Search, Terminal, Wrench } from 'lucide-react'
import { useI18n } from '@/i18n'
import { useId, useState } from 'react'
import type { ToolKind } from '@/store/protocol'
import type { Item, ToolBlock } from '@/store/transcript'
import { resultText, toolPreview } from '@/lib/toolSummary'
import { imageSrc } from '@/lib/images'
import { cn } from '@/lib/utils'

const icons: Record<ToolKind, typeof Terminal> = {
  shell: Terminal,
  file_edit: FilePenLine,
  file_write: FilePenLine,
  file_read: FileSearch,
  search: Search,
  todo: ListTodo,
  mcp: Wrench,
  subagent: Network,
  web: Globe,
  other: Wrench,
}

export function ToolCard({ block, active, renderChildren }: {
  block: ToolBlock
  active: boolean
  renderChildren: (items: Item[], active: boolean) => React.ReactNode
}) {
  const { t, locale } = useI18n()
  const [open, setOpen] = useState(false)
  const panelId = useId()
  const input = block.partialJson ? tryParse(block.partialJson) ?? block.input : block.input
  const summary = toolPreview(block.toolKind, block.name, input, locale).replace(/\s+/g, ' ')
  const done = block.result !== undefined
  const failed = block.result?.isError === true
  const running = !done && active
  const output = block.result ? resultText(block.result.content) : block.output ?? ''
  const images = block.result?.content.filter((c) => c.type === 'image') ?? []
  const Icon = icons[block.toolKind] ?? Wrench
  const status = failed ? t('failed') : done ? t('ok') : running ? t('running') : t('activity.noResult')
  const label = block.toolKind === 'shell'
    ? t(failed ? 'activity.commandFailed' : done ? 'activity.ranCommand' : running ? 'activity.runningCommand' : 'activity.command')
    : block.name

  return (
    <div className="min-w-0">
      <button
        type="button"
        aria-expanded={open}
        aria-controls={panelId}
        onClick={() => setOpen((value) => !value)}
        className="group flex w-full min-w-0 items-center gap-2 rounded-lg px-1 py-2 text-left text-[13px] text-ink-soft hover:bg-paper hover:text-ink"
      >
        <Icon size={16} strokeWidth={1.6} className="shrink-0 text-ink-faint" aria-hidden="true" />
        <span className="min-w-0 flex-1 truncate" title={summary || block.name}>
          <span>{label}</span>{summary && <span className="ml-1.5">{summary}</span>}
        </span>
        <span className={cn('shrink-0 text-[11px]', failed ? 'text-failed' : running ? 'text-running breathe' : 'text-ink-faint')}>
          {status}
        </span>
        <ChevronRight size={13} className={cn('shrink-0 text-ink-faint transition-transform', open && 'rotate-90')} aria-hidden="true" />
      </button>

      <div id={panelId} hidden={!open} className="ml-2 min-w-0 border-l border-rule py-2 pl-3 md:ml-3 md:pl-4">
        {open && <>
          <div className="mb-2 flex flex-wrap items-center gap-2 text-xs text-ink-faint">
            <span className="font-mono text-ink-soft">{block.name}</span>
            <span>{t('activity.input')}</span>
          </div>
          <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-all rounded-xl border border-rule bg-paper p-3 font-mono text-xs leading-6">
            {block.partialJson ?? JSON.stringify(input, null, 2)}
          </pre>
          <div className="mb-2 mt-4 text-xs text-ink-faint">{t('activity.output')}</div>
          {output ? (
            <pre className={cn('max-h-96 overflow-auto whitespace-pre-wrap break-all rounded-xl border border-rule bg-paper p-3 font-mono text-xs leading-6', failed && 'text-failed')}>
              {output}
            </pre>
          ) : !images.length && <p className="text-xs text-ink-faint">{running ? t('activity.waitingResult') : done ? t('activity.emptyOutput') : t('activity.noResult')}</p>}
          {images.map((image, i) => (
            <img key={i} src={imageSrc(image)} alt={t('activity.resultImage', { count: i + 1 })} className="my-2 max-h-72 max-w-full rounded-lg border border-rule object-contain" />
          ))}
          {block.children.length > 0 && (
            <div className="mt-4 min-w-0">
              <p className="text-xs text-ink-faint">{t('activity.subtask')}</p>
              {renderChildren(block.children, active && !done)}
            </div>
          )}
        </>}
      </div>
    </div>
  )
}

function tryParse(partial: string): Record<string, unknown> | null {
  try {
    const value: unknown = JSON.parse(partial)
    return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null
  } catch {
    return null
  }
}
