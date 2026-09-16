import { useI18n } from '@/i18n'
import type { InteractionDecision, PlanPayload } from '@/store/protocol'
import { Markdown } from '../blocks/Markdown'
import { Button } from '../ui/button'
import { Ask } from './InteractionPanel'

export function PlanCard({
  payload, onRespond,
}: {
  payload: PlanPayload
  onRespond: (decision: InteractionDecision) => void
}) {
  const { t } = useI18n()
  return (
    <Ask
      title={t('planReady')}
      actions={
        <>
          <Button variant="solid" onClick={() => onRespond({ type: 'allow' })}>{t('startWorking')}</Button>
          <Button onClick={() => onRespond({ type: 'deny', message: 'Keep planning; the plan needs changes.' })}>{t('keepPlanning')}</Button>
        </>
      }
    >
      <div className="max-h-96 overflow-y-auto">
        <Markdown>{payload.plan_markdown}</Markdown>
      </div>
    </Ask>
  )
}
