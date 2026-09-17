import { en, type TranslationKey } from './en.ts'
import { zh } from './zh.ts'

export type Locale = 'en' | 'zh'
export type Params = Record<string, string | number>
export type DisplayError = Error | string
export const localeTag: Record<Locale, string> = { en: 'en-US', zh: 'zh-CN' }
export const messages = { en, zh }
export type { TranslationKey }

export function resolveLocale(saved: string | null, languages: readonly string[]): Locale {
  if (saved === 'en' || saved === 'zh') return saved
  for (const language of languages) {
    if (/^zh(?:-|$)/i.test(language)) return 'zh'
    if (/^en(?:-|$)/i.test(language)) return 'en'
  }
  return 'en'
}

export function translate(locale: Locale, key: TranslationKey, params: Params = {}): string {
  return messages[locale][key].replace(/\{(\w+)\}/g, (placeholder, name: string) =>
    Object.hasOwn(params, name) ? String(params[name]) : placeholder,
  )
}

/** Keep error data, not translated text, so an open error changes language too. */
export class LocalizedError extends Error {
  key: TranslationKey
  params: Params
  constructor(key: TranslationKey, params: Params = {}) {
    super(translate('en', key, params))
    this.key = key
    this.params = params
  }
}

export function errorMessage(locale: Locale, error: DisplayError): string {
  if (error instanceof LocalizedError) return translate(locale, error.key, error.params)
  const message = typeof error === 'string' ? error : error.message
  if (/^(Failed to fetch|Load failed|NetworkError when attempting to fetch resource\.)$/.test(message)) {
    return translate(locale, 'error.unreachable')
  }
  const code = typeof error === 'object' && 'code' in error ? String(error.code) : ''
  const key = `error.${code}`
  if (Object.hasOwn(en, key)) {
    const summary = translate(locale, key as TranslationKey)
    // Preserve server diagnostics, paths and vendor messages verbatim.
    return message && message !== summary ? `${summary}: ${message}` : summary
  }
  return message
}

export function relativeTime(locale: Locale, ms?: number | null, now = Date.now()): string {
  if (!ms) return ''
  const minutes = Math.round((ms - now) / 60000)
  if (Math.abs(minutes) < 1) return translate(locale, 'justNow')
  const formatter = new Intl.RelativeTimeFormat(localeTag[locale], { numeric: 'always', style: 'short' })
  if (Math.abs(minutes) < 60) return formatter.format(minutes, 'minute')
  const hours = Math.round(minutes / 60)
  if (Math.abs(hours) < 24) return formatter.format(hours, 'hour')
  const days = Math.round(hours / 24)
  if (Math.abs(days) < 7) return formatter.format(days, 'day')
  return new Date(ms).toLocaleDateString(localeTag[locale])
}
