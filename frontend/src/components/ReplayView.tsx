import { useEffect, useState, useRef } from 'react'
import { Virtuoso } from 'react-virtuoso'
import type { VirtuosoHandle } from 'react-virtuoso'
import { fetchSession } from '../api'
import type { SessionDetail, TurnVM } from '../types'
import TurnCard from './TurnCard'
import AnalyticsView from './AnalyticsView'

interface Props {
  sessionId: string | null
  onTurnsChange?: (turns: TurnVM[]) => void
  onVisibleRangeChange?: (range: { start: number; end: number }) => void
  scrollToIndexRef?: React.MutableRefObject<((index: number) => void) | null>
}

function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return String(n)
}

function formatDuration(ms: number): string {
  const totalSeconds = Math.floor(ms / 1000)
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  if (hours > 0) return `${hours}h ${minutes}m`
  if (minutes > 0) return `${minutes}m ${totalSeconds % 60}s`
  return `${totalSeconds}s`
}

export default function ReplayView({ sessionId, onTurnsChange, onVisibleRangeChange, scrollToIndexRef }: Props) {
  const [session, setSession] = useState<SessionDetail | null>(null)
  const [loading, setLoading] = useState(false)
  const [visibleRange, setVisibleRange] = useState<{ start: number; end: number }>()
  const [mode, setMode] = useState<'full' | 'digest'>('full')
  const [density, setDensity] = useState<'standard' | 'tight'>('standard')
  const [showAnalytics, setShowAnalytics] = useState(false)
  const virtuosoRef = useRef<VirtuosoHandle>(null)

  useEffect(() => {
    if (scrollToIndexRef && virtuosoRef.current) {
      scrollToIndexRef.current = (index: number) => {
        virtuosoRef.current?.scrollToIndex({ index, align: 'start', behavior: 'smooth' })
      }
    }
  }, [scrollToIndexRef, session])

  // Keyboard navigation
  useEffect(() => {
    if (!sessionId || !session?.turns.length) return
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return
      if (e.key === 'j' || e.key === 'ArrowDown') {
        e.preventDefault()
        if (visibleRange && visibleRange.end < session.turns.length - 1)
          virtuosoRef.current?.scrollToIndex({ index: visibleRange.start + 1, align: 'start', behavior: 'smooth' })
      } else if (e.key === 'k' || e.key === 'ArrowUp') {
        e.preventDefault()
        if (visibleRange && visibleRange.start > 0)
          virtuosoRef.current?.scrollToIndex({ index: visibleRange.start - 1, align: 'start', behavior: 'smooth' })
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [sessionId, session, visibleRange])

  useEffect(() => {
    if (!sessionId) { setSession(null); onTurnsChange?.([]); return }
    setLoading(true)
    fetchSession(sessionId).then(data => { setSession(data); onTurnsChange?.(data.turns) }).catch(console.error).finally(() => setLoading(false))
  }, [sessionId])

  if (!sessionId) return (
    <main className="flex-1 flex items-center justify-center min-w-[360px] bg-[var(--bg-surface)]">
      <div className="text-center"><div className="text-4xl mb-4 opacity-40">&#128269;</div>
        <h3 className="text-body font-medium text-[var(--text-primary)]">Select a session</h3>
        <p className="text-helper text-[var(--text-muted)] mt-1">Choose a session from the sidebar to view its replay.</p>
        <div className="mt-4 flex gap-2 justify-center text-meta text-[var(--text-muted)]">
          <span><kbd className="bg-[var(--bg-inset)] px-1 py-0.5 rounded-sm border border-[var(--border-default)]">j</kbd>/<kbd className="bg-[var(--bg-inset)] px-1 py-0.5 rounded-sm border border-[var(--border-default)]">k</kbd> navigate turns</span>
          <span>&middot;</span>
          <span><kbd className="bg-[var(--bg-inset)] px-1 py-0.5 rounded-sm border border-[var(--border-default)]">?</kbd> shortcuts</span>
        </div>
      </div>
    </main>
  )
  if (loading) return (
    <main className="flex-1 min-w-[360px] bg-[var(--bg-surface)]">
      <div className="p-4 space-y-2">{Array.from({ length: 4 }).map((_, i) => <div key={i} className="h-24 bg-[var(--bg-surface-hover)] rounded-sm animate-pulse" />)}</div>
    </main>
  )
  if (!session || !session.turns.length) return (
    <main className="flex-1 min-w-[360px] bg-[var(--bg-surface)] flex items-center justify-center">
      <p className="text-helper text-[var(--text-muted)]">No turns found</p>
    </main>
  )

  const totalTokens = session.turns.reduce((sum, t) => sum + t.token_usage.prompt_tokens + t.token_usage.completion_tokens, 0)
  const modelName = session.model_name || session.agent_type || 'unknown'
  const sessionDuration = formatDuration(session.turns.reduce((sum, t) => sum + t.duration_ms, 0))

  return (
    <main className="flex-1 flex flex-col min-w-[360px] overflow-hidden">
      <header className="flex-shrink-0 border-b border-[var(--border-default)] bg-[var(--bg-surface)] flex items-center px-3" style={{ height: '32px' }}>
        <div className="flex items-center gap-2">
          <button onClick={() => setMode(m => m === 'full' ? 'digest' : 'full')} className="text-nav text-[var(--text-secondary)] hover:text-[var(--text-primary)]">{mode === 'full' ? 'Full' : 'Digest'}</button>
          <button onClick={() => setDensity(d => d === 'standard' ? 'tight' : 'standard')} className="text-nav text-[var(--text-secondary)] hover:text-[var(--text-primary)]">{density === 'standard' ? 'Std' : 'Tight'}</button>
          <span className="text-[var(--border-default)]">|</span>
          <button onClick={() => setShowAnalytics(a => !a)} className={`text-nav ${showAnalytics ? 'text-[var(--accent-blue)]' : 'text-[var(--text-secondary)]'} hover:text-[var(--text-primary)]`}>
            {showAnalytics ? 'Replay' : 'Stats'}
          </button>
        </div>
        <span className="flex-1 text-center text-helper text-[var(--text-secondary)] truncate px-2">
          {modelName} &middot; {fmtTokens(totalTokens)} tok &middot; {session.turn_count} turns &middot; {sessionDuration}
        </span>
        <span className="flex-shrink-0 text-meta text-[var(--text-muted)]">
          Turn {visibleRange ? `${visibleRange.start + 1}-${visibleRange.end + 1}` : '?'}/{session.turn_count}
        </span>
      </header>
      {showAnalytics ? (
        <AnalyticsView sessionId={session.id} />
      ) : (
        <Virtuoso
          ref={virtuosoRef}
          style={{ flex: 1 }}
          data={session.turns}
          rangeChanged={(range) => {
            const newRange = { start: range.startIndex, end: range.endIndex }
            setVisibleRange(newRange)
            onVisibleRangeChange?.(newRange)
          }}
          itemContent={(_: number, turn: TurnVM) => <TurnCard turn={turn} mode={mode} density={density} />}
        />
      )}
    </main>
  )
}
