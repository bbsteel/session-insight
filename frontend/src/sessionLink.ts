// Session deep-link helpers. The Go embed file server only knows "/", so
// sessions are addressed with a client-side hash route (same pattern as the
// #/file viewer route in App.tsx): #/session/<agentType>/<id>. Composite
// identity — session IDs are only unique within an agent.

export interface SessionRoute {
  agentType: string
  id: string
}

export function sessionHash(agentType: string, id: string): string {
  return `#/session/${encodeURIComponent(agentType)}/${encodeURIComponent(id)}`
}

export function sessionHref(agentType: string, id: string): string {
  return `${window.location.origin}${window.location.pathname}${sessionHash(agentType, id)}`
}

export function openSessionInNewTab(agentType: string, id: string): void {
  window.open(sessionHref(agentType, id), '_blank', 'noopener')
}

// parseSessionRoute reads a #/session/<agentType>/<id> hash. Returns null for
// any other hash or malformed route.
export function parseSessionRoute(hash: string): SessionRoute | null {
  if (!hash.startsWith('#/session/')) return null
  const rest = hash.slice('#/session/'.length)
  const slash = rest.indexOf('/')
  if (slash <= 0) return null
  const rawAgent = rest.slice(0, slash)
  const rawId = rest.slice(slash + 1)
  // Exactly two segments: a second slash means a malformed route.
  if (!rawAgent || !rawId || rawId.includes('/')) return null
  let agentType: string
  let id: string
  try {
    agentType = decodeURIComponent(rawAgent)
    id = decodeURIComponent(rawId)
  } catch {
    // Malformed percent-encoding must degrade to "no route", not crash the
    // app (App.tsx calls this in a useState initializer on load).
    return null
  }
  if (!agentType || !id) return null
  return { agentType, id }
}

// shouldOpenInNewTab reports whether a click event requests a new tab:
// Ctrl/Cmd+click (either platform convention) or a middle-click delivered via
// auxclick (button 1).
export function shouldOpenInNewTab(e: { metaKey: boolean; ctrlKey: boolean; button?: number }): boolean {
  return e.metaKey || e.ctrlKey || e.button === 1
}

// openOnModifiedClick opens the session in a new tab when the click carries a
// new-tab modifier, returning true when the event was handled (caller must
// skip its in-place navigation). Plain clicks return false.
export function openOnModifiedClick(
  e: { metaKey: boolean; ctrlKey: boolean; button?: number; preventDefault: () => void },
  agentType: string,
  id: string,
): boolean {
  if (!shouldOpenInNewTab(e)) return false
  e.preventDefault()
  openSessionInNewTab(agentType, id)
  return true
}
