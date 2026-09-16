import type { AgentKind } from '@/store/protocol'
import { cn } from '@/lib/utils'

/**
 * Agent identity, and the only place a kind is allowed to change what is drawn.
 * Every vendor keeps its own colour so three threads in one list are told apart
 * at a glance; everything else about them is rendered from their descriptor.
 */
const marks: Record<AgentKind, { initials: string; className: string }> = {
  claude_code: { initials: 'CC', className: 'bg-claude-code/12 text-claude-code' },
  codex: { initials: 'CX', className: 'bg-codex/12 text-codex' },
  opencode: { initials: 'OC', className: 'bg-opencode/12 text-opencode' },
}

const fallback = { initials: '??', className: 'bg-sunken text-ink-soft' }

export function AgentMark({ kind, label, className }: { kind: AgentKind; label?: string; className?: string }) {
  const mark = marks[kind] ?? fallback
  return (
    <span
      title={label ?? kind}
      className={cn(
        'inline-flex h-5 w-6 shrink-0 items-center justify-center rounded-[3px] font-mono text-[0.625rem] font-medium tracking-tight',
        mark.className,
        className,
      )}
    >
      {mark.initials}
    </span>
  )
}

/** The bar down the left of a row, which is where run state lives. */
export function StateBar({ state }: { state: string }) {
  const tone =
    state === 'running' || state === 'starting' ? 'bg-running'
    : state === 'waiting_input' ? 'bg-waiting'
    : 'bg-transparent'
  return <span className={cn('absolute inset-y-1 left-0 w-[3px] rounded-r-sm', tone, state === 'running' && 'breathe')} />
}
