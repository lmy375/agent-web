import { LanguageSwitcher } from './LanguageSwitcher'
import type { DisplayError } from '@/i18n/core'
import { useI18n } from '@/i18n'
import { useState } from 'react'
import { useWorkspace } from '@/store/workspace'
import { Button } from './ui/button'
import { Input } from './ui/field'

export function SignIn() {
  const { t, formatError } = useI18n()
  const signIn = useWorkspace((s) => s.signIn)
  const [password, setPassword] = useState('')
  const [problem, setProblem] = useState<DisplayError | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(event: React.FormEvent) {
    event.preventDefault()
    setBusy(true)
    setProblem(await signIn(password))
    setBusy(false)
  }

  return (
    <div className="relative flex h-full items-center justify-center px-6">
      <LanguageSwitcher className="absolute right-4 top-4" />
      <form onSubmit={submit} className="w-full max-w-xs">
        <h1 className="text-[0.9375rem] font-semibold tracking-tight">agent-web</h1>
        <p className="mt-1 text-[0.8125rem] text-ink-soft">{t('signInHint')}</p>
        <Input
          autoFocus
          type="password"
          aria-label={t('password')}
          value={password}
          placeholder={t('password')}
          onChange={(e) => setPassword(e.target.value)}
          className="mt-4 h-9"
        />
        {problem && <div className="mt-1.5 text-xs text-failed">{formatError(problem)}</div>}
        <Button type="submit" variant="solid" disabled={busy || !password} className="mt-2.5 h-9 w-full">{t('signIn')}</Button>
      </form>
    </div>
  )
}
