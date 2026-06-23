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
