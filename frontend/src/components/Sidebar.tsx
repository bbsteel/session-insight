import { useEffect, useState } from 'react'
import { fetchSessions } from '../api'
import type { SessionSummary } from '../types'
import ThemeToggle from './ThemeToggle'

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

function getSessionName(s: SessionSummary): string {
  if (s.name) return s.name
  if (s.repository) return s.repository
  return s.id.slice(0, 8)
}

interface SidebarProps {
  selectedId: string | null
  onSelect: (id: string) => void
}

export default function Sidebar({ selectedId, onSelect }: SidebarProps) {
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [loading, setLoading] = useState(true)
  const [query, setQuery] = useState('')

  useEffect(() => {
    fetchSessions()
      .then(data => setSessions(data))
      .catch(console.error)
      .finally(() => setLoading(false))
  }, [])

  if (loading) {
    return (
      <aside
        className="h-full flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-surface)]"
        style={{ width: '260px' }}
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

  // Collect unique repos
  const allRepos = [...new Set(sessions.map(s => s.repository).filter(Boolean))].sort()

  // Filter sessions based on query + repo
  const filtered = sessions.filter(s => {
    if (query.trim()) {
      const q = query.toLowerCase()
      const name = getSessionName(s).toLowerCase()
      const repo = (s.repository || '').toLowerCase()
      if (!name.includes(q) && !repo.includes(q)) return false
    }
    return true
  })

  // Group by agent_type
  const grouped = new Map<string, SessionSummary[]>()
  for (const s of filtered) {
    const list = grouped.get(s.agent_type) || []
    list.push(s)
    grouped.set(s.agent_type, list)
  }

  return (
    <aside
      className="h-full flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-surface)] overflow-y-auto"
      style={{ width: '260px' }}
    >
      <div className="p-4 flex items-center justify-between">
        <h2 className="text-nav font-semibold text-[var(--text-primary)]">
          {query ? `Results (${filtered.length}/${sessions.length})` : `Sessions (${sessions.length})`}
        </h2>
        <ThemeToggle />
      </div>

      <div className="px-4 pb-2">
        <div className="relative">
          <input
            type="text"
            placeholder="Search sessions..."
            value={query}
            onChange={e => setQuery(e.target.value)}
            className="w-full h-[28px] rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] px-2 pr-6 text-nav text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none focus:ring-2 focus:ring-[var(--accent-blue)]/20"
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

      {/* Repo filter chips */}
      {allRepos.length > 1 && (
        <div className="px-4 pb-2 flex flex-wrap gap-1">
          {allRepos.map(repo => {
            const count = sessions.filter(s => s.repository === repo).length
            return (
              <button
                key={repo}
                onClick={() => setQuery(q => q === repo ? '' : repo)}
                className={`text-meta px-1.5 py-0.5 rounded-sm transition-colors duration-fast truncate max-w-[200px] ${
                  query === repo
                    ? 'bg-[var(--accent-blue)]/10 text-[var(--accent-blue)]'
                    : 'bg-[var(--bg-inset)] text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
                }`}
                title={`${repo} (${count} sessions)`}
              >
                {repo.split('/').pop()} <span className="opacity-60">{count}</span>
              </button>
            )
          })}
        </div>
      )}

      <div className="px-2 pb-4">
        {Array.from(grouped.entries()).map(([agent, list]) => (
          <div key={agent} className="mb-3">
            <div className="px-2 py-1 text-section text-[var(--text-muted)]">
              {agent} &middot; {list.length}
            </div>
            {list.map(s => (
              <div
                key={s.id}
                onClick={() => onSelect(s.id)}
                className={`px-2 py-1.5 rounded-sm cursor-pointer transition-colors duration-fast ${
                  s.id === selectedId
                    ? 'bg-[var(--bg-surface-hover)]'
                    : 'hover:bg-[var(--bg-surface-hover)]'
                }`}
              >
                <div className="text-body text-[var(--text-primary)] truncate flex items-center gap-1">
                  {s.is_live && <span className="w-1.5 h-1.5 rounded-full bg-[var(--success)] flex-shrink-0 animate-pulse" title="Live session" />}
                  {getSessionName(s)}
                </div>
                <div className="text-helper text-[var(--text-secondary)] mt-0.5">
                  {s.is_live ? '进行中' : (
                    <>
                      {s.message_count > 0 && `${s.message_count} 条消息 · `}
                      {timeAgo(s.updated_at)}
                    </>
                  )}
                </div>
              </div>
            ))}
          </div>
        ))}
      </div>
    </aside>
  )
}
