import { useEffect, useRef, useState } from 'react'
import { fetchIndexStatus, fetchSearch, type IndexStatus } from '../api'
import type { SearchResult } from '../types'
import AgentIcon from './AgentIcon'
import AISettingsModal from './AISettingsModal'
import SettingsDialog from './SettingsDialog'
import { ThemeSwitch } from './ThemeToggle'
import { formatRelativeTime, useI18n, type Locale } from '../i18n'

const HISTORY_KEY = 'search-history'
const HISTORY_LIMIT_KEY = 'search-history-limit'
const DEFAULT_HISTORY_LIMIT = 10

interface HistoryEntry {
  query: string
  pinned: boolean
  ts: number
}

function readHistory(): HistoryEntry[] {
  try {
    const raw = localStorage.getItem(HISTORY_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter(
      (e): e is HistoryEntry => typeof e?.query === 'string' && typeof e?.ts === 'number',
    )
  } catch {
    return []
  }
}

function writeHistory(entries: HistoryEntry[]): void {
  try {
    localStorage.setItem(HISTORY_KEY, JSON.stringify(entries))
  } catch {
    // Storage is optional; keep the in-memory UI state working.
  }
}

function readHistoryLimit(): number {
  try {
    const n = parseInt(localStorage.getItem(HISTORY_LIMIT_KEY) ?? '', 10)
    if (Number.isFinite(n) && n >= 1 && n <= 50) return n
  } catch {
    // fall through to default
  }
  return DEFAULT_HISTORY_LIMIT
}

// 钉住的不占 x 条配额；未钉住的按时间保留最近 limit 条
function sortAndTrim(entries: HistoryEntry[], limit: number): HistoryEntry[] {
  const pinned = entries.filter(e => e.pinned).sort((a, b) => b.ts - a.ts)
  const unpinned = entries
    .filter(e => !e.pinned)
    .sort((a, b) => b.ts - a.ts)
    .slice(0, limit)
  return [...pinned, ...unpinned]
}

function relTime(iso: string, locale: Locale): string {
  if (!iso) return ''
  const diff = Date.now() - new Date(iso).getTime()
  if (!Number.isFinite(diff) || diff < 0) return ''
  const minutes = Math.floor(diff / 60000)
  if (minutes < 1) return formatRelativeTime(locale, 0)
  if (minutes < 60) return formatRelativeTime(locale, -minutes, 'minute')
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return formatRelativeTime(locale, -hours, 'hour')
  const days = Math.floor(hours / 24)
  if (days < 30) return formatRelativeTime(locale, -days, 'day')
  return new Date(iso).toLocaleDateString(locale)
}

function HighlightedSnippet({ text, query }: { text: string; query: string }) {
  const q = query.trim().toLowerCase()
  if (!q) return <>{text}</>
  const lower = text.toLowerCase()
  const parts: React.ReactNode[] = []
  let i = 0
  for (;;) {
    const idx = lower.indexOf(q, i)
    if (idx < 0) break
    if (idx > i) parts.push(text.slice(i, idx))
    parts.push(
      <mark
        key={idx}
        className="rounded-[3px] bg-[var(--accent-blue)]/15 px-0.5 font-medium text-[var(--accent-blue)]"
      >
        {text.slice(idx, idx + q.length)}
      </mark>,
    )
    i = idx + q.length
  }
  parts.push(text.slice(i))
  return <>{parts}</>
}

function PinIcon({ filled }: { filled?: boolean }) {
  return (
    <svg
      width="12"
      height="12"
      viewBox="0 0 24 24"
      fill={filled ? 'currentColor' : 'none'}
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M12 17v5" />
      <path d="M9 3h6l-1 7 3 2v2H7v-2l3-2-1-7z" />
    </svg>
  )
}

function ClockIcon() {
  return (
    <svg
      width="12"
      height="12"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v5l3 2" />
    </svg>
  )
}

export default function GlobalSearch({ onSelect }: { onSelect?: (id: string, agentType?: string, focusSidebar?: boolean, searchQuery?: string) => void }) {
  const { locale, t } = useI18n()
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<SearchResult[]>([])
  const [loading, setLoading] = useState(false)
  const [indexStatus, setIndexStatus] = useState<IndexStatus | null>(null)
  const [open, setOpen] = useState(false)
  const [activeIndex, setActiveIndex] = useState(-1)
  const [history, setHistory] = useState<HistoryEntry[]>([])
  const [historyLimit, setHistoryLimit] = useState(readHistoryLimit)
  const [showSettings, setShowSettings] = useState(false)
  const [showAISettings, setShowAISettings] = useState(false)
  // 侧边栏版本号点击 ⓘ 时经 si-open-settings 事件要求定位到「关于」Tab；
  // 关闭后复位，避免下次从齿轮按钮打开还停在「关于」。
  const [settingsInitialTab, setSettingsInitialTab] = useState<'about' | undefined>(undefined)
  const inputRef = useRef<HTMLInputElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const debounceRef = useRef<ReturnType<typeof setTimeout>>()

  // Other views (e.g. the AI panel's "去配置模型源" guidance) open the AI
  // provider modal through this window event — GlobalSearch is the one
  // component mounted in every layout state.
  useEffect(() => {
    const open = () => setShowAISettings(true)
    window.addEventListener('si-open-ai-settings', open)
    return () => window.removeEventListener('si-open-ai-settings', open)
  }, [])

  // 侧边栏 footer 的版本号/ⓘ 通过该事件打开设置弹窗（可指定定位 Tab）。
  useEffect(() => {
    const open = (e: Event) => {
      const tab = (e as CustomEvent<{ tab?: string }>).detail?.tab
      setSettingsInitialTab(tab === 'about' ? 'about' : undefined)
      setShowSettings(true)
    }
    window.addEventListener('si-open-settings', open)
    return () => window.removeEventListener('si-open-settings', open)
  }, [])

  const isMac = typeof navigator !== 'undefined' && /Mac|iPod|iPhone|iPad/.test(navigator.platform)
  const isHistoryMode = !query.trim()
  const visibleHistory = sortAndTrim(history, historyLimit)
  const listLength = isHistoryMode ? visibleHistory.length : results.length

  // Poll indexer progress: fast while running, slow when idle (to catch the next cycle).
  useEffect(() => {
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const tick = () => {
      fetchIndexStatus()
        .then(s => {
          if (cancelled) return
          setIndexStatus(s)
          const ms = s.state === 'running' ? 400 : 4000
          timer = setTimeout(tick, ms)
        })
        .catch(() => {
          if (cancelled) return
          timer = setTimeout(tick, 8000)
        })
    }
    tick()
    return () => {
      cancelled = true
      if (timer) clearTimeout(timer)
    }
  }, [])

  useEffect(() => {
    if (!query.trim()) {
      setResults([])
      setLoading(false)
      return
    }
    setLoading(true)
    debounceRef.current = setTimeout(() => {
      fetchSearch(query.trim())
        .then(data => { setResults(data); setOpen(true); setActiveIndex(-1) })
        .catch(() => setResults([]))
        .finally(() => setLoading(false))
    }, 250)
    return () => clearTimeout(debounceRef.current)
  }, [query])

  const indexing = indexStatus?.state === 'running'
  const indexLabel = indexing
    ? t('search.indexing', { percent: Math.min(100, Math.max(0, indexStatus?.percent ?? 0)) }) +
      (indexStatus && indexStatus.total > 0 ? ` (${indexStatus.done}/${indexStatus.total})` : '')
    : indexStatus?.message === 'completed_with_errors'
      ? t('search.indexCompleteWithErrors')
      : null
  const searchPlaceholder = indexing
    ? indexLabel!
    : t('search.placeholder', { shortcut: isMac ? '⌘K' : 'Ctrl+K' })
  // Polite live region: announce progress even when the dropdown is closed.
  const indexAriaLive = indexing
    ? indexLabel
    : indexStatus?.message === 'completed_with_errors'
      ? t('search.indexCompleteWithErrors')
      : indexStatus?.message === 'ready' && (indexStatus.total ?? 0) > 0
        ? t('search.indexReady')
        : ''

  useEffect(() => {
    const handleClick = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [])

  const persistHistory = (entries: HistoryEntry[]) => {
    const trimmed = sortAndTrim(entries, historyLimit)
    setHistory(trimmed)
    writeHistory(trimmed)
  }

  const recordQuery = (q: string) => {
    const trimmed = q.trim()
    if (!trimmed) return
    const existing = history.find(e => e.query === trimmed)
    const rest = history.filter(e => e.query !== trimmed)
    persistHistory([{ query: trimmed, pinned: existing?.pinned ?? false, ts: Date.now() }, ...rest])
  }

  const togglePin = (q: string) => {
    persistHistory(history.map(e => (e.query === q ? { ...e, pinned: !e.pinned } : e)))
  }

  const removeEntry = (q: string) => {
    persistHistory(history.filter(e => e.query !== q))
  }

  const clearHistory = () => {
    setHistory([])
    writeHistory([])
  }

  const updateLimit = (n: number) => {
    const clamped = Math.min(50, Math.max(1, Math.round(n) || DEFAULT_HISTORY_LIMIT))
    setHistoryLimit(clamped)
    try {
      localStorage.setItem(HISTORY_LIMIT_KEY, String(clamped))
    } catch {
      // Storage is optional
    }
    const trimmed = sortAndTrim(history, clamped)
    setHistory(trimmed)
    writeHistory(trimmed)
  }

  const openDropdown = () => {
    if (isHistoryMode) {
      const entries = readHistory()
      setHistory(entries)
      setActiveIndex(-1)
      if (entries.length > 0) setOpen(true)
    } else if (results.length > 0) {
      setOpen(true)
    }
  }

  const selectResult = (result: SearchResult) => {
    recordQuery(query)
    // Include the agent because session IDs are only unique within an agent.
    // It also lets the sidebar reveal and focus the exact result.
    onSelect?.(result.session_id, result.agent_type, true, query.trim())
    setOpen(false)
    setQuery('')
  }

  const selectHistory = (q: string) => {
    setQuery(q)
    inputRef.current?.focus()
  }

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      setOpen(false)
      inputRef.current?.blur()
      return
    }
    if (!open || listLength === 0) return
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setActiveIndex(i => Math.min(i + 1, listLength - 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActiveIndex(i => Math.max(i - 1, 0))
    } else if (e.key === 'Enter' && activeIndex >= 0) {
      e.preventDefault()
      if (isHistoryMode) selectHistory(visibleHistory[activeIndex].query)
      else selectResult(results[activeIndex])
    }
  }

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((isMac ? e.metaKey : e.ctrlKey) && e.key === 'k') { e.preventDefault(); inputRef.current?.focus() }
    }
    document.addEventListener('keydown', handler)
    return () => document.removeEventListener('keydown', handler)
  }, [isMac])

  return (
    <>
    <header className="flex-shrink-0 border-b border-[var(--border-default)] bg-[var(--bg-surface)] flex items-center gap-2 px-3" style={{ height: '40px', zIndex: 'var(--z-sticky)' }}>
      <div ref={containerRef} className="relative w-full max-w-[420px]">
        <input
          ref={inputRef}
          type="search"
          placeholder={searchPlaceholder}
          value={query}
          onChange={e => setQuery(e.target.value)}
          onKeyDown={handleKeyDown}
          onFocus={openDropdown}
          onClick={openDropdown}
          className="h-[34px] w-full rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] px-3 text-body text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none focus:ring-2 focus:ring-[var(--accent-blue)]/20 focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg-primary)]"
          aria-label={t('common.search')}
        />
        {indexing && (
          <div
            className="pointer-events-none absolute bottom-0 left-0 h-0.5 rounded-b-md bg-[var(--accent-blue)] transition-[width] duration-300"
            style={{ width: `${Math.min(100, Math.max(2, indexStatus?.percent ?? 0))}%` }}
            aria-hidden="true"
          />
        )}
        {/* Always mounted so screen readers hear index progress without opening the dropdown. */}
        <div className="sr-only" role="status" aria-live="polite" aria-atomic="true">
          {indexAriaLive}
        </div>
        {open && (
          <div className="absolute top-full left-0 right-0 mt-1 rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] shadow-lg z-30 max-h-[400px] overflow-y-auto">
            {indexLabel && (
              <div className="border-b border-[var(--border-muted)] px-3 py-1.5 text-meta text-[var(--text-muted)]">
                {indexLabel}
                {indexing && <span className="ml-1 text-[var(--text-muted)]">· {t('search.partialAvailable')}</span>}
              </div>
            )}
            {isHistoryMode ? (
              <>
                <div className="sticky top-0 bg-[var(--bg-surface)] px-3 pt-2 pb-1 text-meta font-medium uppercase tracking-wide text-[var(--text-muted)]">
                  {t('search.recent')}
                </div>
                {visibleHistory.map((entry, i) => (
                  <div
                    key={entry.query}
                    onMouseEnter={() => setActiveIndex(i)}
                    className={`group flex w-full items-center gap-2 px-3 py-1.5 transition-colors duration-fast ${
                      i === activeIndex ? 'bg-[var(--accent-blue)]/10' : ''
                    }`}
                  >
                    <span className={`flex-shrink-0 ${entry.pinned ? 'text-[var(--accent-blue)]' : 'text-[var(--text-muted)]'}`}>
                      {entry.pinned ? <PinIcon filled /> : <ClockIcon />}
                    </span>
                    <button
                      onClick={() => selectHistory(entry.query)}
                      className="min-w-0 flex-1 truncate text-left text-helper text-[var(--text-primary)]"
                    >
                      {entry.query}
                    </button>
                    <span className="flex flex-shrink-0 items-center gap-0.5 opacity-0 transition-opacity duration-fast group-hover:opacity-100">
                      <button
                        onClick={e => { e.stopPropagation(); togglePin(entry.query) }}
                        title={entry.pinned ? t('search.unpin') : t('search.pin')}
                        aria-label={entry.pinned ? t('search.unpin') : t('search.pin')}
                        className={`flex h-5 w-5 items-center justify-center rounded hover:bg-[var(--bg-surface-hover)] ${
                          entry.pinned ? 'text-[var(--accent-blue)]' : 'text-[var(--text-muted)] hover:text-[var(--text-primary)]'
                        }`}
                      >
                        <PinIcon filled={entry.pinned} />
                      </button>
                      <button
                        onClick={e => { e.stopPropagation(); removeEntry(entry.query) }}
                        title={t('search.delete')}
                        aria-label={t('search.delete')}
                        className="flex h-5 w-5 items-center justify-center rounded text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]"
                      >
                        ✕
                      </button>
                    </span>
                  </div>
                ))}
              </>
            ) : (
              <>
                {loading && results.length === 0 && <div className="px-3 py-2 text-helper text-[var(--text-muted)]">{t('search.searching')}</div>}
                {!loading && results.length === 0 && <div className="px-3 py-2 text-helper text-[var(--text-muted)]">{t('search.noMatches')}</div>}
                {results.length > 0 && (
                  <div className="sticky top-0 bg-[var(--bg-surface)] px-3 pt-2 pb-1 text-meta font-medium uppercase tracking-wide text-[var(--text-muted)]">
                    {t('search.results')}
                  </div>
                )}
                {results.map((r, i) => (
                  <button
                    key={`${r.agent_type}-${r.session_id}`}
                    onClick={() => selectResult(r)}
                    onMouseEnter={() => setActiveIndex(i)}
                    className={`w-full border-b border-[var(--border-muted)] px-3 py-2 text-left transition-colors duration-fast last:border-b-0 ${
                      i === activeIndex ? 'bg-[var(--accent-blue)]/10' : 'hover:bg-[var(--bg-surface-hover)]'
                    }`}
                  >
                    <div className="flex items-center gap-1.5">
                      <AgentIcon agentType={r.agent_type} size={14} />
                      <span className="min-w-0 flex-1 truncate text-helper text-[var(--text-primary)]">
                        {r.name || r.session_id}
                      </span>
                      {r.project && (
                        <span className="max-w-[120px] flex-shrink-0 truncate rounded border border-[var(--border-muted)] bg-[var(--bg-inset)] px-1.5 text-meta text-[var(--text-secondary)]">
                          {r.project}
                        </span>
                      )}
                      {r.updated_at && (
                        <span className="flex-shrink-0 text-meta text-[var(--text-muted)]">{relTime(r.updated_at, locale)}</span>
                      )}
                    </div>
                    <div className="mt-0.5 truncate pl-[20px] text-helper text-[var(--text-secondary)]">
                      <HighlightedSnippet text={r.match} query={query} />
                    </div>
                  </button>
                ))}
              </>
            )}
          </div>
        )}
      </div>
      <div className="ml-auto flex items-center gap-1.5">
        <ThemeSwitch />
        <div className="relative">
          <button
            onClick={() => setShowSettings(true)}
            className={`inline-flex h-7 w-auto items-center gap-1 rounded-md px-1.5 text-nav focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
              showSettings
                ? 'bg-[var(--bg-surface-hover)] text-[var(--text-primary)]'
                : 'text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
            }`}
            title={t('settings.title')}
            aria-label={t('settings.title')}
          >
            <span aria-hidden="true">⚙</span>
            <span>{t('settings.title')}</span>
          </button>
        </div>
      </div>
    </header>
    {showSettings && (
      <SettingsDialog
        open={showSettings}
        onClose={() => {
          setShowSettings(false)
          setSettingsInitialTab(undefined)
        }}
        historyLimit={historyLimit}
        onHistoryLimitChange={updateLimit}
        onClearHistory={clearHistory}
        onOpenAISettings={() => setShowAISettings(true)}
        initialTab={settingsInitialTab}
      />
    )}
    {showAISettings && <AISettingsModal onClose={() => { setShowAISettings(false); setShowSettings(true) }} />}
    </>
  )
}
