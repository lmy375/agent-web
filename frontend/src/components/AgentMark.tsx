import { Bot } from 'lucide-react'
import claudeCodeIcon from '@/assets/agents/claude-code.svg'
import codexIcon from '@/assets/agents/codex.svg'
import opencodeIcon from '@/assets/agents/opencode.svg'
import type { AgentKind } from '@/store/protocol'
import { cn } from '@/lib/utils'

/**
 * Agent identity, and the only place a kind is allowed to change what is drawn.
 * Icons are bundled locally; everything else about an agent is rendered from
 * its descriptor. Asset sources and licensing live in assets/agents/README.md.
 */
const marks: Record<AgentKind, { src: string; label: string }> = {
  claude_code: { src: claudeCodeIcon, label: 'Claude Code' },
  codex: { src: codexIcon, label: 'Codex' },
  opencode: { src: opencodeIcon, label: 'OpenCode' },
}

export function AgentMark({ kind, label, className }: { kind: AgentKind; label?: string; className?: string }) {
  const mark = marks[kind]
  const name = label ?? mark?.label ?? kind
  return (
    <span
      role="img"
      aria-label={name}
      title={name}
      className={cn(
        'inline-flex h-5 w-5 shrink-0 items-center justify-center text-ink-soft',
        className,
      )}
    >
      {mark
        ? <img src={mark.src} alt="" aria-hidden="true" className="h-full w-full object-contain" draggable={false} />
        : <Bot aria-hidden="true" className="h-full w-full" strokeWidth={1.5} />}
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
