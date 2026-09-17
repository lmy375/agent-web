import { localeTag } from '@/i18n/core'
import { useI18n } from '@/i18n'
import { Fragment } from 'react'
import type { ContextUsage, TurnSummary } from '@/store/protocol'
import { cn } from '@/lib/utils'

/**
 * How full the window is, as one ring the eye can read without stopping. The
 * numbers behind it -- what each part of the context costs, what the last turn
 * charged -- are worth a glance, not a permanent strip, so they wait under a
 * hover. A harness that does not report a window size still reports a count,
 * so that case shows the number rather than pretending to a ratio.
 */
export function ContextMeter({ usage, lastTurn, className }: { usage: ContextUsage | null; lastTurn: TurnSummary | null; className?: string }) {
  const { t, locale } = useI18n()
  const cost = lastTurn?.cost_usd
  if (!usage && cost == null) return null

  const ratio = usage && usage.max_tokens > 0 ? Math.min(1, usage.total_tokens / usage.max_tokens) : null
  const percent = ratio === null ? null : Math.round(ratio * 100)
  const number = (n: number) => n.toLocaleString(localeTag[locale])

  return (
    <div className={cn('group relative flex shrink-0 items-center', className)}>
      <button
        type="button"
        aria-label={percent !== null ? `${t('contextUsage')} ${percent}%` : t('contextUsage')}
        className="flex items-center gap-1.5 rounded-md px-1.5 py-1 text-[0.6875rem] text-ink-faint transition-colors hover:bg-sunken hover:text-ink-soft focus-visible:bg-sunken"
      >
        {ratio !== null ? (
          <Ring ratio={ratio} />
        ) : usage ? (
          <span>{t('tokenCount', { count: compact(usage.total_tokens) })}</span>
        ) : (
          <span>${cost!.toFixed(3)}</span>
        )}
      </button>

      <div
        role="tooltip"
        className="pointer-events-none absolute bottom-full left-0 z-10 mb-1.5 hidden w-60 rounded-lg border border-rule bg-surface p-2.5 text-[0.6875rem] leading-relaxed shadow-[0_6px_24px_-8px_#26262433] group-focus-within:block group-hover:block"
      >
        {usage && (
          <>
            <div className="flex items-baseline justify-between gap-2">
              <span className="text-ink-soft">{t('contextUsage')}</span>
              {percent !== null && <span className="font-medium text-ink">{percent}%</span>}
            </div>
            <div className="mt-0.5 font-mono text-ink">
              {ratio !== null
                ? `${number(usage.total_tokens)} / ${number(usage.max_tokens)}`
                : t('tokenCount', { count: number(usage.total_tokens) })}
            </div>
            {usage.categories.length > 0 && (
              <dl className="mt-2 grid grid-cols-[1fr_auto] gap-x-3 border-t border-rule pt-2 text-ink-faint">
                {usage.categories.map((category) => (
                  <Fragment key={category.name}>
                    <dt className="truncate">{category.name}</dt>
                    <dd className="font-mono">{number(category.tokens)}</dd>
                  </Fragment>
                ))}
              </dl>
            )}
            {usage.auto_compact_threshold_tokens != null && (
              <p className="mt-1.5 text-ink-faint">{t('autoCompactAt', { count: compact(usage.auto_compact_threshold_tokens) })}</p>
            )}
          </>
        )}
        {cost != null && (
          <div className={cn('flex items-baseline justify-between gap-2', usage && 'mt-2 border-t border-rule pt-2')}>
            <span className="text-ink-soft">{t('lastTurnCost')}</span>
            <span className="font-mono text-ink">${cost.toFixed(3)}</span>
          </div>
        )}
      </div>
    </div>
  )
}

/** Ink, never a hue: a full window is not the same kind of news as a failure,
 *  so a fuller ring only darkens. */
function Ring({ ratio }: { ratio: number }) {
  const circumference = 2 * Math.PI * 5.5
  return (
    <svg viewBox="0 0 16 16" className="h-4 w-4 -rotate-90" aria-hidden>
      <circle cx="8" cy="8" r="5.5" fill="none" strokeWidth="2.5" stroke="currentColor" className="text-sunken" />
      <circle
        cx="8"
        cy="8"
        r="5.5"
        fill="none"
        strokeWidth="2.5"
        strokeLinecap="round"
        stroke="currentColor"
        strokeDasharray={`${ratio * circumference} ${circumference}`}
        className={cn('transition-[stroke-dasharray,color]', ratio > 0.9 ? 'text-ink' : ratio > 0.75 ? 'text-ink-soft' : 'text-ink-faint')}
      />
    </svg>
  )
}

const compact = (n: number) => (n >= 1000 ? `${Math.round(n / 1000)}k` : String(n))
