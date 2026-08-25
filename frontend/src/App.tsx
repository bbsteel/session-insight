import { useCallback, useEffect, useRef, useState } from 'react'
import Sidebar from './components/Sidebar'
import ReplayView from './components/ReplayView'
import FileViewer from './components/FileViewer'
import CodingQuotaDialog from './components/CodingQuotaDialog'
import SnippetPage from './components/SnippetPage'
import { PanelLeftOpenIcon } from './components/icons'
import type { BookmarkChange } from './bookmarkState'
import { useI18n } from './i18n'
import { parseSessionRoute } from './sessionLink'

const SIDEBAR_HIDDEN_KEY = 'si-sidebar-hidden'

function readSidebarHidden(): boolean {
  try {
    return localStorage.getItem(SIDEBAR_HIDDEN_KEY) === '1'
  } catch {
    return false
  }
}

function writeSidebarHidden(hidden: boolean): void {
  try {
    localStorage.setItem(SIDEBAR_HIDDEN_KEY, hidden ? '1' : '0')
  } catch {
    // Storage is optional; keep the in-memory UI state working.
  }
}

function modifierShortcut(key: string): string {
  const platform =
    typeof navigator !== 'undefined'
      ? ((navigator as Navigator & { userAgentData?: { platform?: string } }).userAgentData?.platform
        || navigator.userAgent
        || navigator.platform
        || '')
      : ''
  return /Mac|iPhone|iPad|iPod/i.test(platform) ? `⌘${key}` : `Ctrl+${key}`
}

// Hash route for the new-tab file viewer (#/file?path=…&cwd=…): the Go embed
// file server only knows "/", so client-side hash routing keeps it zero-config.
function parseFileRoute(): { path: string; cwd: string; line?: number } | null {
  const hash = window.location.hash
  if (!hash.startsWith('#/file?')) return null
  const params = new URLSearchParams(hash.slice('#/file?'.length))
  const path = params.get('path')
  if (!path) return null
  const rawLine = Number(params.get('line'))
  return { path, cwd: params.get('cwd') ?? '', line: Number.isInteger(rawLine) && rawLine > 0 ? rawLine : undefined }
}

export default function App() {
  const { t } = useI18n()
  const [hash, setHash] = useState(() => window.location.hash)
  const currentHashRef = useRef(window.location.hash)
  const snippetReturnHashRef = useRef<string | null>(null)
  useEffect(() => {
    const updateHash = () => {
      const nextHash = window.location.hash
      if (nextHash === '#/snippets' && currentHashRef.current !== '#/snippets') {
        snippetReturnHashRef.current = currentHashRef.current
      }
      currentHashRef.current = nextHash
      setHash(nextHash)
    }
    window.addEventListener('hashchange', updateHash)
    return () => window.removeEventListener('hashchange', updateHash)
  }, [])
  const fileRoute = parseFileRoute()
  const snippetsRoute = hash === '#/snippets'
  // #/session/<agentType>/<id> opens a specific session directly (new-tab
  // entry point); parsed once at mount like the file route.
  const [sessionRoute] = useState(() => (fileRoute || snippetsRoute ? null : parseSessionRoute(window.location.hash)))
  const [selectedId, setSelectedId] = useState<string | null>(sessionRoute?.id ?? null)
  const [selectedAgentType, setSelectedAgentType] = useState<string | null>(sessionRoute?.agentType ?? null)
  const [sidebarHidden, setSidebarHidden] = useState(readSidebarHidden)
  const [showCodingQuotas, setShowCodingQuotas] = useState(false)
  const [bookmarkChange, setBookmarkChange] = useState<BookmarkChange | null>(null)
  const [sidebarFocusTarget, setSidebarFocusTarget] = useState<{ id: string; agentType: string } | null>(
    sessionRoute ? { id: sessionRoute.id, agentType: sessionRoute.agentType } : null,
  )
  const [searchTarget, setSearchTarget] = useState<{ sessionId: string; agentType: string; query: string } | null>(null)
  // Root ancestor of a subagent session opened from global search: ReplayView
  // shows the child transcript but offers a back-to-parent breadcrumb.
  const [searchRootRef, setSearchRootRef] = useState<{ sessionId: string; childAgentType: string; root: { id: string; agentType: string; name: string } } | null>(null)
  const sessionListShortcut = modifierShortcut('B')

  const persistSidebarHidden = useCallback((hidden: boolean) => {
    writeSidebarHidden(hidden)
    setSidebarHidden(hidden)
  }, [])

  const toggleSessionList = useCallback(() => {
    setSidebarHidden(hidden => {
      const nextHidden = !hidden
      writeSidebarHidden(nextHidden)
      return nextHidden
    })
  }, [])

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.isComposing || event.repeat || event.altKey || event.shiftKey) return
      if (!(event.ctrlKey || event.metaKey)) return
      if (event.key !== 'b' && event.key !== 'B') return
      event.preventDefault()
      toggleSessionList()
    }
    document.addEventListener('keydown', onKey, true)
    return () => document.removeEventListener('keydown', onKey, true)
  }, [toggleSessionList])

  const selectSession = (id: string, agentType?: string, focusSidebar = false, searchQuery?: string, rootRef?: { id: string; agentType: string; name: string }) => {
    setSelectedId(id)
    setSelectedAgentType(agentType ?? null)
    // The sidebar lists root sessions only: a subagent hit lands on its root
    // ancestor (rootRef), everything else lands on itself.
    const landing = rootRef && agentType ? { id: rootRef.id, agentType: rootRef.agentType } : { id, agentType: agentType ?? '' }
    setSidebarFocusTarget(focusSidebar && agentType ? landing : null)
    setSearchTarget(focusSidebar && agentType && searchQuery ? { sessionId: id, agentType, query: searchQuery } : null)
    setSearchRootRef(focusSidebar && rootRef && agentType ? { sessionId: id, childAgentType: agentType, root: rootRef } : null)
    if (focusSidebar) persistSidebarHidden(false)
  }

  if (fileRoute) {
    return <FileViewer path={fileRoute.path} cwd={fileRoute.cwd} line={fileRoute.line} />
  }

  return (
    <div className="h-screen flex flex-row overflow-hidden bg-[var(--bg-primary)]">
      <div className={sidebarHidden ? 'hidden' : 'h-full'}>
        <Sidebar
          selectedId={selectedId}
          selectedAgentType={selectedAgentType}
          focusTarget={sidebarFocusTarget}
          onSelect={selectSession}
          onHide={() => persistSidebarHidden(true)}
          sessionListShortcut={sessionListShortcut}
          bookmarkChange={bookmarkChange}
          onBookmarkChange={setBookmarkChange}
          onSessionDeleted={(session) => {
            if (session.id === selectedId) {
              setSelectedId(null)
              setSelectedAgentType(null)
            }
          }}
        />
      </div>
      {sidebarHidden && (
        <div
          className="flex h-full w-8 flex-shrink-0 flex-col items-center border-r border-[var(--border-default)] bg-[var(--bg-surface)] pt-2"
          data-testid="sidebar-show-rail"
        >
          <button
            type="button"
            onClick={() => persistSidebarHidden(false)}
            className="h-7 w-7 flex items-center justify-center rounded-md text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
            aria-label={t('app.openSessions')}
            title={t('app.openSessionsTitle', { shortcut: sessionListShortcut })}
            aria-controls="session-sidebar"
            aria-expanded={false}
            data-testid="sidebar-show"
          >
            <PanelLeftOpenIcon />
          </button>
        </div>
      )}
      <div className="relative isolate flex min-h-0 min-w-0 flex-1 overflow-hidden">
        <ReplayView
          sessionId={selectedId}
          searchTarget={searchTarget}
          searchRootRef={searchRootRef}
          onSelect={selectSession}
          onOpenCodingQuotas={() => setShowCodingQuotas(true)}
          bookmarkChange={bookmarkChange}
          onBookmarkChange={setBookmarkChange}
        />
        {snippetsRoute && (
          <div className="absolute inset-0 z-[220] overflow-hidden bg-[var(--bg-primary)]" data-testid="snippets-overlay">
            <SnippetPage
              onBack={() => {
                const returnHash = snippetReturnHashRef.current ?? ''
                snippetReturnHashRef.current = null
                window.location.hash = returnHash
              }}
              onOpenSource={(snippet) => {
                selectSession(snippet.session_id, snippet.agent_type)
                const returnHash = snippetReturnHashRef.current ?? ''
                snippetReturnHashRef.current = null
                window.location.hash = returnHash
              }}
            />
          </div>
        )}
      </div>
      {showCodingQuotas && <CodingQuotaDialog onClose={() => setShowCodingQuotas(false)} />}
    </div>
  )
}
