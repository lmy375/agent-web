import { localeTag } from '@/i18n/core'
import { useI18n } from '@/i18n'
import type { ContextUsage, TurnSummary } from '@/store/protocol'

/**
 * How full the window is. A harness that does not report a window size still
 * reports a count, so this shows the number rather than pretending to a ratio.
 */
export function ContextMeter({ usage, lastTurn }: { usage: ContextUsage | null; lastTurn: TurnSummary | null }) {
  const { t, locale } = useI18n()
  if (!usage && !lastTurn?.cost_usd) return null
  const ratio = usage && usage.max_tokens > 0 ? Math.min(1, usage.total_tokens / usage.max_tokens) : null

  return (
    <div className="flex shrink-0 items-center gap-2.5 whitespace-nowrap text-[0.6875rem] text-ink-faint">
      {lastTurn?.cost_usd != null && <span className="hidden sm:inline" title={t('lastTurnCost')}>${lastTurn.cost_usd.toFixed(3)}</span>}
      {usage && (
        <span
          className="flex items-center gap-1.5"
          title={usage.categories.map((c) => `${c.name}: ${c.tokens.toLocaleString(localeTag[locale])}`).join('\n')}
        >
          {ratio !== null && (
            <span className="relative hidden h-1 w-14 sm:inline-block overflow-hidden rounded-full bg-sunken">
              <span
                className="absolute inset-y-0 left-0 bg-ink-faint"
                style={{ width: `${Math.max(2, ratio * 100)}%` }}
              />
            </span>
          )}
          <span>
            {ratio !== null ? `${compact(usage.total_tokens)} / ${compact(usage.max_tokens)}` : t('tokenCount', { count: compact(usage.total_tokens) })}
          </span>
        </span>
      )}
    </div>
  )
}

const compact = (n: number) => (n >= 1000 ? `${Math.round(n / 1000)}k` : String(n))
