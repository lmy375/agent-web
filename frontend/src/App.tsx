import { LanguageSwitcher } from '@/components/LanguageSwitcher'
import { localeTag, type DisplayError } from '@/i18n/core'
import { useI18n } from '@/i18n'
import { ArrowUpRight, Code2, PanelLeftOpen, Plus } from 'lucide-react'
import { NewThread } from '@/components/NewThread'
import { useEffect, useState } from 'react'
import { BrowserRouter, Navigate, Route, Routes, useMatch } from 'react-router-dom'
import { useWorkspace } from '@/store/workspace'
import { Sidebar } from '@/components/Sidebar'
import { ThreadView } from '@/components/ThreadView'
import { SignIn } from '@/components/SignIn'
import { Button } from '@/components/ui/button'

export default function App() {
  const { locale } = useI18n()
  useEffect(() => { document.documentElement.lang = localeTag[locale] }, [locale])
  const { auth, unreachable, checkAuth } = useWorkspace()
  useEffect(() => {
    void checkAuth()
  }, [checkAuth])

  if (unreachable) return <Unreachable message={unreachable} onRetry={checkAuth} />
  // Nothing renders until the answer is in, so the workspace never flashes
  // behind a sign-in screen that is about to replace it.
  if (!auth) return null
  if (auth.password_required && !auth.signed_in) return <SignIn />
  return <Workspace />
}

/** The server is down or still starting. A blank page would say nothing at all,
 *  and this is the one screen that cannot rely on anything having loaded. */
function Unreachable({ message, onRetry }: { message: DisplayError; onRetry: () => Promise<void> }) {
  const { t, formatError } = useI18n()
  const [retrying, setRetrying] = useState(false)
  return (
    <div className="relative flex h-full items-center justify-center px-6">
      <LanguageSwitcher className="absolute right-4 top-4" />
      <div className="max-w-sm text-center">
        <h1 className="text-[0.9375rem] font-semibold tracking-tight">agent-web</h1>
        <p className="mt-1.5 text-[0.875rem] leading-relaxed text-ink-soft">
          {t('serverDown', { command: 'go run ./cmd/agentweb', directory: 'backend/' })}
        </p>
        <p className="mt-1 font-mono text-[0.6875rem] text-ink-faint">{formatError(message)}</p>
        <Button
          className="mt-3"
          disabled={retrying}
          onClick={async () => {
            setRetrying(true)
            await onRetry()
            setRetrying(false)
          }}
        >{t('retry')}</Button>
      </div>
    </div>
  )
}

function Workspace() {
  const { load, watchDirectory } = useWorkspace()
  useEffect(() => {
    void load()
    return watchDirectory()
  }, [load, watchDirectory])

  return (
    <BrowserRouter>
      <Panes />
    </BrowserRouter>
  )
}

/** One pane at a time on a narrow screen: the directory, or the thread. */
function Panes() {
  const { t } = useI18n()
  const [sidebarOpen, setSidebarOpen] = useState(true)
  const onThread = useMatch('/t/:id') !== null
  return (
    <div className="flex h-full bg-surface">
      <Sidebar onCollapse={() => setSidebarOpen(false)} className={onThread ? (sidebarOpen ? 'hidden md:flex' : 'hidden') : (sidebarOpen ? 'flex' : 'flex md:hidden')} />
      {!sidebarOpen && <Button size="icon" variant="quiet" className="fixed left-4 top-4 z-10 hidden md:inline-flex" onClick={() => setSidebarOpen(true)} aria-label={t('expandSidebar')} title={t('expandSidebar')}><PanelLeftOpen size={18} /></Button>}
      <Routes>
        <Route path="/" element={<Start />} />
        <Route path="/t/:id" element={<ThreadView sidebarOpen={sidebarOpen} />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </div>
  )
}

function Start() {
  const { t, formatError } = useI18n()
  const [creating, setCreating] = useState(false)
  const { agents, error, load } = useWorkspace()
  const [retrying, setRetrying] = useState(false)
  const ready = agents.filter((a) => !a.runtime.unavailable_reason)

  if (error) {
    return (
      <main className="hidden flex-1 flex-col items-center justify-center gap-3 px-6 text-center md:flex">
        <p className="max-w-sm text-[0.875rem] text-ink-soft">{formatError(error)}</p>
        <Button
          disabled={retrying}
          onClick={async () => {
            setRetrying(true)
            await load()
            setRetrying(false)
          }}
        >{t('retry')}</Button>
      </main>
    )
  }

  return (
    <main className="relative hidden flex-1 flex-col items-center justify-center px-8 md:flex">
      <div className="w-full max-w-lg pb-16">
        <Code2 size={36} strokeWidth={1.3} className="mb-6 text-ink-soft" />
        <p className="mb-3 text-xs font-medium tracking-[0.15em] text-ink-faint uppercase">{t('workspaceEyebrow')}</p>
        <h1 className="text-[32px] font-medium tracking-[-0.035em]">{t('startTitle')}</h1>
        <p className="mt-4 max-w-md text-[15px] leading-7 text-ink-soft">
          {ready.length > 0
            ? t('startHint')
            : t('noAgentHint')}
        </p>
        <Button className="mt-7 h-11 rounded-xl px-4" onClick={() => setCreating(true)}><Plus size={17} />{t('newThread')}<ArrowUpRight size={15} className="ml-5 text-ink-faint" /></Button>
        <div className="mt-10 flex flex-wrap gap-4 text-xs text-ink-faint">{ready.map((a) => <span key={a.kind} className="flex items-center gap-1.5"><span className="h-1 w-1 rounded-full bg-ink-faint" />{a.label}</span>)}</div>
      </div>
      <p className="absolute bottom-6 text-xs text-ink-faint">{t('workspaceTagline')}</p>
      {creating && <NewThread onClose={() => setCreating(false)} />}
    </main>
  )
}
