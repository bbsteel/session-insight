import { useEffect, useMemo, useState, useRef } from 'react'
import { fetchSessions } from '../api'
import type { SessionSummary } from '../types'

function timeAgo(dateStr: string): string {
  const date = new Date(dateStr)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffMin = Math.floor(diffMs / 60000)
  const diffHr = Math.floor(diffMs / 3600000)
  const diffDay = Math.floor(diffMs / 86400000)

  if (diffMin < 1) return '刚刚'
  if (diffMin < 60) return `${diffMin} 分钟前`
  if (diffHr < 24) return `${diffHr} 小时前`
  if (diffDay === 1) return '昨天'
  if (diffDay < 7) return `${diffDay} 天前`
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${month}-${day}`
}

const SIDEBAR_WIDTH_KEY = 'sidebar-width'
const SIDEBAR_VIEW_MODE_KEY = 'sidebar-view-mode'

function getSessionName(s: SessionSummary): string {
  if (s.name) return s.name
  if (s.repository) return s.repository
  return s.id.slice(0, 8)
}

function getAgentLabel(agent: string): string {
  if (!agent) return 'Unknown'
  if (agent.toLowerCase().includes('copilot')) return 'Copilot'
  if (agent.toLowerCase().includes('claude')) return 'Claude Code'
  if (agent.toLowerCase().includes('codex')) return 'Codex'
  return agent
}

function getAgentIcon(agent: string): string {
  const normalized = agent.toLowerCase()
  if (normalized.includes('copilot')) return 'CP'
  if (normalized.includes('claude')) return 'CC'
  if (normalized.includes('codex')) return 'CX'
  return 'AI'
}

interface SidebarProps {
  selectedId: string | null
  onSelect: (id: string) => void
}

export default function Sidebar({ selectedId, onSelect }: SidebarProps) {
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  const [viewMode, setViewMode] = useState<'grouped' | 'flat'>(() => {
    const stored = localStorage.getItem(SIDEBAR_VIEW_MODE_KEY)
    return stored === 'flat' ? 'flat' : 'grouped'
  })
  const [width, setWidth] = useState(() => {
    const stored = Number(localStorage.getItem(SIDEBAR_WIDTH_KEY))
    return Number.isFinite(stored) && stored >= 160 && stored <= 400 ? stored : 260
  })
  const searchRef = useRef<HTMLInputElement>(null)
  const asideRef = useRef<HTMLElement>(null)

  // Global Cmd+K / Ctrl+K to focus search
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault()
        searchRef.current?.focus()
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [])

  useEffect(() => {
    fetchSessions()
      .then(data => { setSessions(data); setError(null) })
      .catch(err => setError(err instanceof Error ? err.message : '会话列表加载失败'))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    localStorage.setItem(SIDEBAR_VIEW_MODE_KEY, viewMode)
  }, [viewMode])

  const beginResize = (e: React.PointerEvent) => {
    const startX = e.clientX
    const startWidth = width
    ;(e.target as HTMLElement).setPointerCapture(e.pointerId)

    const move = (event: PointerEvent) => {
      const next = Math.min(400, Math.max(160, startWidth + event.clientX - startX))
      setWidth(next)
      localStorage.setItem(SIDEBAR_WIDTH_KEY, String(Math.round(next)))
    }
    const up = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
  }

  const filtered = useMemo(() => sessions.filter(s => {
    if (query.trim()) {
      const q = query.toLowerCase()
      const name = getSessionName(s).toLowerCase()
      const repo = (s.repository || '').toLowerCase()
      const agent = (s.agent_type || '').toLowerCase()
      if (!name.includes(q) && !repo.includes(q) && !agent.includes(q)) return false
    }
    return true
  }).sort((a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()), [sessions, query])

  const agentGroups = useMemo(() => {
    const grouped = new Map<string, SessionSummary[]>()
    for (const s of filtered) {
      const key = getAgentLabel(s.agent_type)
      const list = grouped.get(key) || []
      list.push(s)
      grouped.set(key, list)
    }
    return [...grouped.entries()].sort(([a], [b]) => a.localeCompare(b))
  }, [filtered])

  const SessionRow = ({ session, showAgent }: { session: SessionSummary; showAgent: boolean }) => {
    const selected = session.id === selectedId
    return (
      <button
        key={session.id}
        onClick={() => onSelect(session.id)}
        onContextMenu={(e) => { e.preventDefault(); navigator.clipboard.writeText(session.id).catch(() => {}) }}
        title="右键复制会话 ID"
        className={`relative w-full text-left pl-3 pr-2 py-1.5 rounded-md cursor-pointer transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg-surface)] ${
          selected ? 'bg-[var(--bg-surface-hover)]' : 'hover:bg-[var(--bg-surface-hover)]'
        }`}
      >
        {selected && <span className="absolute left-0 top-1.5 bottom-1.5 w-0.5 rounded-full bg-[var(--accent-blue)]" />}
        <div className="text-body text-[var(--text-primary)] truncate flex items-center gap-1.5">
          {session.is_live && <span className="w-1.5 h-1.5 rounded-full bg-[var(--success)] flex-shrink-0 animate-pulse" title="进行中" aria-label="进行中" />}
          {showAgent && <span className="text-agent text-[var(--text-muted)]">{getAgentIcon(session.agent_type)}</span>}
          <span className="truncate">{getSessionName(session)}</span>
        </div>
        <div className="text-helper text-[var(--text-secondary)] mt-0.5 truncate">
          {getAgentIcon(session.agent_type)} · {session.is_live ? '进行中' : timeAgo(session.updated_at)} · {session.message_count || session.turn_count} 条消息
        </div>
      </button>
    )
  }

  if (loading) {
    return (
      <aside
        className="h-full flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-surface)]"
        style={{ width }}
      >
        <div className="p-4">
          <h2 className="text-nav font-semibold text-[var(--text-primary)]">Sessions</h2>
          <div className="mt-3 space-y-2">
            {Array.from({ length: 6 }).map((_, i) => (
              <div key={i} className="h-7 bg-[var(--bg-surface-hover)] rounded-sm animate-pulse" />
            ))}
          </div>
        </div>
      </aside>
    )
  }

  return (
    <aside
      ref={asideRef}
      className="h-full flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-surface)] overflow-y-auto relative"
      style={{ width }}
    >
      <div className="p-4 flex items-center justify-between">
        <h2 className="text-nav font-semibold text-[var(--text-primary)]">
          {query ? `Results (${filtered.length}/${sessions.length})` : `Sessions (${sessions.length})`}
        </h2>
        <div className="flex rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] p-0.5">
          {(['grouped', 'flat'] as const).map(mode => (
            <button
              key={mode}
              onClick={() => setViewMode(mode)}
              className={`h-6 px-2 rounded-sm text-meta transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
                viewMode === mode ? 'bg-[var(--bg-surface)] text-[var(--text-primary)] shadow-sm' : 'text-[var(--text-muted)] hover:text-[var(--text-primary)]'
              }`}
            >
              {mode === 'grouped' ? '分组' : '平铺'}
            </button>
          ))}
        </div>
      </div>

      <div className="px-4 pb-2">
        <div className="relative">
          <input
            ref={searchRef}
            type="text"
            placeholder="搜索会话... (⌘K)"
            value={query}
            onChange={e => setQuery(e.target.value)}
            className="w-full h-[34px] rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] px-3 pr-7 text-body text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none focus:ring-2 focus:ring-[var(--accent-blue)]/20 focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg-surface)]"
          />
          {query && (
            <button
              onClick={() => setQuery('')}
              className="absolute right-1 top-1/2 -translate-y-1/2 w-5 h-5 flex items-center justify-center rounded-sm text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast"
              title="Clear search"
            >
              &times;
            </button>
          )}
        </div>
      </div>

      <div className="px-2 pb-4">
        {error ? (
          <div className="px-3 py-8 text-center">
            <div className="mx-auto mb-2 flex h-7 w-7 items-center justify-center rounded-md bg-[var(--bg-inset)] text-helper text-[var(--warning)]">!</div>
            <div className="text-nav font-medium text-[var(--text-primary)]">后端未连接</div>
            <div className="mt-1 text-helper text-[var(--text-muted)]">启动 Go 服务后会显示本地会话。</div>
          </div>
        ) : filtered.length === 0 ? (
          <div className="px-3 py-8 text-center">
            <div className="mx-auto mb-2 flex h-7 w-7 items-center justify-center rounded-md bg-[var(--bg-inset)] text-helper text-[var(--text-muted)]">⌕</div>
            <div className="text-nav font-medium text-[var(--text-primary)]">未找到匹配的会话</div>
            <div className="mt-1 text-helper text-[var(--text-muted)]">尝试其他关键词或清除筛选条件</div>
          </div>
        ) : viewMode === 'flat' ? (
          <div className="space-y-1">
            {filtered.map(s => <SessionRow key={s.id} session={s} showAgent />)}
          </div>
        ) : (
          agentGroups.map(([agent, list]) => (
            <div key={agent} className="mb-3">
              <div className="px-2 py-1 text-section uppercase tracking-[0.04em] text-[var(--text-muted)] flex items-center justify-between">
                <span>{getAgentIcon(list[0]?.agent_type || agent)} · {agent}</span>
                <span>{list.length}</span>
              </div>
              <div className="space-y-1">
                {list.map(s => <SessionRow key={s.id} session={s} showAgent={false} />)}
              </div>
            </div>
          ))
        )}
      </div>
      <div
        onPointerDown={beginResize}
        className="absolute right-0 top-0 h-full w-1 cursor-grab hover:bg-[var(--accent-blue)]/30 active:cursor-grabbing"
        title="拖拽调整侧栏宽度"
      />
    </aside>
  )
}
