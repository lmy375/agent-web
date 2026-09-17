import type { DisplayError } from '@/i18n/core'
import { useI18n } from '@/i18n'
import { ArrowLeft } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useThread } from '@/store/thread'
import { useWorkspace } from '@/store/workspace'
import { Transcript } from './Transcript'
import { Composer } from './Composer'
import { InteractionPanel } from './dialogs/InteractionPanel'
import { AgentMark } from './AgentMark'
import { Button } from './ui/button'
import { baseName, cn, homeRelative } from '@/lib/utils'

export function ThreadView({ sidebarOpen }: { sidebarOpen: boolean }) {
  const { t, formatError } = useI18n()
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const { config, descriptor, rename, threads } = useWorkspace()
  const thread = useThread()
  const open = useThread((s) => s.open)
  const [renaming, setRenaming] = useState(false)

  useEffect(() => open(id), [id, open])

  // The directory row is the freshest source of run state, since it also
  // arrives while this thread's own stream is reconnecting.
  const summary = threads.find((t) => t.thread_id === id) ?? thread.summary
  const agent = summary ? descriptor(summary.agent_kind) : undefined
  const busy = summary?.run_state === 'running' || summary?.run_state === 'starting'

  if (thread.error && !summary) {
    return <Missing message={thread.error} onBack={() => navigate('/')} />
  }
  if (!summary) return <div className="flex-1" />

  return (
    <main className="flex min-w-0 flex-1 flex-col bg-surface">
      <header className={cn('flex h-16 shrink-0 items-center gap-3 px-5 md:px-7', !sidebarOpen && 'md:pl-16')}>
        <Link to="/" className="-ml-1 px-1 text-ink-faint hover:text-ink md:hidden" aria-label={t('backToThreads')}>
          <ArrowLeft size={19} />
        </Link>
        <AgentMark kind={summary.agent_kind} label={agent?.label} />
        {renaming ? (
          <input
            autoFocus
            defaultValue={summary.title ?? ''}
            placeholder={t('nameThread')}
            onBlur={(e) => {
              const value = e.target.value.trim()
              if (value && value !== summary.title) void rename(id, value)
              setRenaming(false)
            }}
            onKeyDown={(e) => {
              if (e.key === 'Enter') e.currentTarget.blur()
              if (e.key === 'Escape') setRenaming(false)
            }}
            className="min-w-0 flex-1 rounded-[4px] bg-sunken px-1.5 py-0.5 text-[0.875rem] focus-visible:outline-none"
          />
        ) : (
          <button onClick={() => setRenaming(true)} className="min-w-0 truncate text-[15px] font-medium hover:underline" title={t('rename')}>
            {summary.title ?? <span className="text-ink-soft">{t('untitled')}</span>}
          </button>
        )}
        <span className="hidden max-w-48 shrink truncate rounded-md bg-sunken px-2 py-1 text-xs text-ink-soft lg:block" title={homeRelative(summary.cwd, config?.home_dir ?? null)}>
          {baseName(summary.cwd)}
        </span>
        <div className="flex-1" />
      </header>

      <Transcript
        key={`transcript:${id}`}
        active={busy}
        items={thread.transcript.items}
        olderCursor={thread.olderCursor}
        onLoadOlder={() => void thread.loadOlder()}
        footer={<InteractionPanel requests={thread.pending} onRespond={(request, decision) => void thread.respond(request, decision)} />}
      />

      {thread.error && (
        <div className="border-t border-failed/30 bg-failed/[0.08] px-5 py-1.5">
          <div className="mx-auto flex w-full max-w-[960px] items-center justify-between gap-3 text-xs text-failed">
            <span>{formatError(thread.error)}</span>
            <button onClick={thread.clearError} className="shrink-0 opacity-70 hover:opacity-100">{t('dismiss')}</button>
          </div>
        </div>
      )}

      <Composer
        key={`composer:${id}`}
        thread={summary}
        descriptor={agent}
        busy={busy}
        onPrompt={(text, images) => void thread.prompt(text, images)}
        onSteer={(text) => void thread.send({ type: 'steer', text })}
        onInterrupt={() => void thread.send({ type: 'interrupt' })}
        onOptions={(options) => void thread.send({ type: 'set_options', options })}
        usage={thread.contextUsage}
        lastTurn={thread.lastTurn}
      />
    </main>
  )
}

function Missing({ message, onBack }: { message: DisplayError; onBack: () => void }) {
  const { t, formatError } = useI18n()
  return (
    <main className="flex flex-1 flex-col items-center justify-center gap-3 px-6 text-center">
      <p className="text-[0.875rem] text-ink-soft">{formatError(message)}</p>
      <Button onClick={onBack}>{t('backToThreadList')}</Button>
    </main>
  )
}
