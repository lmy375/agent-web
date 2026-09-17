import { Bot, ChevronRight, Network, Square, Terminal, Workflow, Wrench } from 'lucide-react'
import { useI18n } from '@/i18n'
import { useEffect, useId, useRef, useState } from 'react'
import type { BackgroundTask } from '@/store/protocol'
import { Button } from './ui/button'
import { cn } from '@/lib/utils'

/**
 * The work a harness carries between turns. A background task outlives the turn
 * that started it and the conversation resumes on its own when one reports
 * back, so without this the thread looks finished while it is not. Closed it is
 * one line saying how much is in flight; the tasks and their stop controls are
 * behind the disclosure, where a long-running thread's list cannot crowd out
 * the conversation.
 */
const icons: Record<string, typeof Terminal> = {
  local_bash: Terminal,
  local_agent: Bot,
  remote_agent: Bot,
  in_process_teammate: Bot,
  local_workflow: Workflow,
  mcp_task: Wrench,
}

export function BackgroundTasks({
  tasks, onStop,
}: {
  tasks: BackgroundTask[]
  onStop: (taskID: string) => void
}) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const panelId = useId()
  const section = useRef<HTMLElement>(null)
  // Opening the list happens inside this component, so the transcript's own
  // follow-the-bottom effect does not run and what was just revealed can sit
  // below the fold.
  useEffect(() => {
    if (open) section.current?.scrollIntoView({ block: 'nearest' })
  }, [open])
  if (!tasks.length) return null
  return (
    <section ref={section} className="my-5 min-w-0">
      <button
        type="button"
        aria-expanded={open}
        aria-controls={panelId}
        onClick={() => setOpen((value) => !value)}
        className="group flex w-full min-w-0 items-center gap-2 rounded-lg px-1 py-2 text-left text-[13px] text-ink-soft hover:bg-paper hover:text-ink"
      >
        <span className="breathe ml-0.5 block h-1.5 w-1.5 shrink-0 rounded-full bg-running" aria-hidden="true" />
        <span className="min-w-0 flex-1 truncate">{t('backgroundTasks', { count: tasks.length })}</span>
        <ChevronRight size={13} className={cn('shrink-0 text-ink-faint transition-transform', open && 'rotate-90')} aria-hidden="true" />
      </button>

      <ul id={panelId} hidden={!open} className="ml-2 min-w-0 border-l border-rule py-1 pl-3 md:ml-3 md:pl-4">
        {tasks.map((task) => {
          const Icon = icons[task.task_type] ?? Network
          return (
            <li key={task.task_id} className="flex min-w-0 items-center gap-2 py-1">
              <Icon size={15} strokeWidth={1.6} className="shrink-0 text-ink-faint" aria-hidden="true" />
              <span className="min-w-0 flex-1 truncate text-[13px] text-ink-soft" title={task.description}>
                {task.description}
              </span>
              <Button
                size="sm"
                variant="quiet"
                onClick={() => onStop(task.task_id)}
                title={t('stopTask')}
                aria-label={t('stopTask')}
              >
                <Square size={11} strokeWidth={2.4} aria-hidden="true" />
                {t('stop')}
              </Button>
            </li>
          )
        })}
      </ul>
    </section>
  )
}
