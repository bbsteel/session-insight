import { useState, useRef } from 'react'
import Sidebar from './components/Sidebar'
import MiniMap from './components/MiniMap'
import ReplayView from './components/ReplayView'
import type { TurnVM } from './types'

type ReplayScrollBehavior = 'auto' | 'smooth'

export default function App() {
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [turns, setTurns] = useState<TurnVM[]>([])
  const [visibleRange, setVisibleRange] = useState<{ start: number; end: number }>()
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const scrollToIndexRef = useRef<((index: number, behavior?: ReplayScrollBehavior) => void) | null>(null)

  const selectSession = (id: string) => {
    setSelectedId(id)
    setSidebarOpen(false)
  }

  return (
    <div className="h-screen flex flex-col md:flex-row overflow-hidden bg-[var(--bg-primary)]">
      <button
        className="md:hidden fixed left-2 top-2 z-[250] h-7 w-7 rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] text-[var(--text-secondary)] shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
        onClick={() => setSidebarOpen(true)}
        aria-label="打开会话列表"
      >
        ☰
      </button>
      {sidebarOpen && (
        <div
          className="md:hidden fixed inset-0 z-[240] bg-[rgba(0,0,0,var(--opacity-overlay))]"
          onClick={() => setSidebarOpen(false)}
        />
      )}
      <div className={`${sidebarOpen ? 'translate-x-0' : '-translate-x-full'} md:translate-x-0 fixed md:static inset-y-0 left-0 z-[260] transition-transform duration-normal md:block`}>
        <Sidebar selectedId={selectedId} onSelect={selectSession} />
      </div>
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
