import { localeTag } from '@/i18n/core'
import { useI18n } from '@/i18n'
import { MessageSquare } from 'lucide-react'
import { useEffect, useRef } from 'react'
import type { Item } from '@/store/transcript'
import type { ImageBlock } from '@/store/protocol'
import { Markdown } from './blocks/Markdown'
import { CopyButton } from './blocks/CopyButton'
import { ActivityGroup } from './blocks/ActivityGroup'
import { presentTranscript } from '@/lib/transcriptPresentation'
import { Images } from './blocks/Images'
import { Button } from './ui/button'
import { cn } from '@/lib/utils'

export function Transcript({
  items, olderCursor, onLoadOlder, footer, active = false,
}: {
  items: Item[]
  active?: boolean
  olderCursor: string | null
  onLoadOlder: () => void
  footer?: React.ReactNode
}) {
  const { t } = useI18n()
  const bottom = useRef<HTMLDivElement>(null)
  const scroller = useRef<HTMLDivElement>(null)
  const stick = useRef(true)

  // Follow the stream only while the reader is already at the bottom; scrolling
  // up to read something is not an invitation to be yanked back.
  useEffect(() => {
    const node = scroller.current
    if (!node) return
    const onScroll = () => {
      stick.current = node.scrollHeight - node.scrollTop - node.clientHeight < 80
    }
    node.addEventListener('scroll', onScroll, { passive: true })
    return () => node.removeEventListener('scroll', onScroll)
  }, [])

  useEffect(() => {
    if (stick.current) bottom.current?.scrollIntoView({ block: 'end' })
  }, [items, footer])

  return (
    <div ref={scroller} className="min-h-0 flex-1 overflow-y-auto">
      <div className="conversation-width px-4 py-6 md:px-10 md:py-8">
        {olderCursor && (
          <div className="mb-6 flex justify-center">
            <Button size="sm" variant="quiet" onClick={onLoadOlder}>{t('loadEarlier')}</Button>
          </div>
        )}
        {!items.length && <div className="flex min-h-[35vh] flex-col items-center justify-center text-center">
          <MessageSquare size={28} strokeWidth={1.3} className="mb-4 text-ink-faint" />
          <h2 className="text-xl font-medium tracking-tight">{t('freshStart')}</h2>
          <p className="mt-2 text-sm text-ink-faint">{t('freshStartHint')}</p>
        </div>}
        {renderItems(items, active)}
        {footer}
        <div ref={bottom} />
      </div>
    </div>
  )
}

function renderItems(items: Item[], active: boolean): React.ReactNode {
  return presentTranscript(items).map((row) => {
    if (row.kind === 'text') {
      return (
        <div key={row.id} className="group/message my-6 min-w-0 break-words text-[15px] leading-[1.85] md:text-base">
          <Markdown>{row.text}</Markdown>
          <MessageActions text={row.text} />
        </div>
      )
    }
    if (row.kind === 'activity') {
      return <ActivityGroup key={row.id} entries={row.entries} active={active} renderChildren={(children, childActive) => renderItems(children, childActive)} />
    }
    return <ItemView key={row.id} item={row.item} />
  })
}

/** Quiet until the message is pointed at, so a long transcript stays prose. */
function MessageActions({ text, className }: { text: string; className?: string }) {
  if (!text) return null
  return (
    <div className={cn('mt-1 flex opacity-0 transition-opacity group-hover/message:opacity-100 focus-within:opacity-100', className)}>
      <CopyButton text={text} />
    </div>
  )
}

function ItemView({ item }: { item: Exclude<Item, { kind: 'assistant' }> }) {
  const { t, locale, formatError } = useI18n()
  switch (item.kind) {
    case 'user': {
      const text = item.blocks.filter((b) => b.type === 'text').map((b) => b.text).join('\n\n')
      const images = item.blocks.filter((b): b is ImageBlock => b.type === 'image')
      return (
        <div className="group/message my-6 ml-auto w-fit max-w-[90%]">
          <div className="rounded-2xl bg-paper px-5 py-3.5">
            {text && <div className="whitespace-pre-wrap break-words text-[0.9375rem] leading-relaxed">{text}</div>}
            <Images blocks={images} />
          </div>
          <MessageActions text={text} className="justify-end" />
        </div>
      )
    }

    case 'note':
      return (
        <div className="my-5 flex items-center gap-3 text-[0.6875rem] text-ink-faint">
          <span className="h-px flex-1 bg-rule" />
          <span>{item.contextBoundary ? (item.tokensBefore ? t('contextCompactedCount', { count: item.tokensBefore.toLocaleString(localeTag[locale]) }) : t('contextCompacted')) : item.label}</span>
          <span className="h-px flex-1 bg-rule" />
        </div>
      )

    case 'alert':
      return (
        <div className={cn('my-4 rounded-[6px] border px-3 py-2 text-[0.8125rem]', 'border-failed/35 bg-failed/[0.06] text-failed')}>
          <span className="font-mono text-[0.6875rem] opacity-70">{item.code}</span>
          <div className="mt-0.5 whitespace-pre-wrap">{formatError(Object.assign(new Error(item.message), { code: item.code }))}</div>
          {item.fatal && <div className="mt-1 text-[0.75rem] opacity-80">{t('reconnecting')}</div>}
        </div>
      )
  }
}
