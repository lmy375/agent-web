import { Languages } from 'lucide-react'
import { useI18n } from '@/i18n'
import { cn } from '@/lib/utils'

export function LanguageSwitcher({ className }: { className?: string }) {
  const { locale, setLocale, t } = useI18n()
  return (
    <label className={cn('inline-flex items-center gap-1.5 rounded-lg px-2 py-1 text-xs text-ink-soft hover:bg-sunken', className)}>
      <Languages size={15} aria-hidden="true" />
      <select aria-label={t('language')} title={t('language')} value={locale} onChange={(event) => setLocale(event.target.value === 'zh' ? 'zh' : 'en')} className="min-w-0 bg-transparent py-1 outline-offset-2">
        <option value="zh" lang="zh-CN">中文</option>
        <option value="en" lang="en">English</option>
      </select>
    </label>
  )
}
