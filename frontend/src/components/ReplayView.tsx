import { useEffect, useState } from 'react'
import { fetchSession } from '../api'
import type { SessionDetail, TurnVM } from '../types'
import TurnCard from './TurnCard'

interface Props {
  sessionId: string | null
  onTurnsChange?: (turns: TurnVM[]) => void
  onVisibleRangeChange?: (range: any) => void
  scrollToIndexRef?: any
}

export default function ReplayView({ sessionId, onTurnsChange }: Props) {
  const [session, setSession] = useState<SessionDetail | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (!sessionId) { setSession(null); onTurnsChange?.([]); return }
    setLoading(true)
    fetchSession(sessionId).then(data => { setSession(data); onTurnsChange?.(data.turns) }).catch(console.error).finally(() => setLoading(false))
  }, [sessionId])

  if (!sessionId) return <main className="flex-1 min-w-[360px] bg-[var(--bg-surface)] flex items-center justify-center"><p className="text-body text-[var(--text-muted)]">Select a session</p></main>
  if (loading) return <main className="flex-1 min-w-[360px] bg-[var(--bg-surface)]"><p className="p-4 text-body text-[var(--text-muted)]">Loading...</p></main>
  if (!session) return <main className="flex-1 min-w-[360px] bg-[var(--bg-surface)]"><p className="p-4 text-body text-[var(--text-muted)]">No data</p></main>

  return (
    <main className="flex-1 flex flex-col min-w-[360px] overflow-hidden">
      <header className="flex-shrink-0 border-b border-[var(--border-default)] bg-[var(--bg-surface)] flex items-center px-4" style={{ height: '40px' }}>
        <span className="text-nav font-semibold text-[var(--text-primary)]">{session.name || session.id.slice(0, 8)}</span>
        <span className="text-helper text-[var(--text-muted)] ml-3">{session.turn_count} turns</span>
      </header>
      <div style={{ flex: 1, overflow: 'auto' }}>
        {session.turns.map((turn, i) => (
          <TurnCard key={i} turn={turn} />
        ))}
      </div>
    </main>
  )
}
