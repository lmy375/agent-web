import { Languages } from 'lucide-react'
import { useI18n } from '@/i18n'
import { Picker } from './ui/field'
import { cn } from '@/lib/utils'

export function LanguageSwitcher({ className }: { className?: string }) {
  const { locale, setLocale, t } = useI18n()
  return (
    <div className={cn('inline-flex items-center gap-1 text-ink-soft', className)}>
      <Languages size={15} aria-hidden="true" />
      <Picker
        title={t('language')}
        value={locale}
        options={[{ value: 'zh', label: '中文' }, { value: 'en', label: 'English' }]}
        onChange={(next) => setLocale(next === 'zh' ? 'zh' : 'en')}
      />
    </div>
  )
}
