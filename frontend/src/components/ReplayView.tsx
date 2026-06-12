import { useEffect, useState, useRef } from 'react'
import { Virtuoso } from 'react-virtuoso'
import type { VirtuosoHandle } from 'react-virtuoso'
import { fetchSession } from '../api'
import type { SessionDetail, TurnVM } from '../types'
import TurnCard from './TurnCard'

interface Props {
  sessionId: string | null
  onTurnsChange?: (turns: TurnVM[]) => void
  onVisibleRangeChange?: (range: { start: number; end: number }) => void
  scrollToIndexRef?: React.MutableRefObject<((index: number) => void) | null>
}

export default function ReplayView({ sessionId, onTurnsChange, onVisibleRangeChange, scrollToIndexRef }: Props) {
  const [session, setSession] = useState<SessionDetail | null>(null)
  const [loading, setLoading] = useState(false)
  const [visibleRange, setVisibleRange] = useState<{ start: number; end: number }>()
  const virtuosoRef = useRef<VirtuosoHandle>(null)

  useEffect(() => {
    if (!sessionId) {
      setSession(null)
      onTurnsChange?.([])
      return
    }
    setLoading(true)
    fetchSession(sessionId)
      .then(data => {
        setSession(data)
        onTurnsChange?.(data.turns)
      })
      .catch(console.error)
      .finally(() => setLoading(false))
  }, [sessionId])

  useEffect(() => {
    if (scrollToIndexRef && virtuosoRef.current) {
      scrollToIndexRef.current = (index: number) => {
        virtuosoRef.current?.scrollToIndex({ index, align: 'start', behavior: 'smooth' })
      }
    }
  }, [scrollToIndexRef, session])

  if (!sessionId) {
    return (
      <main className="flex-1 flex flex-col min-w-[360px] overflow-hidden">
        <header className="flex-shrink-0 border-b border-[var(--border-default)] bg-[var(--bg-surface)] flex items-center px-4" style={{ height: '40px' }}>
          <span className="text-nav font-semibold text-[var(--text-primary)]">SessionInsight</span>
        </header>
        <div className="flex-1 flex items-center justify-center">
          <div className="text-center">
            <div className="text-4xl mb-4 opacity-40">&#128269;</div>
            <h3 className="text-body font-medium text-[var(--text-primary)]">Select a session</h3>
            <p className="text-helper text-[var(--text-muted)] mt-1">Choose a session from the sidebar to view its replay.</p>
          </div>
        </div>
      </main>
    )
  }

  if (loading) {
    return (
      <main className="flex-1 flex flex-col min-w-[360px] overflow-hidden">
        <header className="flex-shrink-0 border-b border-[var(--border-default)] bg-[var(--bg-surface)] flex items-center px-4" style={{ height: '40px' }}>
          <span className="text-nav font-semibold text-[var(--text-primary)]">Loading...</span>
        </header>
        <div className="flex-1 p-4 space-y-2">
          {Array.from({ length: 4 }).map((_, i) => (
            <div key={i} className="h-24 bg-[var(--bg-surface-hover)] rounded-sm animate-pulse" />
          ))}
        </div>
      </main>
    )
  }

  if (!session || !session.turns.length) {
    return (
      <main className="flex-1 flex flex-col min-w-[360px] overflow-hidden">
        <header className="flex-shrink-0 border-b border-[var(--border-default)] bg-[var(--bg-surface)] flex items-center px-4" style={{ height: '40px' }}>
          <span className="text-nav font-semibold text-[var(--text-primary)]">{session?.name || 'Session'}</span>
        </header>
        <div className="flex-1 flex items-center justify-center">
          <p className="text-helper text-[var(--text-muted)]">No turns found</p>
        </div>
      </main>
    )
  }

  const name = session.name || session.repository || session.id.slice(0, 8)

  return (
    <main className="flex-1 flex flex-col min-w-[360px] overflow-hidden">
      <header className="flex-shrink-0 border-b border-[var(--border-default)] bg-[var(--bg-surface)] flex items-center px-4" style={{ height: '40px' }}>
        <span className="text-nav font-semibold text-[var(--text-primary)] truncate">{name}</span>
        <span className="text-helper text-[var(--text-muted)] ml-3 whitespace-nowrap">{session.turn_count} turns</span>
      </header>
      <Virtuoso
        ref={virtuosoRef}
        style={{ flex: 1 }}
        totalCount={session.turns.length}
        rangeChanged={(range) => {
          const newRange = { start: range.startIndex, end: range.endIndex }
          setVisibleRange(newRange)
          onVisibleRangeChange?.(newRange)
        }}
        itemContent={(index: number) => <TurnCard turn={session.turns[index]} />}
      />
    </main>
  )
}
