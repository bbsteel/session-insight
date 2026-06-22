import type { AgentInfo, EditCall, SearchResult, SessionDetail, SessionSummary } from './types'

export async function fetchSessions(agent?: string): Promise<SessionSummary[]> {
  const params = new URLSearchParams()
  if (agent) params.set('agent', agent)

  const res = await fetch(`/api/sessions?${params}`)
  if (!res.ok) throw new Error(`Failed to fetch sessions: ${res.status}`)
  return readJson<SessionSummary[]>(res, 'sessions')
}

export async function fetchSession(id: string): Promise<SessionDetail> {
  const res = await fetch(`/api/sessions/${id}`)
  if (!res.ok) throw new Error(`Failed to fetch session: ${res.status}`)
  return readJson<SessionDetail>(res, 'session')
}

export async function fetchAgents(): Promise<AgentInfo[]> {
  const res = await fetch('/api/agents')
  if (!res.ok) throw new Error(`Failed to fetch agents: ${res.status}`)
  return readJson<AgentInfo[]>(res, 'agents')
}

export async function fetchRenderANSI(id: string, cols?: number): Promise<string> {
  const url = cols ? `/api/sessions/${id}/render?cols=${cols}` : `/api/sessions/${id}/render`
  const res = await fetch(url)
  if (!res.ok) throw new Error(`Failed to fetch render: ${res.status}`)
  return res.text()
}

export async function fetchSessionEdits(id: string): Promise<EditCall[]> {
  const res = await fetch(`/api/sessions/${id}/edits`)
  if (!res.ok) throw new Error(`Failed to fetch edits: ${res.status}`)
  return res.json()
}

export async function fetchSearch(q: string): Promise<SearchResult[]> {
  const params = new URLSearchParams({ q })
  const res = await fetch(`/api/search?${params}`)
  if (!res.ok) throw new Error(`Search failed: ${res.status}`)
  return res.json()
}

async function readJson<T>(res: Response, label: string): Promise<T> {
  const contentType = res.headers.get('content-type') || ''
  if (!contentType.includes('application/json')) {
    throw new Error(`Failed to fetch ${label}: expected JSON, got ${contentType || 'unknown content type'}`)
  }
  return res.json()
}
