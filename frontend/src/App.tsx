import { useState, useRef } from 'react'
import Sidebar from './components/Sidebar'
import MiniMap from './components/MiniMap'
import ReplayView from './components/ReplayView'
import type { TurnVM } from './types'

export default function App() {
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [turns, setTurns] = useState<TurnVM[]>([])
  const [visibleRange, setVisibleRange] = useState<{ start: number; end: number }>()
  const scrollToIndexRef = useRef<((index: number) => void) | null>(null)

  return (
    <div className="h-screen flex overflow-hidden bg-[var(--bg-primary)]">
      <Sidebar selectedId={selectedId} onSelect={setSelectedId} />
      <MiniMap turns={turns} visibleRange={visibleRange} scrollToIndexRef={scrollToIndexRef} />
      <ReplayView
        sessionId={selectedId}
        onTurnsChange={setTurns}
        onVisibleRangeChange={setVisibleRange}
        scrollToIndexRef={scrollToIndexRef}
      />
    </div>
  )
}
