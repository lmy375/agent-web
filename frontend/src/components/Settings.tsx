import { useI18n } from '@/i18n'
import { LogOut } from 'lucide-react'
import { useWorkspace } from '@/store/workspace'
import { AgentMark } from './AgentMark'
import { Button } from './ui/button'
import { Picker } from './ui/field'
import { cn } from '@/lib/utils'

/** What is true of the whole workspace rather than of one thread: the language
 *  it speaks, what this machine can run, and who is signed in. Everything here
 *  applies the moment it is chosen, so the dialog only ever needs dismissing. */
export function Settings({ onClose }: { onClose: () => void }) {
  const { t, locale, setLocale } = useI18n()
  const { agents, auth, signOut } = useWorkspace()

  return (
    <div className="fixed inset-0 z-20 flex items-start justify-center bg-ink/25 p-6 pt-[12vh]" onClick={onClose}>
      <div
        role="dialog" aria-modal="true" aria-labelledby="settings-title"
        className="w-full max-w-md rounded-2xl border border-rule bg-paper p-6 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 id="settings-title" className="text-[0.9375rem] font-medium">{t('settings')}</h2>

        <div className="mt-4">
          <div className="mb-1.5 text-xs text-ink-soft">{t('language')}</div>
          <Picker
            variant="field"
            title={t('language')}
            value={locale}
            options={[{ value: 'zh', label: '中文' }, { value: 'en', label: 'English' }]}
            onChange={(next) => setLocale(next === 'zh' ? 'zh' : 'en')}
          />
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
              </div>
            ))}
            {!agents.length && <div className="text-xs text-ink-soft">{t('noAgents')}</div>}
          </div>
        </div>

        <div className="mt-5 flex items-center gap-2">
          {auth?.password_required && (
            <Button variant="quiet" onClick={() => void signOut()}><LogOut size={15} strokeWidth={1.5} />{t('signOut')}</Button>
          )}
          <div className="flex-1" />
          <Button onClick={onClose}>{t('done')}</Button>
        </div>
      </div>
    </div>
  )
}
