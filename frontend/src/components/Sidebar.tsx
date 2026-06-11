import { useEffect, useState } from 'react'
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

  // Group by agent_type
  const grouped = new Map<string, SessionSummary[]>()
  for (const s of sessions) {
    const list = grouped.get(s.agent_type) || []
    list.push(s)
    grouped.set(s.agent_type, list)
  }

  return (
    <aside
      className="h-full flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-surface)] overflow-y-auto"
      style={{ width: '260px' }}
    >
      <div className="p-4">
        <h2 className="text-nav font-semibold text-[var(--text-primary)]">
          Sessions ({sessions.length})
        </h2>
      </div>

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
                <div className="text-body text-[var(--text-primary)] truncate">
                  {getSessionName(s)}
                </div>
                <div className="text-helper text-[var(--text-secondary)] mt-0.5">
                  {s.message_count > 0 ? `${s.message_count} 条消息` : ''}
                  {s.message_count > 0 && ' · '}
                  {timeAgo(s.updated_at)}
                </div>
              </div>
            ))}
          </div>
        ))}
      </div>
    </aside>
  )
}
