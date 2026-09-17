import { useEffect, useId } from 'react'
import { useI18n } from '@/i18n'
import { Button } from './ui/button'

/** The one question worth stopping for, asked in this page's own voice rather
 *  than the browser's: `confirm()` is modal to the whole tab, unstyled, and
 *  says the origin's name back to the owner. */
export function ConfirmDialog({ title, message, confirmLabel, onConfirm, onClose }: {
  title: string
  message: string
  confirmLabel: string
  onConfirm: () => void
  onClose: () => void
}) {
  const { t } = useI18n()
  const titleID = useId()

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div className="fixed inset-0 z-30 flex items-start justify-center bg-ink/25 p-6 pt-[16vh]" onClick={onClose}>
      <div
        role="dialog" aria-modal="true" aria-labelledby={titleID}
        className="w-full max-w-sm rounded-2xl border border-rule bg-paper p-5 shadow-lg"
        onClick={(event) => event.stopPropagation()}
      >
        <h2 id={titleID} className="text-[0.9375rem] font-medium">{title}</h2>
        <p className="mt-2 text-[0.8125rem] leading-relaxed text-ink-soft">{message}</p>
        <div className="mt-5 flex justify-end gap-2">
          <Button variant="quiet" onClick={onClose}>{t('cancel')}</Button>
          <Button autoFocus variant="danger" onClick={() => { onClose(); onConfirm() }}>{confirmLabel}</Button>
        </div>
      </div>
    </div>
  )
}
