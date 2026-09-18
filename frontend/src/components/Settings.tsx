import { LocalizedError, type DisplayError } from '@/i18n/core'
import { useI18n } from '@/i18n'
import { ArrowLeft, LogOut } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { useWorkspace } from '@/store/workspace'
import type { AgentDescriptor, SystemPromptMode, WorkspaceSettings } from '@/store/protocol'
import { AgentMark } from './AgentMark'
import { Button } from './ui/button'
import { Picker, Textarea } from './ui/field'
import { cn } from '@/lib/utils'

/** The same cap the server holds, so the count runs out here rather than in a
 *  rejected save. Both count bytes: a prompt reaches two of the harnesses as a
 *  command-line argument, which is what the limit is really about. */
const maxSystemPromptBytes = 16 << 10

/** The page has its own address, so a reload can land on it before the
 *  workspace has loaded. The draft waits for the server's answer rather than
 *  starting from defaults, which would show an empty prompt over a workspace
 *  that has one. */
export function Settings({ sidebarOpen }: { sidebarOpen: boolean }) {
  const settings = useWorkspace((s) => s.settings)
  if (!settings) return <div className="flex-1" />
  return <SettingsPage settings={settings} sidebarOpen={sidebarOpen} />
}

/** What is true of the whole workspace rather than of one thread: the language
 *  it speaks, the system prompt its threads are created with, and what this
 *  machine can run. The prompt is a draft until it is saved, so the language
 *  beside it waits for the same button rather than applying under the typing. */
function SettingsPage({ settings, sidebarOpen }: { settings: WorkspaceSettings; sidebarOpen: boolean }) {
  const { t, locale, formatError } = useI18n()
  const { agents, auth, saveSettings, signOut } = useWorkspace()
  const navigate = useNavigate()
  /** Where the page was opened from, so leaving it returns the thread that was
   *  on screen rather than the start page. */
  const from = (useLocation().state as { from?: string } | null)?.from ?? '/'
  const [draft, setDraft] = useState<WorkspaceSettings>(settings)
  const [problem, setProblem] = useState<DisplayError | null>(null)
  const [busy, setBusy] = useState(false)

  const size = new TextEncoder().encode(draft.system_prompt.text).length
  const tooLong = size > maxSystemPromptBytes

  async function save() {
    setBusy(true)
    try {
      await saveSettings({ ...draft, locale: draft.locale || locale })
      navigate(from)
    } catch (error) {
      setProblem(error instanceof Error ? error : new LocalizedError('error.settings_invalid'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="flex min-w-0 flex-1 flex-col bg-surface">
      <header className={cn('flex h-16 shrink-0 items-center gap-3 px-5 md:px-7', !sidebarOpen && 'md:pl-16')}>
        <Link to={from} className="-ml-1 px-1 text-ink-faint hover:text-ink md:hidden" aria-label={t('backToThreads')}>
          <ArrowLeft size={19} />
        </Link>
        <h1 className="text-[15px] font-medium">{t('settings')}</h1>
        <div className="flex-1" />
        {problem && <span className="min-w-0 truncate text-xs text-failed">{formatError(problem)}</span>}
        <Button variant="quiet" onClick={() => navigate(from)}>{t('cancel')}</Button>
        <Button variant="solid" disabled={busy || tooLong} onClick={() => void save()}>{t('save')}</Button>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto px-5 pb-24 md:px-7">
        <div className="mx-auto w-full max-w-3xl divide-y divide-rule">
          <Row title={t('language')}>
            <Picker
              variant="field"
              className="max-w-56"
              title={t('language')}
              value={draft.locale || locale}
              options={[{ value: 'zh', label: '中文' }, { value: 'en', label: 'English' }]}
              onChange={(next) => setDraft((d) => ({ ...d, locale: next === 'zh' ? 'zh' : 'en' }))}
            />
          </Row>

          <Row title={t('systemPrompt')}>
            <Textarea
              id="system-prompt"
              aria-label={t('systemPrompt')}
              rows={10}
              value={draft.system_prompt.text}
              placeholder={t('systemPromptPlaceholder')}
              onChange={(e) => setDraft((d) => ({ ...d, system_prompt: { ...d.system_prompt, text: e.target.value } }))}
              className="min-h-40 resize-y leading-relaxed"
            />
            <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1">
              <span className="text-xs text-ink-soft">{t('systemPromptMode')}</span>
              <Picker
                title={t('systemPromptMode')}
                value={draft.system_prompt.mode}
                options={[
                  { value: 'append', label: t('systemPromptAppend') },
                  { value: 'replace', label: t('systemPromptReplace') },
                ]}
                onChange={(next) =>
                  setDraft((d) => ({ ...d, system_prompt: { ...d.system_prompt, mode: next as SystemPromptMode } }))}
              />
              <span className="flex-1" />
              <span className={cn('text-xs tabular-nums text-ink-faint', tooLong && 'text-failed')}>
                {t('systemPromptSize', { count: size, limit: maxSystemPromptBytes })}
              </span>
            </div>

            <div className="mt-4 rounded-[5px] border border-rule bg-paper px-3 py-2.5">
              <div className="mb-1.5 text-xs text-ink-soft">{t('howItWorks')}</div>
              <ul className="grid gap-1 text-xs leading-relaxed text-ink-soft">
                <li>{t('systemPromptNoteShared')}</li>
                <li>{t('systemPromptNoteReplace')}</li>
                <li>{t('systemPromptNoteNew')}</li>
                <li>{t('systemPromptNoteScope')}</li>
              </ul>
            </div>
          </Row>

          <Row title={t('agentsOnThisMachine')}>
            <div className="grid gap-1.5">
              {agents.map((agent) => (
                <div
                  key={agent.kind}
                  className={cn('flex items-center gap-2.5 rounded-[5px] border border-rule bg-paper px-2.5 py-2', agent.runtime.unavailable_reason && 'opacity-55')}
                >
                  <AgentMark kind={agent.kind} label={agent.label} />
                  <span className="min-w-0 flex-1">
                    <span className="block text-[0.8125rem] font-medium">{agent.label}</span>
                    <span className="block truncate text-xs text-ink-soft">{agent.runtime.unavailable_reason ?? t('ready')}</span>
                  </span>
                  <span className="shrink-0 rounded-[4px] bg-sunken px-1.5 py-0.5 text-xs text-ink-soft">
                    {t(promptBehaviour(agent, draft.system_prompt.mode))}
                  </span>
                </div>
              ))}
              {!agents.length && <div className="text-xs text-ink-soft">{t('noAgents')}</div>}
            </div>
          </Row>

          {auth?.password_required && (
            <Row title={t('account')}>
              <Button onClick={() => void signOut()}><LogOut size={15} strokeWidth={1.5} />{t('signOut')}</Button>
            </Row>
          )}
        </div>
      </div>
    </main>
  )
}

/** One setting to a row: what it is on the left, what you set it with on the
 *  right. The two columns stack once the pane is too narrow to hold both. */
function Row({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="grid gap-3 py-7 md:grid-cols-[12rem_1fr] md:gap-10">
      <h2 className="text-[0.875rem] font-medium">{title}</h2>
      <div className="min-w-0">{children}</div>
    </section>
  )
}

/** What the workspace prompt does at one agent, which is the descriptor's
 *  answer and never a guess from the kind: an agent that cannot stand its own
 *  instructions down appends whatever the chosen mode says. */
function promptBehaviour(agent: AgentDescriptor, mode: SystemPromptMode) {
  const support = agent.capabilities.system_prompt_support
  if (!support) return 'systemPromptDoesNothing'
  if (mode === 'replace' && support === 'replace') return 'systemPromptDoesReplace'
  return 'systemPromptDoesAppend'
}
