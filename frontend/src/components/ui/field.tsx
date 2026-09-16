import * as React from 'react'
import { cn } from '@/lib/utils'

const shell =
  'w-full rounded-[5px] border border-rule bg-surface px-2 text-[0.8125rem] text-ink ' +
  'placeholder:text-ink-faint focus-visible:border-ink'

export const Input = React.forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(
  ({ className, ...props }, ref) => <input ref={ref} className={cn(shell, 'h-8', className)} {...props} />,
)
Input.displayName = 'Input'

export const Textarea = React.forwardRef<HTMLTextAreaElement, React.TextareaHTMLAttributes<HTMLTextAreaElement>>(
  ({ className, ...props }, ref) => <textarea ref={ref} className={cn(shell, 'py-1.5 resize-none', className)} {...props} />,
)
Textarea.displayName = 'Textarea'

/** A control that reads as text until you reach for it: the composer's row of
 *  knobs should not look like three form fields. Where a knob does belong in a
 *  form — the new-thread dialog — `field` gives it the same frame as `Input`. */
export function Picker({
  value, onChange, options, title, variant = 'text', className,
}: {
  value: string
  onChange: (value: string) => void
  options: { value: string; label: string }[]
  title?: string
  variant?: 'text' | 'field'
  className?: string
}) {
  const field = variant === 'field'
  return (
    <div className={cn('relative inline-flex min-w-0 max-w-full items-center', field && 'w-full', className)}>
      <select
        title={title}
        aria-label={title}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className={cn(
          'appearance-none max-w-full text-[0.8125rem]',
          field
            ? 'h-8 w-full rounded-[5px] border border-rule bg-surface pl-2 pr-6 text-ink focus-visible:border-ink'
            : 'rounded-md bg-transparent py-1.5 pl-2 pr-5 text-ink-soft hover:bg-sunken hover:text-ink focus-visible:bg-sunken',
        )}
      >
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
      <svg
        className={cn('pointer-events-none absolute h-2.5 w-2.5 text-ink-faint', field ? 'right-2' : 'right-1')}
        viewBox="0 0 10 6"
        aria-hidden
      >
        <path d="M1 1l4 4 4-4" fill="none" stroke="currentColor" strokeWidth="1.4" />
      </svg>
    </div>
  )
}
