import { Check, Copy } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useI18n } from '@/i18n'
import { cn } from '@/lib/utils'

/**
 * Copies the markdown the message was written in, not the rendered text: what
 * lands in the clipboard should be what the agent actually said.
 */
export function CopyButton({ text, className }: { text: string; className?: string }) {
  const { t } = useI18n()
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (!copied) return
    const timer = setTimeout(() => setCopied(false), 1600)
    return () => clearTimeout(timer)
  }, [copied])

  const label = t(copied ? 'copied' : 'copy')
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={() => void navigator.clipboard.writeText(text).then(() => setCopied(true), () => undefined)}
      className={cn(
        'flex items-center rounded-[4px] p-1 text-ink-faint transition-colors hover:bg-sunken hover:text-ink-soft',
        className,
      )}
    >
      {copied ? <Check size={13} strokeWidth={1.8} /> : <Copy size={13} strokeWidth={1.6} />}
    </button>
  )
}
