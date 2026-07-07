import { useEffect, useRef, useState } from 'react'
import type { TerminalControl } from '../terminalControl'

// Floating in-terminal search bar (Ctrl+F). Searches the visible (composed)
// buffer via xterm addon-search; Enter/Shift+Enter step matches, Esc closes.

interface Props {
  controlRef: React.MutableRefObject<TerminalControl | null>
  /** Bumped on fold rewrites — the buffer was replaced, so re-run the search. */
  refreshToken: number
  onClose: () => void
}

export default function TerminalSearchBar({ controlRef, refreshToken, onClose }: Props) {
  const [query, setQuery] = useState('')
  const [result, setResult] = useState<{ index: number; count: number } | null>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const queryRef = useRef('')
  queryRef.current = query

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

  // Incremental search on typing; re-run after fold rewrites replaced the buffer.
  useEffect(() => {
    const ctrl = controlRef.current
    if (!ctrl) return
    if (!query) {
      ctrl.searchClear()
      setResult(null)
      return
    }
    ctrl.searchNext(query)
  }, [query, refreshToken, controlRef])

  const step = (dir: 1 | -1) => {
    const ctrl = controlRef.current
    const q = queryRef.current
    if (!ctrl || !q) return
    if (dir === 1) ctrl.searchNext(q)
    else ctrl.searchPrev(q)
  }

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
        className="h-6 w-48 border-none bg-transparent text-helper text-[var(--text-primary)] placeholder-[var(--text-muted)] focus:outline-none"
      />
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
