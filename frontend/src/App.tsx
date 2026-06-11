import Sidebar from './components/Sidebar'
import MiniMap from './components/MiniMap'
import ReplayView from './components/ReplayView'

export default function App() {
  return (
    <div className="h-screen flex overflow-hidden bg-[var(--bg-primary)]">
      <Sidebar />
      <MiniMap />
      <ReplayView />
    </div>
  )
}
