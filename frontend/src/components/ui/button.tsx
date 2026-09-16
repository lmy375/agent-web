import * as React from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

/**
 * Buttons are ink on paper. The signal hues are reserved for state, so an
 * action never competes with "this thread needs you".
 */
const button = cva(
  'inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-lg font-medium ' +
    'transition-[background-color,border-color,color] disabled:pointer-events-none disabled:opacity-40',
  {
    variants: {
      variant: {
        solid: 'bg-ink text-paper hover:bg-ink/85',
        outline: 'border border-rule bg-surface hover:bg-sunken',
        quiet: 'text-ink-soft hover:bg-sunken hover:text-ink',
        danger: 'border border-failed/40 text-failed hover:bg-failed/10',
      },
      size: {
        md: 'h-9 px-3 text-[0.8125rem]',
        sm: 'h-7 px-2 text-xs',
        icon: 'h-8 w-8',
      },
    },
    defaultVariants: { variant: 'outline', size: 'md' },
  },
)

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof button> {}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, ...props }, ref) => (
    <button ref={ref} className={cn(button({ variant, size }), className)} {...props} />
  ),
)
Button.displayName = 'Button'
