import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { TerminalControl, TerminalSearchOptions } from '../terminalControl'
import { useI18n } from '../i18n'

// Floating in-terminal search bar (Ctrl+F). Searches the visible (composed)
// buffer via xterm addon-search; Enter/Shift+Enter step matches, Esc closes.
// Case-sensitivity / whole-word / highlight-all toggles persist across sessions.

const OPTS_KEY = 'session-insight-terminal-search-opts'
const MIN_BESIDE_PANEL_WIDTH = 360
// Debounce typing so each keystroke does not start a new buffer walk.
// Enter/step still runs immediately.
const SEARCH_DEBOUNCE_MS = 200

function loadOpts(): TerminalSearchOptions {
  try {
    const raw = localStorage.getItem(OPTS_KEY)
    if (raw) {
      const o = JSON.parse(raw) as Partial<TerminalSearchOptions>
      return {
        caseSensitive: !!o.caseSensitive,
        wholeWord: !!o.wholeWord,
        regex: !!o.regex,
        // Deliberately not persisted: highlight-all defaults off so long
        // sessions stay responsive (selection still marks the active hit).
        highlightAll: false,
      }
    }
  } catch { /* corrupted storage → defaults */ }
  return { caseSensitive: false, wholeWord: false, regex: false, highlightAll: false }
}

function isInvalidRegex(query: string, regexOn: boolean): boolean {
  if (!regexOn || !query) return false
  try { new RegExp(query); return false } catch { return true }
}

interface Props {
  controlRef: React.MutableRefObject<TerminalControl | null>
  /** Bumped on fold rewrites — the buffer was replaced, so re-run the search. */
  refreshToken: number
  /** Bumped on every Ctrl+F — refocus the input even when already open. */
  focusToken: number
  /** Width occupied by a pinned navigation panel on the right. */
  rightInset?: number
  onClose: () => void
}

export default function TerminalSearchBar({ controlRef, refreshToken, focusToken, rightInset = 0, onClose }: Props) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [opts, setOpts] = useState<TerminalSearchOptions>(loadOpts)
  const [result, setResult] = useState<{ index: number; count: number } | null>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const barRef = useRef<HTMLDivElement>(null)
  const [containerWidth, setContainerWidth] = useState<number | null>(null)
  const queryRef = useRef('')
  queryRef.current = query
  const optsRef = useRef(opts)
  optsRef.current = opts

  // Refocus (and select the query for quick replacement) on every Ctrl+F,
  // including when the bar is already open but focus wandered off.
  useEffect(() => {
    inputRef.current?.focus()
    inputRef.current?.select()
  }, [focusToken])

  useLayoutEffect(() => {
    const container = barRef.current?.parentElement
    if (!container) return
    const reportWidth = () => setContainerWidth(container.getBoundingClientRect().width)
    reportWidth()
    const observer = new ResizeObserver(reportWidth)
    observer.observe(container)
    return () => observer.disconnect()
  }, [])

  useEffect(() => {
    const ctrl = controlRef.current
    ctrl?.setSearchResultsListener((index, count) => {
      // count < 0 → still counting (show blank, not "No results")
      // count === 0 → confirmed no matches
      // count > 0 → n/m available
      setResult(count < 0 ? null : { index, count })
    })
    return () => {
      ctrl?.setSearchResultsListener(null)
      ctrl?.searchClear()
    }
  }, [controlRef])

  useEffect(() => {
    localStorage.setItem(OPTS_KEY, JSON.stringify(opts))
  }, [opts])

  // Highlight-all is CSS-only: do not re-scan / re-decorate the buffer.
  useEffect(() => {
    controlRef.current?.setSearchHighlightAll(opts.highlightAll)
  }, [opts.highlightAll, controlRef])

  const invalidRegex = isInvalidRegex(query, opts.regex)

  // Incremental search on typing / match-option change; re-run after fold rewrites.
  // Debounce query typing; option toggles and fold refresh use the same path so
  // a pending typed query still lands with the latest options.
  useEffect(() => {
    const ctrl = controlRef.current
    if (!ctrl) return
    if (!query || isInvalidRegex(query, opts.regex)) {
      ctrl.searchClear()
      setResult(null)
      return
    }
    const handle = window.setTimeout(() => {
      ctrl.searchNext(query, optsRef.current)
    }, SEARCH_DEBOUNCE_MS)
    return () => clearTimeout(handle)
    // highlightAll intentionally omitted — handled by setSearchHighlightAll above.
    // eslint-disable-next-line react-hooks/exhaustive-deps -- optsRef carries latest full opts
  }, [query, opts.caseSensitive, opts.wholeWord, opts.regex, refreshToken, controlRef])

  const step = (dir: 1 | -1) => {
    const ctrl = controlRef.current
    const q = queryRef.current
    if (!ctrl || !q) return
    if (dir === 1) ctrl.searchNext(q, optsRef.current)
    else ctrl.searchPrev(q, optsRef.current)
  }

  const jump = (edge: 'first' | 'last') => {
    const ctrl = controlRef.current
    const q = queryRef.current
    if (!ctrl || !q) return
    if (edge === 'first') ctrl.searchFirst(q, optsRef.current)
    else ctrl.searchLast(q, optsRef.current)
  }

  const toggle = (key: keyof TerminalSearchOptions) =>
    setOpts(prev => ({ ...prev, [key]: !prev[key] }))

  const toggleCls = (on: boolean) =>
    `h-6 min-w-6 flex-none rounded px-1 text-meta ${on
      ? 'bg-[var(--accent-blue)] text-white shadow-[inset_0_2px_3px_rgba(0,0,0,0.35)]'
      : 'text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'}`

  const pinnedPanelGap = 8
  const fitsBesidePanel = rightInset > 0
    && containerWidth !== null
    && containerWidth - rightInset - pinnedPanelGap * 2 >= MIN_BESIDE_PANEL_WIDTH
  const insetStyle = fitsBesidePanel
    ? {
        right: rightInset + pinnedPanelGap,
        maxWidth: `calc(100% - ${rightInset + pinnedPanelGap * 2}px)`,
      }
    : undefined

  return (
    <div
      ref={barRef}
      data-testid="terminal-search-bar"
      data-layout={rightInset > 0 ? (fitsBesidePanel ? 'beside' : 'overlay') : 'default'}
      className="absolute right-14 top-2 z-20 flex items-center gap-1 rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] px-2 py-1 shadow-md"
      style={insetStyle}
    >
      <input
        ref={inputRef}
        value={query}
        onChange={e => setQuery(e.target.value)}
        onKeyDown={e => {
          if (e.key === 'Enter') { e.preventDefault(); step(e.shiftKey ? -1 : 1) }
          else if (e.key === 'Escape') { e.preventDefault(); onClose() }
        }}
        placeholder={t('terminalSearch.placeholder')}
        className="h-6 min-w-24 w-44 border-none bg-transparent text-helper text-[var(--text-primary)] placeholder-[var(--text-muted)] focus:outline-none"
      />
      <button onClick={() => toggle('caseSensitive')} title={t('terminalSearch.caseSensitive')} aria-pressed={opts.caseSensitive} className={toggleCls(opts.caseSensitive)}>Aa</button>
      <button onClick={() => toggle('wholeWord')} title={t('terminalSearch.wholeWord')} aria-pressed={opts.wholeWord} className={toggleCls(opts.wholeWord)}><span className="underline underline-offset-2">wd</span></button>
      <button onClick={() => toggle('regex')} title={t('terminalSearch.regex')} aria-pressed={opts.regex} className={toggleCls(opts.regex)}>.*</button>
      <button onClick={() => toggle('highlightAll')} title={t('terminalSearch.highlightAll')} aria-pressed={opts.highlightAll} className={toggleCls(opts.highlightAll)}>{t('terminalSearch.highlightAllShort')}</button>
      <span data-testid="terminal-search-count" className={`min-w-[52px] flex-none text-right text-meta tabular-nums ${invalidRegex ? 'text-[var(--error)]' : 'text-[var(--text-muted)]'}`}>
        {invalidRegex
          ? t('terminalSearch.invalidRegex')
          : query
            ? (result
              ? (result.count > 0
                ? (result.index >= 0 ? `${result.index + 1}/${result.count}` : `${result.count}`)
                : t('terminalSearch.noResults'))
              : '') // null = counting or not yet reported
            : ''}
      </span>
      <button
        type="button"
        data-testid="terminal-search-first"
        onClick={() => jump('first')}
        title={t('terminalSearch.first')}
        aria-label={t('terminalSearch.first')}
        className="h-6 w-6 flex-none rounded text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]"
      >
        <span className="mx-auto flex h-4 w-3.5 flex-col items-center justify-center gap-px leading-none" aria-hidden>
          <span className="block h-[2px] w-3 rounded-[1px] bg-current" />
          <span className="text-[11px] leading-none">↑</span>
        </span>
      </button>
      <button
        type="button"
        data-testid="terminal-search-prev"
        onClick={() => step(-1)}
        title={t('terminalSearch.previous')}
        aria-label={t('terminalSearch.previous')}
        className="h-6 w-6 flex-none rounded text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]"
      >↑</button>
      <button
        type="button"
        data-testid="terminal-search-next"
        onClick={() => step(1)}
        title={t('terminalSearch.next')}
        aria-label={t('terminalSearch.next')}
        className="h-6 w-6 flex-none rounded text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]"
      >↓</button>
      <button
        type="button"
        data-testid="terminal-search-last"
        onClick={() => jump('last')}
        title={t('terminalSearch.last')}
        aria-label={t('terminalSearch.last')}
        className="h-6 w-6 flex-none rounded text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]"
      >
        <span className="mx-auto flex h-4 w-3.5 flex-col items-center justify-center gap-px leading-none" aria-hidden>
          <span className="text-[11px] leading-none">↓</span>
          <span className="block h-[2px] w-3 rounded-[1px] bg-current" />
        </span>
      </button>
      <button
        type="button"
        onClick={onClose}
        title={t('terminalSearch.close')}
        className="h-6 w-6 flex-none rounded text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]"
      >✕</button>
    </div>
  )
}
