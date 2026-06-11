import type { SessionDetail, SessionSummary } from './types'

export async function fetchSessions(agent?: string): Promise<SessionSummary[]> {
  const params = new URLSearchParams()
  if (agent) params.set('agent', agent)

  const res = await fetch(`/api/sessions?${params}`)
  if (!res.ok) throw new Error(`Failed to fetch sessions: ${res.status}`)
  return res.json()
}

export async function fetchSession(id: string): Promise<SessionDetail> {
  const res = await fetch(`/api/sessions/${id}`)
  if (!res.ok) throw new Error(`Failed to fetch session: ${res.status}`)
  return res.json()
}
