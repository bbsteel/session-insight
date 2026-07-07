import { useEffect, useRef, useState } from 'react'
import type { TerminalControl, TerminalSearchOptions } from '../terminalControl'

// Floating in-terminal search bar (Ctrl+F). Searches the visible (composed)
// buffer via xterm addon-search; Enter/Shift+Enter step matches, Esc closes.
// Case-sensitivity / whole-word / highlight-all toggles persist across sessions.

const OPTS_KEY = 'session-insight-terminal-search-opts'

function loadOpts(): TerminalSearchOptions {
  try {
    const raw = localStorage.getItem(OPTS_KEY)
    if (raw) {
      const o = JSON.parse(raw) as Partial<TerminalSearchOptions>
      return {
        caseSensitive: !!o.caseSensitive,
        wholeWord: !!o.wholeWord,
        highlightAll: o.highlightAll !== false,
      }
    }
  } catch { /* corrupted storage → defaults */ }
  return { caseSensitive: false, wholeWord: false, highlightAll: true }
}

interface Props {
  controlRef: React.MutableRefObject<TerminalControl | null>
  /** Bumped on fold rewrites — the buffer was replaced, so re-run the search. */
  refreshToken: number
  onClose: () => void
}

export default function TerminalSearchBar({ controlRef, refreshToken, onClose }: Props) {
  const [query, setQuery] = useState('')
  const [opts, setOpts] = useState<TerminalSearchOptions>(loadOpts)
  const [result, setResult] = useState<{ index: number; count: number } | null>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const queryRef = useRef('')
  queryRef.current = query
  const optsRef = useRef(opts)
  optsRef.current = opts

  useEffect(() => {
    inputRef.current?.focus()
    const ctrl = controlRef.current
    ctrl?.setSearchResultsListener((index, count) => {
      setResult(count >= 0 ? { index, count } : null)
    })
    return () => {
      ctrl?.setSearchResultsListener(null)
      ctrl?.searchClear()
    }
  }, [controlRef])

  useEffect(() => {
    localStorage.setItem(OPTS_KEY, JSON.stringify(opts))
  }, [opts])

  // Incremental search on typing / option change; re-run after fold rewrites.
  useEffect(() => {
    const ctrl = controlRef.current
    if (!ctrl) return
    if (!query) {
      ctrl.searchClear()
      setResult(null)
      return
    }
    ctrl.searchNext(query, opts)
  }, [query, opts, refreshToken, controlRef])

  const step = (dir: 1 | -1) => {
    const ctrl = controlRef.current
    const q = queryRef.current
    if (!ctrl || !q) return
    if (dir === 1) ctrl.searchNext(q, optsRef.current)
    else ctrl.searchPrev(q, optsRef.current)
  }

  const toggle = (key: keyof TerminalSearchOptions) =>
    setOpts(prev => ({ ...prev, [key]: !prev[key] }))

  const toggleCls = (on: boolean) =>
    `h-6 min-w-6 rounded px-1 text-meta ${on
      ? 'bg-[var(--accent-blue)]/20 text-[var(--accent-blue)]'
      : 'text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'}`

  return (
    <div className="absolute right-14 top-2 z-20 flex items-center gap-1 rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] px-2 py-1 shadow-md">
      <input
        ref={inputRef}
        value={query}
        onChange={e => setQuery(e.target.value)}
        onKeyDown={e => {
          if (e.key === 'Enter') { e.preventDefault(); step(e.shiftKey ? -1 : 1) }
          else if (e.key === 'Escape') { e.preventDefault(); onClose() }
        }}
        placeholder="在终端中查找"
        className="h-6 w-44 border-none bg-transparent text-helper text-[var(--text-primary)] placeholder-[var(--text-muted)] focus:outline-none"
      />
      <button onClick={() => toggle('caseSensitive')} title="区分大小写" aria-pressed={opts.caseSensitive} className={toggleCls(opts.caseSensitive)}>Aa</button>
      <button onClick={() => toggle('wholeWord')} title="全词匹配" aria-pressed={opts.wholeWord} className={toggleCls(opts.wholeWord)}>|ab|</button>
      <button onClick={() => toggle('highlightAll')} title="高亮全部命中" aria-pressed={opts.highlightAll} className={toggleCls(opts.highlightAll)}>≡</button>
      <span className="min-w-[52px] text-right text-meta tabular-nums text-[var(--text-muted)]">
        {query ? (result && result.count > 0 ? `${result.index + 1}/${result.count}` : '无结果') : ''}
      </span>
      <button
        onClick={() => step(-1)}
        title="上一个 (Shift+Enter)"
        className="h-6 w-6 rounded text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]"
      >↑</button>
      <button
        onClick={() => step(1)}
        title="下一个 (Enter)"
        className="h-6 w-6 rounded text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]"
      >↓</button>
      <button
        onClick={onClose}
        title="关闭 (Esc)"
        className="h-6 w-6 rounded text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]"
      >✕</button>
    </div>
  )
}
