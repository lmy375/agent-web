import type { DisplayError } from '@/i18n/core'
import { useI18n } from '@/i18n'
import { FolderOpen } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { api, type DirListing } from '@/lib/api'
import { homeRelative } from '@/lib/utils'
import { Button } from './ui/button'
import { Input } from './ui/field'

/** Browse the machine this server runs on, and pick where a thread will work.
 *  The list is a popover rather than a standing panel: the path is usually
 *  typed or already right, and browsing for it is the exception. */
export function DirectoryPicker({
  value, home, onChange,
}: {
  value: string
  home: string | null
  onChange: (path: string) => void
}) {
  const { t, formatError } = useI18n()
  const [listing, setListing] = useState<DirListing | null>(null)
  const [typed, setTyped] = useState(value)
  const [problem, setProblem] = useState<DisplayError | null>(null)
  const [browsing, setBrowsing] = useState(false)
  const root = useRef<HTMLDivElement>(null)

  useEffect(() => setTyped(value), [value])

  // Listed even while the popover is shut, because a typed path that cannot be
  // read should say so before the thread is started.
  useEffect(() => {
    let live = true
    api
      .dirs(value)
      .then((result) => live && (setListing(result), setProblem(null)))
      .catch((error: Error) => live && setProblem(error))
    return () => {
      live = false
    }
  }, [value])

  // Escape and clicks elsewhere close the list; neither may reach the dialog
  // this picker sits in and close that instead.
  useEffect(() => {
    if (!browsing) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      event.stopPropagation()
      setBrowsing(false)
    }
    const onPointer = (event: MouseEvent) => {
      if (!root.current?.contains(event.target as Node)) setBrowsing(false)
    }
    document.addEventListener('keydown', onKey, true)
    document.addEventListener('mousedown', onPointer, true)
    return () => {
      document.removeEventListener('keydown', onKey, true)
      document.removeEventListener('mousedown', onPointer, true)
    }
  }, [browsing])

  return (
    <div ref={root} className="relative">
      <div className="flex gap-1.5">
        <Input
          aria-label={t('workingDirectory')}
          value={typed}
          spellCheck={false}
          onChange={(e) => setTyped(e.target.value)}
          onBlur={() => typed !== value && onChange(typed)}
          onKeyDown={(e) => e.key === 'Enter' && onChange(typed)}
          className="font-mono text-xs"
        />
        <Button
          size="icon"
          aria-label={t('browseDirectories')}
          title={t('browseDirectories')}
          aria-expanded={browsing}
          onClick={() => setBrowsing((open) => !open)}
        >
          <FolderOpen size={15} strokeWidth={1.5} />
        </Button>
      </div>
      <div className="mt-1.5 truncate font-mono text-[0.6875rem] text-ink-faint" title={value}>{homeRelative(value, home)}</div>
      {problem && <div className="mt-1.5 text-xs text-failed">{formatError(problem)}</div>}

      {browsing && (
        <div className="absolute left-0 right-0 top-9 z-10 rounded-[6px] border border-rule bg-surface shadow-lg">
          {/* Short viewports matter here: the dialog already starts 12vh down. */}
          <div className="max-h-[min(14rem,35vh)] overflow-y-auto py-1">
            {listing && listing.parent !== listing.path && (
              <Row label=".." onClick={() => onChange(listing.parent)} />
            )}
            {listing?.dirs.map((dir) => <Row key={dir.path} label={dir.name} onClick={() => onChange(dir.path)} />)}
            {listing && !listing.dirs.length && (
              <div className="px-3 py-2 text-xs text-ink-faint">{t('noSubdirectories')}</div>
            )}
          </div>
          <div className="flex items-center gap-2 border-t border-rule px-2 py-1.5">
            <span className="min-w-0 flex-1 truncate font-mono text-[0.6875rem] text-ink-faint" title={value}>
              {homeRelative(value, home)}
            </span>
            <Button size="sm" variant="solid" onClick={() => setBrowsing(false)}>{t('useThisDirectory')}</Button>
          </div>
        </div>
      )}
    </div>
  )
}

function Row({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <button onClick={onClick} className="block w-full truncate px-3 py-1 text-left font-mono text-xs hover:bg-sunken">
      {label}
    </button>
  )
}
