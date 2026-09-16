import { useI18n } from '@/i18n'
import { useState } from 'react'
import type { InteractionDecision, PermissionPayload } from '@/store/protocol'
import { toolSummary } from '@/lib/toolSummary'
import { Button } from '../ui/button'
import { Ask } from './InteractionPanel'

export function PermissionCard({
  payload, onRespond,
}: {
  payload: PermissionPayload
  onRespond: (decision: InteractionDecision) => void
}) {
  const { t, locale } = useI18n()
  const [remembered, setRemembered] = useState<string | null>(null)
  const summary = toolSummary(payload.tool_kind, payload.tool_name, payload.tool_input, locale)
  const chosen = payload.suggestions.find((s) => s.rule === remembered)

  return (
    <Ask
      title={t('runTool', { tool: payload.tool_name })}
      actions={
        <>
          <Button variant="solid" onClick={() => onRespond({ type: 'allow', remember: chosen })}>
            {chosen ? t('allowRemember') : t('allowOnce')}
          </Button>
          <Button onClick={() => onRespond({ type: 'deny', message: 'The user declined this tool call.' })}>{t('deny')}</Button>
          <Button
            variant="quiet"
            onClick={() => onRespond({ type: 'deny', message: 'The user stopped the turn.', interrupt: true })}
          >{t('denyStop')}</Button>
        </>
      }
    >
      <pre className="overflow-x-auto whitespace-pre-wrap font-mono text-[0.8125rem] leading-relaxed text-ink">
        {summary || JSON.stringify(payload.tool_input, null, 2)}
      </pre>
      {payload.suggestions.length > 0 && (
        <div className="mt-2.5 flex flex-wrap items-center gap-1.5">
          {payload.suggestions.map((suggestion) => {
            const on = remembered === suggestion.rule
            return (
              <button
                key={suggestion.rule}
                onClick={() => setRemembered(on ? null : suggestion.rule)}
                className={
                  'rounded-[4px] border px-2 py-1 font-mono text-[0.6875rem] ' +
                  (on ? 'border-ink bg-ink text-paper' : 'border-rule bg-surface text-ink-soft hover:border-ink-faint')
                }
              >
                {suggestion.rule}
                <span className="ml-1.5 opacity-60">{suggestion.scope === 'session' ? t('thisSession') : t('always')}</span>
              </button>
            )
          })}
        </div>
      )}
    </Ask>
  )
}
