import { useEffect, useMemo, useState, useRef, useCallback } from 'react'
import { fetchAgents, fetchSessions } from '../api'
import type { AgentInfo, SessionSummary } from '../types'
import AgentFilter from './AgentFilter'
import AgentIcon from './AgentIcon'

function formatDateTime(dateStr: string): string {
  const date = new Date(dateStr)
  if (Number.isNaN(date.getTime())) return dateStr
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  const hour = String(date.getHours()).padStart(2, '0')
  const minute = String(date.getMinutes()).padStart(2, '0')
  return `${year}-${month}-${day} ${hour}:${minute}`
}

const SIDEBAR_WIDTH_KEY = 'sidebar-width'
const SIDEBAR_VIEW_MODE_KEY = 'sidebar-view-mode'
const SIDEBAR_COLLAPSED_GROUPS_KEY = 'sidebar-collapsed-groups'

function readStorage(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

function writeStorage(key: string, value: string): void {
  try {
    localStorage.setItem(key, value)
  } catch {
    // Storage is optional; keep the in-memory UI state working.
  }
}

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


interface SidebarProps {
  selectedId: string | null
  onSelect: (id: string) => void
  drawer?: boolean
  onClose?: () => void
}

export default function Sidebar({ selectedId, onSelect, drawer, onClose }: SidebarProps) {
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  const [viewMode, setViewMode] = useState<'grouped' | 'flat'>(() => {
    const stored = readStorage(SIDEBAR_VIEW_MODE_KEY)
    return stored === 'flat' ? 'flat' : 'grouped'
  })
  const [width, setWidth] = useState(() => {
    const stored = Number(readStorage(SIDEBAR_WIDTH_KEY))
    return Number.isFinite(stored) && stored >= 160 && stored <= 400 ? stored : 260
  })
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(() => {
    try {
      const stored = readStorage(SIDEBAR_COLLAPSED_GROUPS_KEY)
      return stored ? new Set(JSON.parse(stored)) : new Set()
    } catch { return new Set() }
  })
  const [toast, setToast] = useState<string | null>(null)
  const toastTimer = useRef<ReturnType<typeof setTimeout>>()
  const [isMobile, setIsMobile] = useState(() =>
    typeof window !== 'undefined' && window.matchMedia('(max-width: 767px)').matches
  )
  const [agents, setAgents] = useState<AgentInfo[] | null>(null)
  const [agentsReady, setAgentsReady] = useState(false)
  const [agentFilter, setAgentFilter] = useState<string>('')

  const searchRef = useRef<HTMLInputElement>(null)
  const asideRef = useRef<HTMLElement>(null)

  useEffect(() => {
    const mq = window.matchMedia('(max-width: 767px)')
    const handler = (e: MediaQueryListEvent) => setIsMobile(e.matches)
    mq.addEventListener('change', handler)
    return () => mq.removeEventListener('change', handler)
  }, [])

  // Auto-focus search when drawer opens on mobile
  useEffect(() => {
    if (isMobile && drawer) {
      const timer = setTimeout(() => searchRef.current?.focus(), 100)
      return () => clearTimeout(timer)
    }
  }, [isMobile, drawer])

  // Esc to close drawer on mobile
  useEffect(() => {
    if (!isMobile || !onClose) return
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [isMobile, onClose])

  // Load all sessions once on mount — agent/search filters are client-side
  useEffect(() => {
    fetchSessions()
      .then(data => { setSessions(data); setError(null) })
      .catch(err => setError(err instanceof Error ? err.message : '会话列表加载失败'))
      .finally(() => setLoading(false))
  }, [])

  // Fetch agents on mount
  useEffect(() => {
    fetchAgents()
      .then(data => {
        setAgents(data)
        setAgentsReady(true)
      })
      .catch(() => setAgents([]))
  }, [])

  useEffect(() => {
    writeStorage(SIDEBAR_VIEW_MODE_KEY, viewMode)
  }, [viewMode])

  useEffect(() => {
    writeStorage(SIDEBAR_COLLAPSED_GROUPS_KEY, JSON.stringify([...collapsedGroups]))
  }, [collapsedGroups])

  const showToast = useCallback((msg: string) => {
    setToast(msg)
    if (toastTimer.current) clearTimeout(toastTimer.current)
    toastTimer.current = setTimeout(() => setToast(null), 2000)
  }, [])

  const beginResize = (e: React.PointerEvent) => {
    if (isMobile) return
    const startX = e.clientX
    const startWidth = width
    ;(e.target as HTMLElement).setPointerCapture(e.pointerId)

    const move = (event: PointerEvent) => {
      const next = Math.min(400, Math.max(160, startWidth + event.clientX - startX))
      setWidth(next)
      writeStorage(SIDEBAR_WIDTH_KEY, String(Math.round(next)))
    }
    const up = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
  }

  const copyId = useCallback(async (id: string) => {
    try {
      await navigator.clipboard.writeText(id)
      showToast('已复制会话 ID')
    } catch {
      showToast('复制失败')
    }
  }, [showToast])

  const filtered = useMemo(() => sessions.filter(s => {
    if (agentFilter && s.agent_type !== agentFilter) return false
    if (query.trim()) {
      const q = query.toLowerCase()
      const name = getSessionName(s).toLowerCase()
      const repo = (s.repository || '').toLowerCase()
      const branch = (s.branch || '').toLowerCase()
      const agent = (s.agent_type || '').toLowerCase()
      const preview = (s.preview_text || '').toLowerCase()
      if (!name.includes(q) && !repo.includes(q) && !branch.includes(q) && !agent.includes(q) && !preview.includes(q)) return false
    }
    return true
  }).sort((a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()), [sessions, query, agentFilter])

  const liveCount = useMemo(() => sessions.filter(s => s.is_live).length, [sessions])

  const effectiveAgents = useMemo<AgentInfo[]>(() => {
    if (agents && agents.length > 0) return agents

    const counts = new Map<string, number>()
    for (const session of sessions) {
      if (!session.agent_type) continue
      counts.set(session.agent_type, (counts.get(session.agent_type) ?? 0) + 1)
    }
    return [...counts.entries()].map(([type, session_count]) => ({
      type,
      display_name: getAgentLabel(type),
      session_count,
    }))
  }, [agents, sessions])

  useEffect(() => {
    if (!agentsReady || !agentFilter) return
    if (!effectiveAgents.some(agent => agent.type === agentFilter)) {
      setAgentFilter('')
    }
  }, [agentFilter, agentsReady, effectiveAgents])

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

  const toggleGroup = (agent: string) => {
    setCollapsedGroups(prev => {
      const next = new Set(prev)
      if (next.has(agent)) next.delete(agent)
      else next.add(agent)
      return next
    })
  }

  const SessionRow = ({ session }: { session: SessionSummary }) => {
    const selected = session.id === selectedId
    const repo = session.repository || ''
    const branch = session.branch || ''

    const parts: string[] = []
    if (repo) {
      const shortRepo = repo.split('/').slice(-2).join('/')
      parts.push(shortRepo)
    }
    if (branch) parts.push(branch)
    parts.push(`${session.message_count || session.turn_count} msgs`)
    if (session.is_live) parts.push('进行中')
    const metadata = parts.join(' · ')
    const dateTime = formatDateTime(session.updated_at)

    return (
      <div className="group relative">
        <button
          onClick={() => onSelect(session.id)}
          onContextMenu={(e) => { e.preventDefault(); copyId(session.id) }}
          title="右键复制会话 ID"
          className={`relative w-full text-left pl-2.5 pr-8 rounded-md cursor-pointer transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg-surface)] ${
            selected ? 'bg-[var(--bg-surface-hover)]' : 'hover:bg-[var(--bg-surface-hover)]'
          }`}
          style={{ paddingTop: '0.375rem', paddingBottom: '0.375rem' }}
        >
          {selected && <span className="absolute left-0 top-1.5 bottom-1.5 w-0.5 rounded-full bg-[var(--accent-blue)]" />}
          <div className="flex items-start gap-1.5">
            <AgentIcon agentType={session.agent_type} size={20} className="mt-0.5" />
            <div className="min-w-0 flex-1">
              <div className="text-body text-[var(--text-primary)] truncate flex items-center gap-1.5">
                {session.is_live && <span className="w-1.5 h-1.5 rounded-full bg-[var(--success)] flex-shrink-0 animate-pulse" title="进行中" aria-label="进行中" />}
                <span className="truncate">{getSessionName(session)}</span>
              </div>
              <div className="text-helper text-[var(--text-secondary)] mt-0.5 flex items-center gap-2">
                <span className="truncate min-w-0">{metadata}</span>
                <time className="ml-auto flex-shrink-0 tabular-nums" dateTime={session.updated_at}>
                  {dateTime}
                </time>
              </div>
            </div>
          </div>
        </button>
        <button
          onClick={(e) => { e.stopPropagation(); copyId(session.id) }}
          tabIndex={-1}
          className="absolute right-1 top-1.5 w-5 h-5 flex items-center justify-center rounded-sm text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-inset)] opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 max-md:opacity-100 transition-opacity duration-fast"
          aria-hidden="true"
          title="复制会话 ID"
        >
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
            <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
          </svg>
        </button>
      </div>
    )
  }

  const effectiveWidth = isMobile ? 'min(86vw, 340px)' : `${width}px`

  if (loading) {
    return (
      <aside
        className="h-full flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-surface)]"
        style={{ width: effectiveWidth }}
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
      className="h-full flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-surface)] relative flex flex-col"
      style={{ width: effectiveWidth }}
      role={isMobile ? 'dialog' : undefined}
      aria-label="会话列表"
    >
      {/* Header */}
      <div className="p-4 flex items-center justify-between flex-shrink-0">
        <div className="flex items-center gap-2 min-w-0">
          <h2 className="text-nav font-semibold text-[var(--text-primary)] truncate">Sessions</h2>
          <span className="text-helper text-[var(--text-muted)] flex-shrink-0">{sessions.length} total</span>
          {liveCount > 0 && (
            <span className="text-helper text-[var(--success)] flex-shrink-0 flex items-center gap-1">
              <span className="w-1.5 h-1.5 rounded-full bg-[var(--success)] animate-pulse" />
              {liveCount} live
            </span>
          )}
        </div>
        <div className="flex items-center gap-1 flex-shrink-0">
          {!agentFilter && (
            <div className="flex rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] p-0.5">
              {(['grouped', 'flat'] as const).map(mode => (
                <button
                  key={mode}
                  onClick={() => setViewMode(mode)}
                  aria-pressed={viewMode === mode}
                  className={`h-6 px-2 rounded-sm text-meta transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
                    viewMode === mode ? 'bg-[var(--bg-surface)] text-[var(--text-primary)] shadow-sm' : 'text-[var(--text-muted)] hover:text-[var(--text-primary)]'
                  }`}
                >
                  {mode === 'grouped' ? '分组' : '平铺'}
                </button>
              ))}
            </div>
          )}
          {isMobile && onClose && (
            <button
              onClick={onClose}
              aria-label="关闭会话列表"
              className="ml-1 w-6 h-6 flex items-center justify-center rounded-md text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <line x1="18" y1="6" x2="6" y2="18" />
                <line x1="6" y1="6" x2="18" y2="18" />
              </svg>
            </button>
          )}
        </div>
      </div>

      {/* Filter Bar */}
      <div className="px-4 pb-2 flex-shrink-0">
        <div className="relative">
          <svg className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-[var(--text-muted)] pointer-events-none" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <circle cx="11" cy="11" r="8" />
            <line x1="21" y1="21" x2="16.65" y2="16.65" />
          </svg>
          <input
            ref={searchRef}
            type="text"
            placeholder="过滤会话..."
            value={query}
            onChange={e => setQuery(e.target.value)}
            className="w-full h-[34px] rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] pl-8 pr-7 text-body text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none focus:ring-2 focus:ring-[var(--accent-blue)]/20 focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg-surface)]"
          />
          {query && (
            <button
              onClick={() => setQuery('')}
              className="absolute right-1 top-1/2 -translate-y-1/2 w-5 h-5 flex items-center justify-center rounded-sm text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast"
              aria-label="清除搜索"
            >
              &times;
            </button>
          )}
        </div>
      </div>

      {/* Agent Filter */}
      <AgentFilter
        agents={effectiveAgents}
        selected={agentFilter}
        onSelect={setAgentFilter}
      />

      {/* Session List */}
      <div className="px-2 pb-4 flex-1 overflow-y-auto">
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
            {query && (
              <button
                onClick={() => setQuery('')}
                className="mt-3 h-7 px-3 rounded-md border border-[var(--border-default)] text-meta text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
              >
                清除筛选
              </button>
            )}
          </div>
        ) : agentFilter ? (
          <div className="space-y-1">
            {filtered.map(s => <SessionRow key={s.id} session={s} />)}
          </div>
        ) : viewMode === 'flat' ? (
          <div className="space-y-1">
            {filtered.map(s => <SessionRow key={s.id} session={s} />)}
          </div>
        ) : (
          agentGroups.map(([agent, list]) => {
            const collapsed = collapsedGroups.has(agent)
            return (
              <div key={agent} className="mb-3">
                <button
                  onClick={() => toggleGroup(agent)}
                  className="sticky top-0 z-10 w-full px-2 py-1 text-section uppercase tracking-[0.04em] text-[var(--text-muted)] flex items-center justify-between bg-[var(--bg-surface)] hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast rounded-sm cursor-pointer"
                  aria-expanded={!collapsed}
                >
                  <span className="flex items-center gap-1">
                    <svg className={`w-2.5 h-2.5 transition-transform duration-fast ${collapsed ? '' : 'rotate-90'}`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
                      <polyline points="9 18 15 12 9 6" />
                    </svg>
                    <AgentIcon agentType={list[0]?.agent_type} size={16} />
                    <span>{agent}</span>
                  </span>
                  <span>{list.length}</span>
                </button>
                {!collapsed && (
                  <div className="space-y-1">
                    {list.map(s => <SessionRow key={s.id} session={s} />)}
                  </div>
                )}
              </div>
            )
          })
        )}
      </div>

      {/* Resize handle (desktop only) */}
      {!isMobile && (
        <div
          onPointerDown={beginResize}
          className="absolute right-0 top-0 h-full w-1 cursor-grab hover:bg-[var(--accent-blue)]/30 active:cursor-grabbing"
          title="拖拽调整侧栏宽度"
        />
      )}

      {/* Toast */}
      {toast && (
        <div className="absolute bottom-4 left-1/2 -translate-x-1/2 z-[var(--z-toast)] px-3 py-1.5 rounded-md bg-[var(--text-primary)] text-[var(--text-inverse)] text-meta shadow-lg animate-pulse">
          {toast}
        </div>
      )}
    </aside>
  )
}
