import { create } from 'zustand'
import { storage } from '@/lib/utils'
import { errorMessage, resolveLocale, translate, type DisplayError, type Locale, type Params, type TranslationKey } from './core'

/** The chosen language is a workspace setting and lives on the server; this is
 *  the local mirror of it. Two screens cannot read the setting at all -- the
 *  sign-in and connection-error screens have no session -- and every screen
 *  paints before the fetch returns, which is what the mirror answers. A choice
 *  made where there is no session is for that screen: signing in applies
 *  whatever the server holds. */
export const LANGUAGE_STORAGE_KEY = 'agent-web.language'

export const useLanguage = create<{ locale: Locale; setLocale: (locale: Locale) => void }>((set) => ({
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
