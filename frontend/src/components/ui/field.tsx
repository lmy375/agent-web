import * as React from 'react'
import { Check, ChevronDown } from 'lucide-react'
import { useEffect, useId, useRef, useState } from 'react'
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

export interface Choice {
  value: string
  label: string
  description?: string | null
}

/** A control that reads as text until you reach for it: the composer's row of
 *  knobs should not look like three form fields. Where a knob does belong in a
 *  form — the new-thread dialog — `field` gives it the same frame as `Input`.
 *
 *  It is a listbox rather than a `<select>` because the native menu is the
 *  operating system's, not this page's: it cannot show a choice's description,
 *  cannot mark the current one, and looks like nothing else here. Focus stays
 *  on the trigger and the active option is named by `aria-activedescendant`,
 *  which is the ARIA combobox pattern and needs no focus trap. */
export function Picker({
  value, onChange, options, title, variant = 'text', className,
}: {
  value: string
  onChange: (value: string) => void
  options: Choice[]
  title?: string
  variant?: 'text' | 'field'
  className?: string
}) {
  const field = variant === 'field'
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(0)
  /** Where the menu goes, decided once per opening from where the trigger sits. */
  const [place, setPlace] = useState({ above: false, right: false })
  const root = useRef<HTMLDivElement>(null)
  const listID = useId()
  const current = options.findIndex((option) => option.value === value)
  const selected = current >= 0 ? options[current] : undefined

  useEffect(() => {
    if (!open) return
    const away = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false)
    }
    document.addEventListener('pointerdown', away)
    return () => document.removeEventListener('pointerdown', away)
  }, [open])

  // Keyboard moves the highlight without scrolling the page, so the menu has to
  // follow it itself.
  useEffect(() => {
    if (open) root.current?.querySelector('[data-active="true"]')?.scrollIntoView({ block: 'nearest' })
  }, [active, open])

  function toggle() {
    if (open) return setOpen(false)
    const rect = root.current?.getBoundingClientRect()
    // The composer sits at the bottom of the window and its menus have to open
    // upwards; a dialog's have room below. Same for a trigger near the right
    // edge, whose menu is wider than it is.
    const height = Math.min(options.length * (options.some((o) => o.description) ? 48 : 32) + 8, 264)
    setPlace({
      above: !!rect && rect.bottom + height + 8 > window.innerHeight && rect.top > height,
      right: !!rect && rect.left > window.innerWidth / 2,
    })
    setActive(current < 0 ? 0 : current)
    setOpen(true)
  }

  function pick(next: string) {
    setOpen(false)
    if (next !== value) onChange(next)
  }

  function onKeyDown(event: React.KeyboardEvent) {
    if (!open) {
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp' || event.key === 'Enter' || event.key === ' ') {
        event.preventDefault()
        toggle()
      }
      return
    }
    if (event.key === 'Tab') return setOpen(false)
    if (event.key === 'Escape') return setOpen(false)
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      return options[active] && pick(options[active].value)
    }
    const moves: Record<string, number> = { ArrowDown: active + 1, ArrowUp: active - 1, Home: 0, End: options.length - 1 }
    if (event.key in moves) {
      event.preventDefault()
      setActive((moves[event.key] + options.length) % options.length)
    }
  }

  return (
    <div ref={root} className={cn('relative inline-flex min-w-0 max-w-full items-center', field && 'w-full', className)}>
      <button
        type="button"
        role="combobox"
        title={title}
        aria-label={title}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? listID : undefined}
        aria-activedescendant={open ? `${listID}-${active}` : undefined}
        onClick={toggle}
        onKeyDown={onKeyDown}
        className={cn(
          'flex min-w-0 max-w-full items-center gap-1 text-[0.8125rem] transition-colors',
          field
            ? 'h-8 w-full justify-between rounded-[5px] border border-rule bg-surface px-2 text-ink focus-visible:border-ink'
            : 'rounded-md bg-transparent py-1.5 pl-2 pr-1.5 text-ink-soft hover:bg-sunken hover:text-ink',
          open && !field && 'bg-sunken text-ink',
        )}
      >
        <span className="min-w-0 truncate">{selected?.label ?? value}</span>
        <ChevronDown size={13} strokeWidth={1.6} className={cn('shrink-0 text-ink-faint transition-transform', open && 'rotate-180')} aria-hidden="true" />
      </button>

      {open && (
        <ul
          id={listID}
          role="listbox"
          aria-label={title}
          className={cn(
            'absolute z-30 max-h-64 min-w-full max-w-[min(22rem,80vw)] overflow-y-auto rounded-[8px] border border-rule bg-surface py-1',
            'shadow-[0_10px_30px_-12px_#26262440]',
            place.above ? 'bottom-full mb-1.5' : 'top-full mt-1.5',
            place.right ? 'right-0' : 'left-0',
          )}
        >
          {options.map((option, index) => (
            <li
              key={option.value}
              id={`${listID}-${index}`}
              role="option"
              aria-selected={option.value === value}
              data-active={index === active}
              onPointerEnter={() => setActive(index)}
              onClick={() => pick(option.value)}
              className={cn(
                'flex cursor-pointer items-start gap-1.5 px-2 py-1.5 text-[0.8125rem] text-ink',
                index === active && 'bg-sunken',
              )}
            >
              <Check size={13} strokeWidth={2} className={cn('mt-1 shrink-0', option.value === value ? 'opacity-100' : 'opacity-0')} aria-hidden="true" />
              <span className="min-w-0">
                <span className="block truncate">{option.label}</span>
                {option.description && <span className="mt-0.5 block text-xs leading-snug text-ink-soft">{option.description}</span>}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
