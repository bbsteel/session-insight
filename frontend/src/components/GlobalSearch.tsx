import { useEffect, useRef, useState } from 'react'
import { fetchSearch, fetchSettings, saveSettings } from '../api'
import type { SearchResult } from '../types'
import AgentIcon from './AgentIcon'
import AISettingsModal from './AISettingsModal'
import ThemeToggle from './ThemeToggle'
import { getNavOpenPref, setNavOpenPref, type NavOpenPref } from '../navPrefs'
import {
  defaultBannerColor,
  getBannerColorOverride,
  setBannerColorOverride,
  useIsDark,
} from '../terminalTheme'

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

function relTime(iso: string): string {
  if (!iso) return ''
  const diff = Date.now() - new Date(iso).getTime()
  if (!Number.isFinite(diff) || diff < 0) return ''
  const minutes = Math.floor(diff / 60000)
  if (minutes < 1) return '刚刚'
  if (minutes < 60) return `${minutes}分钟前`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}小时前`
  const days = Math.floor(hours / 24)
  if (days < 30) return `${days}天前`
  return new Date(iso).toLocaleDateString()
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
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<SearchResult[]>([])
  const [loading, setLoading] = useState(false)
  const [open, setOpen] = useState(false)
  const [activeIndex, setActiveIndex] = useState(-1)
  const [history, setHistory] = useState<HistoryEntry[]>([])
  const [historyLimit, setHistoryLimit] = useState(readHistoryLimit)
  const [showSettings, setShowSettings] = useState(false)
  const [showAISettings, setShowAISettings] = useState(false)
  const isDark = useIsDark()
  const [bannerColor, setBannerColor] = useState<string | null>(getBannerColorOverride)
  const [editorCommand, setEditorCommand] = useState('')
  const [editorCommandDefault, setEditorCommandDefault] = useState('')
  const savedEditorCommandRef = useRef('')
  const [fileExts, setFileExts] = useState('')
  const savedFileExtsRef = useRef('')
  // 终端消息时间戳前缀:哪些消息类型显示 HH:MM:SS(服务端渲染选项)。
  const [tsKinds, setTsKinds] = useState<string[]>([])
  const [navOpenPref, setNavOpenPrefState] = useState<NavOpenPref>(getNavOpenPref)
  const inputRef = useRef<HTMLInputElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const settingsRef = useRef<HTMLDivElement>(null)
  const debounceRef = useRef<ReturnType<typeof setTimeout>>()

  // Other views (e.g. the AI panel's "去配置模型源" guidance) open the AI
  // provider modal through this window event — GlobalSearch is the one
  // component mounted in every layout state.
  useEffect(() => {
    const open = () => setShowAISettings(true)
    window.addEventListener('si-open-ai-settings', open)
    return () => window.removeEventListener('si-open-ai-settings', open)
  }, [])

  const isHistoryMode = !query.trim()
  const visibleHistory = sortAndTrim(history, historyLimit)
  const listLength = isHistoryMode ? visibleHistory.length : results.length

  // Editor command template lives server-side (the server executes it);
  // loaded when the settings panel opens, saved on blur if changed.
  useEffect(() => {
    if (!showSettings) return
    fetchSettings()
      .then(s => {
        setEditorCommand(s.editor_command)
        setEditorCommandDefault(s.editor_command_default)
        savedEditorCommandRef.current = s.editor_command
        setFileExts(s.file_open_extensions ?? '')
        savedFileExtsRef.current = s.file_open_extensions ?? ''
        setTsKinds((s.timestamp_kinds ?? '').split(',').filter(Boolean))
      })
      .catch(() => {})
  }, [showSettings])

  const toggleTsKind = (kind: string) => {
    const next = tsKinds.includes(kind) ? tsKinds.filter(k => k !== kind) : [...tsKinds, kind]
    setTsKinds(next)
    void saveSettings({ timestamp_kinds: next.join(',') })
      .then(() => window.dispatchEvent(new Event('si-settings-changed')))
      .catch(() => setTsKinds(tsKinds))
  }

  const persistEditorCommand = async () => {
    const value = editorCommand.trim()
    if (value === savedEditorCommandRef.current) return
    try {
      await saveSettings({ editor_command: value })
      savedEditorCommandRef.current = value
    } catch {
      setEditorCommand(savedEditorCommandRef.current)
    }
  }

  const persistFileExts = async () => {
    const value = fileExts.trim()
    if (value === savedFileExtsRef.current) return
    try {
      await saveSettings({ file_open_extensions: value })
      savedFileExtsRef.current = value
    } catch {
      setFileExts(savedFileExtsRef.current)
    }
  }

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

  useEffect(() => {
    const handleClick = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) setOpen(false)
      if (settingsRef.current && !settingsRef.current.contains(e.target as Node)) setShowSettings(false)
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

  const isMac = typeof navigator !== 'undefined' && /Mac|iPod|iPhone|iPad/.test(navigator.platform)
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
          placeholder={`全文搜索... (${isMac ? '⌘K' : 'Ctrl+K'})`}
          value={query}
          onChange={e => setQuery(e.target.value)}
          onKeyDown={handleKeyDown}
          onFocus={openDropdown}
          onClick={openDropdown}
          className="h-[34px] w-full rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] px-3 text-body text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none focus:ring-2 focus:ring-[var(--accent-blue)]/20 focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg-primary)]"
          aria-label="全文搜索"
        />
        {open && (
          <div className="absolute top-full left-0 right-0 mt-1 rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] shadow-lg z-30 max-h-[400px] overflow-y-auto">
            {isHistoryMode ? (
              <>
                <div className="sticky top-0 bg-[var(--bg-surface)] px-3 pt-2 pb-1 text-meta font-medium uppercase tracking-wide text-[var(--text-muted)]">
                  最近搜索
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
                        title={entry.pinned ? '取消钉住' : '钉住'}
                        aria-label={entry.pinned ? '取消钉住' : '钉住'}
                        className={`flex h-5 w-5 items-center justify-center rounded hover:bg-[var(--bg-surface-hover)] ${
                          entry.pinned ? 'text-[var(--accent-blue)]' : 'text-[var(--text-muted)] hover:text-[var(--text-primary)]'
                        }`}
                      >
                        <PinIcon filled={entry.pinned} />
                      </button>
                      <button
                        onClick={e => { e.stopPropagation(); removeEntry(entry.query) }}
                        title="删除"
                        aria-label="删除"
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
                {loading && results.length === 0 && <div className="px-3 py-2 text-helper text-[var(--text-muted)]">搜索中...</div>}
                {!loading && results.length === 0 && <div className="px-3 py-2 text-helper text-[var(--text-muted)]">无匹配结果</div>}
                {results.length > 0 && (
                  <div className="sticky top-0 bg-[var(--bg-surface)] px-3 pt-2 pb-1 text-meta font-medium uppercase tracking-wide text-[var(--text-muted)]">
                    结果
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
                        <span className="flex-shrink-0 text-meta text-[var(--text-muted)]">{relTime(r.updated_at)}</span>
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
      <div className="ml-auto flex items-center gap-1">
        <div ref={settingsRef} className="relative">
          <button
            onClick={() => setShowSettings(v => !v)}
            className={`h-7 w-7 rounded-md text-nav focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
              showSettings
                ? 'bg-[var(--bg-surface-hover)] text-[var(--text-primary)]'
                : 'text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
            }`}
            title="设置"
            aria-label="设置"
          >
            ⚙
          </button>
          {showSettings && (
            <div className="absolute right-0 top-full z-30 mt-1 max-h-[75vh] w-[260px] overflow-y-auto rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] p-3 shadow-lg">
              <div className="mb-2 text-meta font-medium uppercase tracking-wide text-[var(--text-muted)]">外观</div>
              <ThemeToggle />
              <div className="mt-1 text-meta text-[var(--text-muted)]">默认浅色；跟随系统时随 OS 深/浅切换</div>
              <div className="mb-2 mt-4 border-t border-[var(--border-muted)] pt-3 text-meta font-medium uppercase tracking-wide text-[var(--text-muted)]">
                会话导航
              </div>
              <label className="flex items-center justify-between gap-2 text-helper text-[var(--text-primary)]">
                打开时展开
                <select
                  value={navOpenPref}
                  onChange={e => {
                    const next = e.target.value as NavOpenPref
                    setNavOpenPref(next)
                    setNavOpenPrefState(next)
                  }}
                  className="h-7 max-w-[9rem] rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] px-1.5 text-helper text-[var(--text-primary)] focus:border-[var(--accent-blue)] focus:outline-none"
                  aria-label="打开会话时展开导航"
                >
                  <option value="user">用户消息</option>
                  <option value="tool">工具调用</option>
                  <option value="off">不展开</option>
                </select>
              </label>
              <div className="mt-1 text-meta text-[var(--text-muted)]">打开会话时展开导航面板并启用钉住（点击外部不关闭）</div>
              <div className="mb-2 mt-4 border-t border-[var(--border-muted)] pt-3 text-meta font-medium uppercase tracking-wide text-[var(--text-muted)]">搜索设置</div>
              <label className="flex items-center justify-between gap-2 text-helper text-[var(--text-primary)]">
                历史记录条数
                <input
                  type="number"
                  min={1}
                  max={50}
                  value={historyLimit}
                  onChange={e => updateLimit(Number(e.target.value))}
                  className="h-7 w-16 rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] px-2 text-helper text-[var(--text-primary)] focus:border-[var(--accent-blue)] focus:outline-none"
                />
              </label>
              <div className="mt-1 text-meta text-[var(--text-muted)]">钉住的记录不占用条数限制</div>
              <button
                onClick={clearHistory}
                className="mt-3 h-7 w-full rounded-md border border-[var(--border-default)] text-helper text-[var(--accent-red,#e5534b)] transition-colors duration-fast hover:bg-[var(--bg-surface-hover)]"
              >
                清空搜索历史
              </button>
              <div className="mb-2 mt-4 border-t border-[var(--border-muted)] pt-3 text-meta font-medium uppercase tracking-wide text-[var(--text-muted)]">
                终端外观
              </div>
              <label className="flex items-center justify-between gap-2 text-helper text-[var(--text-primary)]">
                Turn 横幅颜色
                <span className="flex items-center gap-1.5">
                  <input
                    type="color"
                    value={bannerColor ?? defaultBannerColor(isDark)}
                    onChange={e => {
                      setBannerColor(e.target.value)
                      setBannerColorOverride(e.target.value)
                    }}
                    className="h-7 w-9 cursor-pointer rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] p-0.5"
                  />
                  {bannerColor && (
                    <button
                      onClick={() => {
                        setBannerColor(null)
                        setBannerColorOverride(null)
                      }}
                      className="h-7 rounded-md border border-[var(--border-default)] px-2 text-helper text-[var(--text-secondary)] transition-colors duration-fast hover:bg-[var(--bg-surface-hover)]"
                    >
                      恢复默认
                    </button>
                  )}
                </span>
              </label>
              <div className="mt-1 text-meta text-[var(--text-muted)]">默认随深/浅主题，改动即时生效</div>
              <div className="mt-3 text-helper text-[var(--text-primary)]">消息时间戳前缀</div>
              <div className="mt-1 flex items-center gap-3">
                {([['user', '用户输入'], ['assistant', '助手回复'], ['tool', '工具调用']] as const).map(([kind, label]) => (
                  <label key={kind} className="flex cursor-pointer items-center gap-1.5 text-helper text-[var(--text-primary)]">
                    <input
                      type="checkbox"
                      checked={tsKinds.includes(kind)}
                      onChange={() => toggleTsKind(kind)}
                      className="h-3.5 w-3.5 accent-[var(--accent-blue)]"
                    />
                    {label}
                  </label>
                ))}
              </div>
              <div className="mt-1 text-meta text-[var(--text-muted)]">勾选的消息类型在终端里显示 HH:MM:SS，保存后立即重渲染</div>
              <div className="mb-2 mt-4 border-t border-[var(--border-muted)] pt-3 text-meta font-medium uppercase tracking-wide text-[var(--text-muted)]">
                编辑器
              </div>
              <label className="block text-helper text-[var(--text-primary)]">
                打开文件命令
                <input
                  type="text"
                  value={editorCommand}
                  placeholder={editorCommandDefault || 'code --goto {path}:{line}'}
                  onChange={e => setEditorCommand(e.target.value)}
                  onBlur={() => void persistEditorCommand()}
                  className="mt-1 h-7 w-full rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] px-2 font-mono text-meta text-[var(--text-primary)] focus:border-[var(--accent-blue)] focus:outline-none"
                />
              </label>
              <div className="mt-1 text-meta text-[var(--text-muted)]">
                {'占位符 {path}、{line}；留空自动探测（code → xdg-open）'}
              </div>
              <label className="mt-3 block text-helper text-[var(--text-primary)]">
                可打开文件类型
                <input
                  type="text"
                  value={fileExts}
                  placeholder="留空 = 默认代码/文本扩展名"
                  onChange={e => setFileExts(e.target.value)}
                  onBlur={() => void persistFileExts()}
                  className="mt-1 h-7 w-full rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] px-2 font-mono text-meta text-[var(--text-primary)] focus:border-[var(--accent-blue)] focus:outline-none"
                />
              </label>
              <div className="mt-1 text-meta text-[var(--text-muted)]">
                逗号分隔（如 go,ts,vue）；* 表示不限制。终端里只有这些类型的文件才会出现打开入口，改动刷新后生效
              </div>
              <div className="mb-2 mt-4 border-t border-[var(--border-muted)] pt-3 text-meta font-medium uppercase tracking-wide text-[var(--text-muted)]">
                AI
              </div>
              <button
                onClick={() => { setShowSettings(false); setShowAISettings(true) }}
                className="h-7 w-full rounded-md border border-[var(--border-default)] text-helper text-[var(--text-secondary)] transition-colors duration-fast hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]"
              >
                管理 AI 模型源…
              </button>
              <div className="mt-1 text-meta text-[var(--text-muted)]">用于会话总结、会话标题与会话交接</div>
            </div>
          )}
        </div>
      </div>
    </header>
    {showAISettings && <AISettingsModal onClose={() => setShowAISettings(false)} />}
    </>
  )
}
