import { create } from 'zustand'
import { storage } from '@/lib/utils'
import { errorMessage, resolveLocale, translate, type DisplayError, type Locale, type Params, type TranslationKey } from './core'

export const LANGUAGE_STORAGE_KEY = 'agent-web.language'
const useLanguage = create<{ locale: Locale; setLocale: (locale: Locale) => void }>((set) => ({
  locale: resolveLocale(storage.get(LANGUAGE_STORAGE_KEY), navigator.languages),
  setLocale(locale) {
    storage.set(LANGUAGE_STORAGE_KEY, locale)
    set({ locale })
  },
}))

export function useI18n() {
  const { locale, setLocale } = useLanguage()
  return {
    locale,
    setLocale,
    t: (key: TranslationKey, params?: Params) => translate(locale, key, params),
    formatError: (error: DisplayError) => errorMessage(locale, error),
  }
}
