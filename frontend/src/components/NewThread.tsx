import { LocalizedError, type DisplayError } from '@/i18n/core'
import { useI18n } from '@/i18n'
import { useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import { useWorkspace } from '@/store/workspace'
import type { AgentKind, ThreadOptions } from '@/store/protocol'
import { AgentMark } from './AgentMark'
import { DirectoryPicker } from './DirectoryPicker'
import { Button } from './ui/button'
import { Picker } from './ui/field'
import { cn, displayGroup } from '@/lib/utils'

/** Choosing an agent, its options and a directory is the whole of starting a
 *  thread; every option here is read from the chosen agent's descriptor, and
 *  each one can still be changed later from the composer. */
export function NewThread({ onClose, initialCwd }: { onClose: () => void; initialCwd?: string }) {
  const { t, formatError } = useI18n()
  const { agents, config, create } = useWorkspace()
  const navigate = useNavigate()
  const [chosen, setChosen] = useState<AgentKind | null>(null)
  const [picked, setPicked] = useState<string | null>(null)
  const [overrides, setOverrides] = useState<Partial<ThreadOptions>>({})
  const [problem, setProblem] = useState<DisplayError | null>(null)
  const [busy, setBusy] = useState(false)

  // Both defaults come from what the server said, so they are derived rather
  // than copied into state on arrival.
  const kind = chosen ?? agents.find((a) => !a.runtime.unavailable_reason)?.kind ?? null
  const cwd = picked ?? initialCwd ?? config?.root_dir ?? ''
  const setCwd = setPicked

  function setKind(next: AgentKind) {
    setChosen(next)
    // A model id or a knob's value belongs to one agent, never to the next one.
    setOverrides({})
  }

  const descriptor = agents.find((a) => a.kind === kind)
  const defaults = descriptor?.runtime.defaults
  const model = overrides.model ?? defaults?.model ?? ''
  const groups = descriptor?.runtime.groups ?? []
  const setting = (id: string) => overrides.settings?.[id] ?? defaults?.settings?.[id] ?? ''

  const modelOptions = (descriptor?.runtime.models ?? []).map((m) => ({
    value: m.id,
    label: m.label === 'Default (recommended)' ? t('modelDefault') : m.label,
  }))
  // An agent that names no default model lets the harness decide, and that has
  // to stay reachable once something else has been chosen.
  if (modelOptions.length && !defaults?.model) modelOptions.unshift({ value: '', label: t('modelDefault') })

  async function start() {
    if (!kind) return
    setBusy(true)
    try {
      const settings = Object.fromEntries(
        groups.map((group) => [group.id, setting(group.id)]).filter(([, value]) => value),
      )
      const thread = await create(kind, cwd, {
        ...(model ? { model } : {}),
        ...(Object.keys(settings).length ? { settings } : {}),
      })
      onClose()
      navigate(`/t/${encodeURIComponent(thread.thread_id)}`)
    } catch (error) {
      setProblem(error instanceof Error ? error : new LocalizedError('error.createThread'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 z-20 flex items-start justify-center bg-ink/25 p-6 pt-[12vh]" onClick={onClose}>
      <div
        role="dialog" aria-modal="true" aria-labelledby="new-thread-title"
        className="w-full max-w-lg rounded-2xl border border-rule bg-paper p-6 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 id="new-thread-title" className="text-[0.9375rem] font-medium">{t('newThread')}</h2>

        <div className="mt-3 grid gap-1.5">
          {agents.map((agent) => {
            const blocked = agent.runtime.unavailable_reason
            return (
              <button
                key={agent.kind}
                disabled={!!blocked}
                onClick={() => setKind(agent.kind)}
                className={cn(
                  'flex items-center gap-2.5 rounded-[5px] border px-2.5 py-2 text-left',
                  kind === agent.kind ? 'border-ink bg-surface' : 'border-rule bg-surface/60 hover:border-ink-faint',
                  blocked && 'cursor-not-allowed opacity-55 hover:border-rule',
                )}
              >
                <AgentMark kind={agent.kind} label={agent.label} />
                <span className="min-w-0 flex-1">
                  <span className="block text-[0.8125rem] font-medium">{agent.label}</span>
                  <span className="block truncate text-xs text-ink-soft">
                    {blocked ?? t('agentOptions', { models: agent.runtime.models.length, knobs: agent.runtime.groups.length })}
                  </span>
                </span>
              </button>
            )
          })}
          {!agents.length && <div className="text-xs text-ink-soft">{t('noAgents')}</div>}
        </div>

        {(modelOptions.length > 0 || groups.length > 0) && (
          <div className="mt-4 flex flex-wrap gap-2.5">
            {modelOptions.length > 0 && (
              <Option label={t('model')}>
                <Picker variant="field" title={t('model')} value={model} options={modelOptions} onChange={(next) => setOverrides((o) => ({ ...o, model: next }))} />
              </Option>
            )}
            {groups.map(displayGroup).map((group) => (
              <Option key={group.id} label={group.label}>
                <Picker
                  variant="field"
                  title={group.label}
                  value={setting(group.id)}
                  options={group.options}
                  onChange={(next) => setOverrides((o) => ({ ...o, settings: { ...o.settings, [group.id]: next } }))}
                />
              </Option>
            ))}
          </div>
        )}

        <div className="mt-4">
          <div className="mb-1.5 text-xs text-ink-soft">{t('workingDirectory')}</div>
          <DirectoryPicker value={cwd} home={config?.home_dir ?? null} onChange={setCwd} />
        </div>

        {problem && <div className="mt-2 text-xs text-failed">{formatError(problem)}</div>}

        <div className="mt-4 flex justify-end gap-2">
          <Button variant="quiet" onClick={onClose}>{t('cancel')}</Button>
          <Button variant="solid" disabled={!kind || !cwd || busy} onClick={start}>{t('start')}</Button>
        </div>
      </div>
    </div>
  )
}

/** The label is repeated as the control's `aria-label`, so what is read is
 *  exactly what is shown. */
function Option({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="min-w-[8.5rem] flex-1">
      <span className="mb-1.5 block text-xs text-ink-soft">{label}</span>
      {children}
    </label>
  )
}
