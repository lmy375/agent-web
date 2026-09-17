import { effortLabel, LocalizedError, type DisplayError } from '@/i18n/core'
import { useI18n } from '@/i18n'
import { ArrowUp, CornerDownLeft, Plus, Square } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import type { AgentDescriptor, ContextUsage, PermissionMode, ThreadSummary, TurnSummary } from '@/store/protocol'
import { api } from '@/lib/api'
import { imageFiles, toAttachment, toBlock, attachmentSrc, type Attachment } from '@/lib/images'
import { ContextMeter } from './ContextMeter'
import { Button } from './ui/button'
import { Picker } from './ui/field'
import { cn } from '@/lib/utils'

interface ComposerProps {
  thread: ThreadSummary
  descriptor: AgentDescriptor | undefined
  busy: boolean
  /** Resolves false when the prompt never reached the harness. */
  onPrompt: (text: string, images: ReturnType<typeof toBlock>[]) => Promise<boolean>
  onSteer: (text: string) => void
  onInterrupt: () => void
  onOptions: (options: { model?: string; mode?: PermissionMode; effort?: string }) => void
  usage: ContextUsage | null
  lastTurn: TurnSummary | null
}

/** What `/` and `@` are completing right now. */
type Menu = { kind: 'slash' | 'file'; query: string; from: number } | null

export function Composer({ thread, descriptor, busy, onPrompt, onSteer, onInterrupt, onOptions, usage, lastTurn }: ComposerProps) {
  const { t, locale, formatError } = useI18n()
  const [text, setText] = useState('')
  const [attachments, setAttachments] = useState<Attachment[]>([])
  const [problem, setProblem] = useState<DisplayError | null>(null)
  const [menu, setMenu] = useState<Menu>(null)
  const [files, setFiles] = useState<string[]>([])
  const [highlight, setHighlight] = useState(0)
  const fileInput = useRef<HTMLInputElement>(null)
  const input = useRef<HTMLTextAreaElement>(null)

  const capabilities = descriptor?.capabilities
  const runtime = descriptor?.runtime
  const canSteer = capabilities?.supports_steer === true && busy

  // The textarea grows with the prompt and stops before it eats the transcript.
  useEffect(() => {
    const node = input.current
    if (!node) return
    node.style.height = 'auto'
    node.style.height = `${Math.min(node.scrollHeight, 320)}px`
  }, [text])

  const commands = useMemo(() => {
    if (menu?.kind !== 'slash') return []
    const query = menu.query.toLowerCase()
    return (runtime?.commands ?? []).filter((c) => c.name.toLowerCase().includes(query)).slice(0, 8)
  }, [menu, runtime])

  useEffect(() => {
    if (menu?.kind !== 'file' || menu.query.length < 1) {
      setFiles([])
      return
    }
    let live = true
    const timer = setTimeout(async () => {
      const result = await api.searchFiles(thread.thread_id, menu.query).catch(() => null)
      if (live && result) setFiles(result.paths)
    }, 120)
    return () => {
      live = false
      clearTimeout(timer)
    }
  }, [menu, thread.thread_id])

  useEffect(() => setHighlight(0), [menu?.query, menu?.kind])

  /** A menu opens on a `/` at the very start, or on an `@` anywhere. */
  function readMenu(value: string, caret: number) {
    const before = value.slice(0, caret)
    const slash = /^\/([\w:-]*)$/.exec(before)
    if (slash) return setMenu({ kind: 'slash', query: slash[1], from: 0 })
    const at = /(^|\s)@([^\s]*)$/.exec(before)
    if (at) return setMenu({ kind: 'file', query: at[2], from: caret - at[2].length - 1 })
    setMenu(null)
  }

  function accept(value: string) {
    const node = input.current
    if (!node || !menu) return
    const caret = node.selectionStart
    const head = text.slice(0, menu.from)
    const tail = text.slice(caret)
    const inserted = menu.kind === 'slash' ? `/${value} ` : `@${value} `
    setText(head + inserted + tail)
    setMenu(null)
    queueMicrotask(() => {
      node.focus()
      const at = head.length + inserted.length
      node.setSelectionRange(at, at)
    })
  }

  async function attach(list: File[]) {
    const room = (capabilities?.max_images_per_prompt ?? 0) - attachments.length
    if (room <= 0) return setProblem(new LocalizedError('imageLimit', { agent: descriptor?.label ?? thread.agent_kind, count: capabilities?.max_images_per_prompt ?? 0 }))
    for (const file of list.slice(0, room)) {
      try {
        const attachment = await toAttachment(file)
        setAttachments((current) => [...current, attachment])
      } catch (error) {
        setProblem(error instanceof Error ? error : new LocalizedError('imageReadFailed'))
      }
    }
  }

  async function submit() {
    if (busy && !canSteer) return
    const body = text.trim()
    if (!body && !attachments.length) return
    if (canSteer && attachments.length) return setProblem(new LocalizedError('imageWait'))
    const sent = attachments
    setText('')
    setAttachments([])
    setMenu(null)
    if (canSteer && body) return onSteer(body)
    if (await onPrompt(body, sent.map(toBlock))) return
    // The prompt never left, so hand the typing back rather than make the
    // owner write it again -- unless they have already started something new.
    setText((current) => current || body)
    setAttachments((current) => (current.length ? current : sent))
  }

  const suggestions = menu?.kind === 'slash' ? commands.map((c) => ({ value: c.name, hint: c.description })) : files.map((p) => ({ value: p, hint: '' }))

  function onKeyDown(event: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (menu && suggestions.length) {
      if (event.key === 'ArrowDown') {
        event.preventDefault()
        return setHighlight((h) => (h + 1) % suggestions.length)
      }
      if (event.key === 'ArrowUp') {
        event.preventDefault()
        return setHighlight((h) => (h - 1 + suggestions.length) % suggestions.length)
      }
      if (event.key === 'Tab' || (event.key === 'Enter' && !event.shiftKey)) {
        event.preventDefault()
        return accept(suggestions[highlight].value)
      }
      if (event.key === 'Escape') return setMenu(null)
    }
    if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
      event.preventDefault()
      void submit()
    }
  }

  const modeOptions = (capabilities?.modes ?? []).map((mode) => ({ value: mode, label: t(`mode.${mode}`) }))
  const effortOptions = (capabilities?.efforts ?? []).map((e) => ({ value: e.id, label: effortLabel(locale, e.id, e.label) }))
  const modelOptions = (runtime?.models ?? []).map((m) => ({ value: m.id, label: m.label === 'Default (recommended)' ? t('modelDefault') : m.label }))

  return (
    <div className="shrink-0 bg-surface">
      <div className="conversation-width px-4 pb-3 pt-2 md:px-10 md:pb-4">
        {problem && (
          <div className="mb-2 flex items-center justify-between rounded-[4px] bg-failed/10 px-2 py-1 text-xs text-failed">
            {formatError(problem)}
            <button onClick={() => setProblem(null)} className="opacity-70 hover:opacity-100">{t('dismiss')}</button>
          </div>
        )}

        {attachments.length > 0 && (
          <div className="mb-2 flex flex-wrap gap-1.5">
            {attachments.map((a) => (
              <button
                key={a.id}
                onClick={() => setAttachments((current) => current.filter((x) => x.id !== a.id))}
                title={t('removeImage', { name: a.name || t('pastedImage') })}
                className="group relative"
              >
                <img src={attachmentSrc(a)} alt={a.name || t('pastedImage')} className="h-12 w-12 rounded-[4px] border border-rule object-cover" />
                <span className="absolute inset-0 hidden items-center justify-center rounded-[4px] bg-ink/70 text-xs text-paper group-hover:flex">{t('remove')}</span>
              </button>
            ))}
          </div>
        )}

        <div className="relative rounded-2xl border border-ink-faint/45 bg-surface shadow-[0_3px_16px_-5px_#26262418] transition-[border-color,box-shadow] focus-within:border-ink-faint focus-within:shadow-[0_3px_20px_-5px_#26262425]">
          {menu && suggestions.length > 0 && (
            <ul className="absolute bottom-full left-0 z-10 mb-1 max-h-64 w-full overflow-y-auto rounded-[6px] border border-rule bg-surface py-1 shadow-sm">
              {suggestions.map((s, i) => (
                <li key={s.value}>
                  <button
                    onMouseDown={(e) => {
                      e.preventDefault()
                      accept(s.value)
                    }}
                    onMouseEnter={() => setHighlight(i)}
                    className={cn(
                      'flex w-full items-baseline gap-2 px-3 py-1 text-left',
                      i === highlight && 'bg-sunken',
                    )}
                  >
                    <span className="font-mono text-xs text-ink">{s.value}</span>
                    {s.hint && <span className="truncate text-xs text-ink-faint">{s.hint}</span>}
                  </button>
                </li>
              ))}
            </ul>
          )}

          <textarea
            ref={input}
            rows={1}
            aria-label={t('message')}
            value={text}
            placeholder={canSteer ? t('steerPlaceholder') : t('promptPlaceholder', { agent: descriptor?.label ?? t('agent') })}
            onChange={(e) => {
              setText(e.target.value)
              readMenu(e.target.value, e.target.selectionStart)
            }}
            onKeyDown={onKeyDown}
            onPaste={(e) => {
              const images = imageFiles(e.clipboardData)
              if (images.length) {
                e.preventDefault()
                void attach(images)
              }
            }}
            onDrop={(e) => {
              const images = imageFiles(e.dataTransfer)
              if (images.length) {
                e.preventDefault()
                void attach(images)
              }
            }}
            className="block min-h-16 w-full resize-none bg-transparent py-5 pl-4 pr-14 text-[15px] leading-relaxed text-ink placeholder:text-ink-faint focus-visible:outline-none md:pl-5"
          />

          <div className="absolute bottom-3 right-3">
            {busy && !canSteer && capabilities?.supports_interrupt !== false ? (
              <Button size="icon" onClick={onInterrupt} aria-label={t('stopResponse')} title={t('stopResponse')} className="rounded-full"><Square size={13} fill="currentColor" /></Button>
            ) : (
              <Button size="icon" variant="solid" className="rounded-full" disabled={(!text.trim() && !attachments.length) || (busy && !canSteer)} onClick={() => void submit()} aria-label={canSteer ? t('steer') : t('sendMessage')} title={canSteer ? t('steer') : t('sendMessage')}><ArrowUp size={18} /></Button>
            )}
          </div>
        </div>

        <div className="mt-2 flex flex-wrap items-center gap-x-1 gap-y-2 px-1">
          {modeOptions.length > 0 && <Picker title={t('permissionMode')} value={thread.options.mode ?? ''} options={modeOptions} onChange={(mode) => onOptions({ mode: mode as PermissionMode })} />}
          {!!capabilities?.max_images_per_prompt && (
            <>
              <input ref={fileInput} type="file" accept="image/*" multiple className="hidden" onChange={(event) => { void attach(Array.from(event.target.files ?? [])); event.target.value = '' }} />
              <Button variant="quiet" size="icon" onClick={() => fileInput.current?.click()} aria-label={t('attachImages')} title={t('attachImages')}><Plus size={18} strokeWidth={1.5} /></Button>
            </>
          )}
          <span className="ml-1 hidden items-center gap-1 text-[11px] text-ink-faint xl:flex"><CornerDownLeft size={12} />{t('enterToSend')}</span>
          <ContextMeter usage={usage} lastTurn={lastTurn} className="ml-1" />
          <div className="flex-1" />
          {modelOptions.length > 0 && <Picker title={t('model')} value={thread.options.model ?? ''} options={modelOptions} onChange={(model) => onOptions({ model })} />}
          {effortOptions.length > 0 && <Picker title={t('effort')} value={thread.options.effort ?? ''} options={effortOptions} onChange={(effort) => onOptions({ effort })} />}
          {canSteer && capabilities?.supports_interrupt !== false && <Button size="sm" variant="quiet" onClick={onInterrupt}><Square size={11} />{t('stop')}</Button>}
        </div>
      </div>
    </div>
  )
}
