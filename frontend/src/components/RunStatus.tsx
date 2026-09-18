import { localeTag, type TranslationKey } from '@/i18n/core'
import { useI18n } from '@/i18n'
import { LoaderCircle } from 'lucide-react'
import { Fragment, useEffect, useState } from 'react'
import type { RunState, RunningTurn, TurnSummary, Usage } from '@/store/protocol'
import type { Item } from '@/store/transcript'
import { latestThought, type ActivityEntry } from '@/lib/transcriptPresentation'
import { cn, compactTokens } from '@/lib/utils'

/**
 * The last line of the conversation: how long the turn has been going, what it
 * has spent, and what the agent is doing right now. It keeps ticking while the
 * turn runs and stays behind afterwards as that turn's record, until the next
 * turn replaces it. It sits at the end of the transcript rather than in the
 * composer, where the context meter is already speaking about the window.
 */
export function RunStatus({ running, state, items, current, last }: {
  running: boolean
  state: RunState
  items: Item[]
  current: RunningTurn | null
  last: TurnSummary | null
}) {
  const { t } = useI18n()
  // The seconds tick inside this component alone: a clock in the store would
  // re-render the whole transcript, and the scroller's follow-the-bottom effect
  // with it, once a second.
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!running) return
    setNow(Date.now())
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [running])

  if (!running && !last) return null

  const activity = latestActivity(items)

  const startedAt = Date.parse(running ? (current?.started_at ?? '') : (last?.started_at ?? ''))
  const finishedAt = running ? now : Date.parse(last?.finished_at ?? '')
  const ran = startedAt > 0 && finishedAt > startedAt ? elapsed(finishedAt - startedAt) : ''
  const usage = running ? current?.usage : last?.usage
  const cost = running ? null : last?.cost_usd

  return (
    <div className="mt-6 flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1 border-t border-rule pt-3 text-[0.6875rem] text-ink-faint">
      {running
        ? <LoaderCircle size={13} strokeWidth={1.7} className="shrink-0 animate-spin text-running" aria-hidden="true" />
        : <span className="ml-0.5 block h-1.5 w-1.5 shrink-0 rounded-full bg-rule" aria-hidden="true" />}
      {ran && <span className="shrink-0 font-mono">{ran}</span>}
      {usage && <Tokens usage={usage} cost={cost ?? null} elapsed={ran} />}
      <span className="min-w-0 flex-1 truncate">
        {t(phrase({ running, state, known: current !== null, activity, last }))}
        {running && activity.thought && ` · ${activity.thought}`}
      </span>
    </div>
  )
}

/** The four counts side by side, with the whole accounting under a hover. */
function Tokens({ usage, cost, elapsed }: { usage: Usage; cost: number | null; elapsed: string }) {
  const { t, locale } = useI18n()
  const number = (n: number) => n.toLocaleString(localeTag[locale])
  const rows: [TranslationKey, number][] = [
    ['usage.input', usage.input_tokens],
    ['usage.output', usage.output_tokens],
    ['usage.cacheRead', usage.cache_read_tokens],
    ['usage.cacheWrite', usage.cache_write_tokens],
  ]
  if (usage.reasoning_tokens != null) rows.push(['usage.reasoning', usage.reasoning_tokens])

  return (
    <div className="group relative flex shrink-0 items-center">
      <button
        type="button"
        aria-label={t('runStatus.turnUsage')}
        className="flex items-center gap-1 rounded-md px-1 py-0.5 transition-colors hover:bg-sunken hover:text-ink-soft focus-visible:bg-sunken"
      >
        {/* A narrow screen keeps the two counts that move; the cache pair waits
            under the hover, where the full accounting already is. */}
        {rows.slice(0, 4).map(([key, tokens], index) => (
          <span key={key} className={cn('flex items-center gap-1', index > 1 && 'hidden sm:flex')}>
            <span className="font-mono">{compactTokens(tokens)}</span>
            <span>{t(key)}</span>
          </span>
        ))}
      </button>

      <div
        role="tooltip"
        className="pointer-events-none absolute bottom-full left-0 z-10 mb-1.5 hidden w-56 rounded-lg border border-rule bg-surface p-2.5 text-[0.6875rem] leading-relaxed shadow-[0_6px_24px_-8px_#26262433] group-focus-within:block group-hover:block"
      >
        {elapsed && (
          <div className="flex items-baseline justify-between gap-2">
            <span className="text-ink-soft">{t('runStatus.elapsed')}</span>
            <span className="font-mono text-ink">{elapsed}</span>
          </div>
        )}
        <dl className={cn('grid grid-cols-[1fr_auto] gap-x-3 text-ink-faint', elapsed && 'mt-2 border-t border-rule pt-2')}>
          {rows.map(([key, tokens]) => (
            <Fragment key={key}>
              <dt className="truncate">{t(key)}</dt>
              <dd className="font-mono text-ink">{number(tokens)}</dd>
            </Fragment>
          ))}
        </dl>
        {cost != null && (
          <div className="mt-2 flex items-baseline justify-between gap-2 border-t border-rule pt-2">
            <span className="text-ink-soft">{t('runStatus.cost')}</span>
            <span className="font-mono text-ink">${cost.toFixed(3)}</span>
          </div>
        )}
      </div>
    </div>
  )
}

/**
 * What the newest message says the agent is doing. Only that message is read:
 * the reducer mutates the transcript in place, so nothing here may be memoised
 * on the array's identity, and this runs on every tick of the clock.
 */
function latestActivity(items: Item[]): { tools: boolean; writing: boolean; thought: string } {
  const item = items.at(-1)
  if (item?.kind !== 'assistant') return { tools: false, writing: false, thought: '' }
  const last = item.blocks.at(-1)
  const thinking = item.blocks.findLast((block) => block.type === 'thinking')
  const thought = thinking
    ? latestThought([{ kind: 'thinking', id: item.id, text: thinking.text, streaming: false } satisfies ActivityEntry])
    : ''
  return { tools: last?.type === 'tool', writing: last?.type === 'text', thought }
}

function phrase({ running, state, known, activity, last }: {
  running: boolean
  state: RunState
  /** The turn has been reported; without that the harness is still coming up,
   *  or this page has just reconnected and not yet resynced. */
  known: boolean
  activity: { tools: boolean; writing: boolean }
  last: TurnSummary | null
}): TranslationKey {
  if (!running) return last ? `turnStatus.${last.status}` : 'runStatus.working'
  if (state === 'starting' || !known) return 'runStatus.starting'
  if (state === 'waiting_input') return 'runStatus.waiting'
  if (activity.tools) return 'runStatus.tools'
  if (activity.writing) return 'runStatus.writing'
  return 'runStatus.thinking'
}

/** `47s`, `22m 32s`, `1h 22m 32s`. Digits and the unit letters read the same in
 *  every locale this ships, so none of it goes through the translation table. */
function elapsed(ms: number): string {
  const total = Math.round(ms / 1000)
  const seconds = total % 60
  const minutes = Math.floor(total / 60) % 60
  const hours = Math.floor(total / 3600)
  const pad = (n: number) => String(n).padStart(2, '0')
  if (hours) return `${hours}h ${pad(minutes)}m ${pad(seconds)}s`
  if (minutes) return `${minutes}m ${pad(seconds)}s`
  return `${seconds}s`
}
