import { useState } from 'react'
import Sidebar from './components/Sidebar'
import ReplayView from './components/ReplayView'
import FileViewer from './components/FileViewer'
import type { BookmarkChange } from './bookmarkState'

// Hash route for the new-tab file viewer (#/file?path=…&cwd=…): the Go embed
// file server only knows "/", so client-side hash routing keeps it zero-config.
function parseFileRoute(): { path: string; cwd: string } | null {
  const hash = window.location.hash
  if (!hash.startsWith('#/file?')) return null
  const params = new URLSearchParams(hash.slice('#/file?'.length))
  const path = params.get('path')
  if (!path) return null
  return { path, cwd: params.get('cwd') ?? '' }
}

export default function App() {
  const [fileRoute] = useState(parseFileRoute)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [selectedAgentType, setSelectedAgentType] = useState<string | null>(null)
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const [bookmarkChange, setBookmarkChange] = useState<BookmarkChange | null>(null)
  const [sidebarFocusTarget, setSidebarFocusTarget] = useState<{ id: string; agentType: string } | null>(null)
  const [searchTarget, setSearchTarget] = useState<{ sessionId: string; agentType: string; query: string } | null>(null)

  const selectSession = (id: string, agentType?: string, focusSidebar = false, searchQuery?: string) => {
    setSelectedId(id)
    setSelectedAgentType(agentType ?? null)
    setSidebarFocusTarget(focusSidebar && agentType ? { id, agentType } : null)
    setSearchTarget(focusSidebar && agentType && searchQuery ? { sessionId: id, agentType, query: searchQuery } : null)
    setSidebarOpen(false)
  }

  if (fileRoute) {
    return <FileViewer path={fileRoute.path} cwd={fileRoute.cwd} />
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
        <Sidebar
          selectedId={selectedId}
          selectedAgentType={selectedAgentType}
          focusTarget={sidebarFocusTarget}
          onSelect={selectSession}
          drawer={sidebarOpen}
          onClose={() => setSidebarOpen(false)}
          bookmarkChange={bookmarkChange}
          onBookmarkChange={setBookmarkChange}
        />
      </div>
      <div className="flex min-h-0 min-w-0 flex-1 overflow-hidden">
        <ReplayView
          sessionId={selectedId}
          searchTarget={searchTarget}
          onSelect={selectSession}
          bookmarkChange={bookmarkChange}
          onBookmarkChange={setBookmarkChange}
        />
      </div>
    </div>
  )
}
