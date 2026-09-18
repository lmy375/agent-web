import { LocalizedError, type DisplayError } from '@/i18n/core'
import { useI18n } from '@/i18n'
import { LogOut } from 'lucide-react'
import { useState } from 'react'
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

/** What is true of the whole workspace rather than of one thread: the language
 *  it speaks, the system prompt its threads are created with, and what this
 *  machine can run. The prompt is a draft until it is saved, so the language
 *  beside it waits for the same button rather than applying under the typing. */
export function Settings({ onClose }: { onClose: () => void }) {
  const { t, locale, formatError } = useI18n()
  const { agents, auth, settings, saveSettings, signOut } = useWorkspace()
  const [draft, setDraft] = useState<WorkspaceSettings>(
    settings ?? { locale: '', system_prompt: { text: '', mode: 'append' } },
  )
  const [problem, setProblem] = useState<DisplayError | null>(null)
  const [busy, setBusy] = useState(false)

  const size = new TextEncoder().encode(draft.system_prompt.text).length
  const tooLong = size > maxSystemPromptBytes

  async function save() {
    setBusy(true)
    try {
      await saveSettings({ ...draft, locale: draft.locale || locale })
      onClose()
    } catch (error) {
      setProblem(error instanceof Error ? error : new LocalizedError('error.settings_invalid'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 z-20 flex items-start justify-center bg-ink/25 p-6 pt-[8vh]" onClick={onClose}>
      <div
        role="dialog" aria-modal="true" aria-labelledby="settings-title"
        className="max-h-[84vh] w-full max-w-lg overflow-y-auto rounded-2xl border border-rule bg-paper p-6 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 id="settings-title" className="text-[0.9375rem] font-medium">{t('settings')}</h2>

        <label className="mt-4 block">
          <span className="mb-1.5 block text-xs text-ink-soft">{t('language')}</span>
          <Picker
            variant="field"
            title={t('language')}
            value={draft.locale || locale}
            options={[{ value: 'zh', label: '中文' }, { value: 'en', label: 'English' }]}
            onChange={(next) => setDraft((d) => ({ ...d, locale: next === 'zh' ? 'zh' : 'en' }))}
          />
        </label>

        <div className="mt-4">
          <div className="mb-1.5 flex items-baseline justify-between gap-2">
            <label htmlFor="system-prompt" className="text-xs text-ink-soft">{t('systemPrompt')}</label>
            <span className={cn('text-xs tabular-nums text-ink-faint', tooLong && 'text-failed')}>
              {t('systemPromptSize', { count: size, limit: maxSystemPromptBytes })}
            </span>
          </div>
          <Textarea
            id="system-prompt"
            rows={6}
            value={draft.system_prompt.text}
            placeholder={t('systemPromptPlaceholder')}
            onChange={(e) => setDraft((d) => ({ ...d, system_prompt: { ...d.system_prompt, text: e.target.value } }))}
            className="leading-relaxed"
          />
          <label className="mt-2 flex items-center gap-2">
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
          </label>
        </div>

        <div className="mt-4 rounded-[5px] border border-rule bg-surface px-3 py-2.5">
          <div className="mb-1.5 text-xs text-ink-soft">{t('howItWorks')}</div>
          <ul className="grid gap-1 text-xs leading-relaxed text-ink-soft">
            <li>{t('systemPromptNoteShared')}</li>
            <li>{t('systemPromptNoteReplace')}</li>
            <li>{t('systemPromptNoteNew')}</li>
            <li>{t('systemPromptNoteScope')}</li>
          </ul>
        </div>

        <div className="mt-4">
          <div className="mb-1.5 text-xs text-ink-soft">{t('agentsOnThisMachine')}</div>
          <div className="grid gap-1.5">
            {agents.map((agent) => (
              <div
                key={agent.kind}
                className={cn('flex items-center gap-2.5 rounded-[5px] border border-rule bg-surface px-2.5 py-2', agent.runtime.unavailable_reason && 'opacity-55')}
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
        </div>

        {problem && <div className="mt-2 text-xs text-failed">{formatError(problem)}</div>}

        <div className="mt-5 flex items-center gap-2">
          {auth?.password_required && (
            <Button variant="quiet" onClick={() => void signOut()}><LogOut size={15} strokeWidth={1.5} />{t('signOut')}</Button>
          )}
          <div className="flex-1" />
          <Button variant="quiet" onClick={onClose}>{t('cancel')}</Button>
          <Button variant="solid" disabled={busy || tooLong} onClick={() => void save()}>{t('save')}</Button>
        </div>
      </div>
    </div>
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
