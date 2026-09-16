import { relativeTime } from '@/i18n/core'
import { LanguageSwitcher } from './LanguageSwitcher'
import { useI18n } from '@/i18n'
import { useMemo, useState } from 'react'
import type { CSSProperties } from 'react'
import { NavLink } from 'react-router-dom'
import { ChevronRight, Code2, FolderOpen, LogOut, PanelLeftClose, Plus } from 'lucide-react'
import { useWorkspace } from '@/store/workspace'
import type { ThreadSummary } from '@/store/protocol'
import { AgentMark } from './AgentMark'
import { NewThread } from './NewThread'
import { SidebarResizer } from './SidebarResizer'
import { Button } from './ui/button'
import { useSidebarWidth } from '@/lib/sidebarWidth'
import { baseName, cn, homeRelative } from '@/lib/utils'

export function Sidebar({ className, onCollapse }: { className?: string; onCollapse: () => void }) {
  const { t } = useI18n()
  const { threads, agents, config, nextCursor, loadMore, auth, signOut } = useWorkspace()
  const [creating, setCreating] = useState<{ cwd?: string } | null>(null)
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const { width, setWidth, commit } = useSidebarWidth()
  const waiting = useMemo(() => threads.filter((t) => t.run_state === 'waiting_input'), [threads])
  const projects = useMemo(() => {
    const groups = new Map<string, ThreadSummary[]>()
    for (const thread of threads) {
      const group = groups.get(thread.cwd) ?? []
      group.push(thread)
      groups.set(thread.cwd, group)
    }
    return [...groups]
  }, [threads])

  function toggle(cwd: string) {
    setCollapsed((current) => {
      const next = new Set(current)
      if (next.has(cwd)) next.delete(cwd)
      else next.add(cwd)
      return next
    })
  }

  return (
    <aside style={{ '--sidebar-width': `${width}px` } as CSSProperties} className={cn('relative w-full shrink-0 flex-col border-r border-rule/70 bg-paper md:w-(--sidebar-width) md:max-w-[50vw]', className)}>
      <header className="flex h-16 shrink-0 items-center gap-2.5 px-5">
        <Code2 size={21} strokeWidth={1.6} />
        <span className="flex-1 text-[15px] font-semibold tracking-tight">agent-web</span>
        <Button size="icon" variant="quiet" className="hidden md:inline-flex" onClick={onCollapse} aria-label={t('collapseSidebar')} title={t('collapseSidebar')}>
          <PanelLeftClose size={18} strokeWidth={1.5} />
        </Button>
      </header>

      <button onClick={() => setCreating({})} className="mx-3 mb-6 flex items-center gap-3 rounded-lg px-3 py-2 text-left text-[14px] text-ink-soft transition-colors hover:bg-sunken hover:text-ink">
        <span className="flex h-6 w-6 items-center justify-center rounded-full bg-sunken"><Plus size={17} /></span>{t('newThread')}</button>

      <nav aria-label={t('threads')} className="min-h-0 flex-1 overflow-y-auto px-3 pb-5">
        {waiting.length > 0 && (
          <section className="mb-5 rounded-xl bg-waiting/[0.06] p-1">
            <div className="px-2 py-2 text-xs font-medium text-waiting">{t('waitingForYou', { count: waiting.length })}</div>
            {waiting.map((thread) => <ThreadRow key={thread.thread_id} thread={thread} />)}
          </section>
        )}
        {projects.map(([cwd, group]) => (
          <section key={cwd} className="mb-5">
            <div className="mb-1 flex items-center gap-1 px-2">
              <button onClick={() => toggle(cwd)} aria-expanded={!collapsed.has(cwd)} className="flex min-w-0 flex-1 items-center gap-1.5 py-1.5 text-left text-[12px] text-ink-faint hover:text-ink" title={homeRelative(cwd, config?.home_dir ?? null)}>
                <span className="truncate">{baseName(cwd)}</span>
                <ChevronRight size={13} className={cn('shrink-0 transition-transform', !collapsed.has(cwd) && 'rotate-90')} />
              </button>
              <Button size="icon" variant="quiet" className="h-6 w-6" onClick={() => setCreating({ cwd })} aria-label={t('newInProject', { project: baseName(cwd) })} title={t('newInThisProject')}><Plus size={15} /></Button>
            </div>
            {!collapsed.has(cwd) && group.map((thread) => <ThreadRow key={thread.thread_id} thread={thread} />)}
          </section>
        ))}
        {!threads.length && (
          <div className="px-3 py-6 text-[13px] leading-relaxed text-ink-faint">
            <FolderOpen size={22} strokeWidth={1.4} className="mb-3" />
            <p>{t('emptyProjects')}</p>
            <p className="mt-1">{t('emptyProjectsHint')}</p>
          </div>
        )}
        {nextCursor && <Button variant="quiet" size="sm" className="ml-6 mt-1" onClick={() => void loadMore()}>{t('showMore')}</Button>}
      </nav>

      <div className="flex justify-end px-3 pb-2"><LanguageSwitcher /></div>
      <footer className="flex shrink-0 items-center gap-3 border-t border-rule/70 px-5 py-4">
        <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-sunken"><Code2 size={16} strokeWidth={1.5} /></span>
        <div className="min-w-0 flex-1">
          <p className="text-[13px] text-ink-soft">{t('localWorkspace')}</p>
          <div className="mt-1 flex flex-wrap gap-1.5">
            {agents.map((agent) => <span key={agent.kind} title={agent.runtime.unavailable_reason ?? t('agentReady', { agent: agent.label })} className={cn('text-[10px] text-ink-faint', agent.runtime.unavailable_reason && 'line-through opacity-50')}>{agent.label}</span>)}
          </div>
        </div>
        {auth?.password_required && <Button size="icon" variant="quiet" onClick={() => void signOut()} aria-label={t('signOut')} title={t('signOut')}><LogOut size={16} /></Button>}
      </footer>
      <SidebarResizer className="hidden md:block" width={width} onResize={setWidth} onCommit={commit} />
      {creating && <NewThread initialCwd={creating.cwd} onClose={() => setCreating(null)} />}
    </aside>
  )
}

function ThreadRow({ thread }: { thread: ThreadSummary }) {
  const { t, locale } = useI18n()
  const running = thread.run_state === 'running' || thread.run_state === 'starting'
  return (
    <NavLink to={`/t/${encodeURIComponent(thread.thread_id)}`} title={`${thread.title ?? t('untitled')} · ${relativeTime(locale, Date.parse(thread.updated_at))}`} className={({ isActive }) => cn('group mb-0.5 flex min-w-0 items-center gap-3 rounded-lg px-3 py-2 text-ink-soft transition-colors hover:bg-sunken/70 hover:text-ink', isActive && 'bg-sunken text-ink')}>
      <span aria-label={t(`state.${thread.run_state}`)} className={cn('h-1.5 w-1.5 shrink-0 rounded-full border border-ink-faint/45', running && 'breathe border-running bg-running', thread.run_state === 'waiting_input' && 'border-waiting bg-waiting')} />
      <span className="min-w-0 flex-1 truncate text-[13px]">{thread.title ?? t('untitled')}</span>
      <AgentMark kind={thread.agent_kind} className="h-4 w-5 text-[8px] opacity-60 group-hover:opacity-100" />
    </NavLink>
  )
}
