import type { SessionSummary } from './types'

export type SidebarRow =
  | { type: 'group'; label: string; count: number }
  | { type: 'session'; session: SessionSummary }

export function getAgentLabel(agent: string): string {
  if (!agent) return 'Unknown'
  if (agent.toLowerCase().includes('copilot')) return 'Copilot'
  if (agent.toLowerCase().includes('claude')) return 'Claude Code'
  if (agent.toLowerCase().includes('codex')) return 'Codex'
  if (agent.toLowerCase().includes('opencode')) return 'OpenCode'
  return agent
}

export function formatRelativeTime(dateStr: string, now = Date.now()): string {
  const timestamp = new Date(dateStr).getTime()
  if (!Number.isFinite(timestamp)) return dateStr

  const elapsedMinutes = Math.max(0, Math.floor((now - timestamp) / 60_000))
  if (elapsedMinutes < 1) return '刚刚'
  if (elapsedMinutes < 60) return `${elapsedMinutes}分钟前`

  const elapsedHours = Math.floor(elapsedMinutes / 60)
  if (elapsedHours < 24) return `${elapsedHours}小时前`

  const elapsedDays = Math.floor(elapsedHours / 24)
  if (elapsedDays < 30) return `${elapsedDays}天前`

  const elapsedMonths = Math.floor(elapsedDays / 30)
  if (elapsedMonths < 12) return `${elapsedMonths}个月前`

  return `${Math.floor(elapsedDays / 365)}年前`
}

export function buildSidebarRows(
  sessions: SessionSummary[],
  grouped: boolean,
  collapsedGroups: Set<string>,
): SidebarRow[] {
  if (!grouped) {
    return sessions.map(session => ({ type: 'session', session }))
  }

  const groups = new Map<string, SessionSummary[]>()
  for (const session of sessions) {
    const label = getAgentLabel(session.agent_type)
    const group = groups.get(label) ?? []
    group.push(session)
    groups.set(label, group)
  }

  const rows: SidebarRow[] = []
  for (const [label, group] of [...groups.entries()].sort(([left], [right]) => left.localeCompare(right))) {
    rows.push({ type: 'group', label, count: group.length })
    if (!collapsedGroups.has(label)) {
      rows.push(...group.map(session => ({ type: 'session' as const, session })))
    }
  }
  return rows
}
