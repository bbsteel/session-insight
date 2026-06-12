import { useState } from 'react'
import Sidebar from './components/Sidebar'
import MiniMap from './components/MiniMap'
import ReplayView from './components/ReplayView'
import type { TurnVM } from './types'

export default function App() {
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [turns, setTurns] = useState<TurnVM[]>([])

  return (
    <div className="h-screen flex overflow-hidden bg-[var(--bg-primary)]">
      <Sidebar selectedId={selectedId} onSelect={setSelectedId} />
      <MiniMap turns={turns} />
      <ReplayView sessionId={selectedId} onTurnsChange={setTurns} />
    </div>
  )
}
