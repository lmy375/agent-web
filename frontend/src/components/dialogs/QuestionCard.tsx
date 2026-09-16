import { useI18n } from '@/i18n'
import { useState } from 'react'
import type { InteractionDecision, QuestionPayload } from '@/store/protocol'
import { Button } from '../ui/button'
import { Textarea } from '../ui/field'
import { Ask } from './InteractionPanel'

export function QuestionCard({
  payload, onRespond,
}: {
  payload: QuestionPayload
  onRespond: (decision: InteractionDecision) => void
}) {
  const { t } = useI18n()
  const [picked, setPicked] = useState<Record<string, string[]>>({})
  const [written, setWritten] = useState<Record<string, string>>({})

  const toggle = (id: string, label: string, multi: boolean) =>
    setPicked((current) => {
      const existing = current[id] ?? []
      if (!multi) return { ...current, [id]: existing.includes(label) ? [] : [label] }
      return {
        ...current,
        [id]: existing.includes(label) ? existing.filter((l) => l !== label) : [...existing, label],
      }
    })

  const answerFor = (id: string) => {
    const extra = written[id]?.trim()
    return [...(picked[id] ?? []), ...(extra ? [extra] : [])]
  }
  const complete = payload.questions.every((q) => answerFor(q.id).length > 0)

  return (
    <Ask
      title={payload.questions.length > 1 ? t('questions', { count: payload.questions.length }) : t('question')}
      actions={
        <>
          <Button
            variant="solid"
            disabled={!complete}
            onClick={() =>
              onRespond({
                type: 'answer',
                answers: payload.questions.map((q) => ({ question_id: q.id, answers: answerFor(q.id) })),
              })
            }
          >{t('answer')}</Button>
          <Button variant="quiet" onClick={() => onRespond({ type: 'deny', message: 'The user skipped the question.' })}>{t('skip')}</Button>
        </>
      }
    >
      <div className="space-y-4">
        {payload.questions.map((question) => (
          <div key={question.id}>
            <div className="mb-1.5 text-[0.875rem] leading-snug text-ink">{question.prompt}</div>
            <div className="grid gap-1.5 sm:grid-cols-2">
              {question.options.map((option) => {
                const on = (picked[question.id] ?? []).includes(option.label)
                return (
                  <button
                    key={option.label}
                    onClick={() => toggle(question.id, option.label, question.multi_select)}
                    className={
                      'rounded-[5px] border px-2.5 py-1.5 text-left text-[0.8125rem] ' +
                      (on ? 'border-ink bg-surface' : 'border-rule bg-surface/60 hover:border-ink-faint')
                    }
                  >
                    <div className="font-medium">{option.label}</div>
                    {option.description && <div className="text-xs text-ink-soft">{option.description}</div>}
                  </button>
                )
              })}
            </div>
            <Textarea
              rows={1}
              aria-label={question.prompt}
              className="mt-1.5"
              placeholder={t('ownAnswer')}
              value={written[question.id] ?? ''}
              onChange={(e) => setWritten((w) => ({ ...w, [question.id]: e.target.value }))}
            />
          </div>
        ))}
      </div>
    </Ask>
  )
}
