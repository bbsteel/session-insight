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

// 与后端 model.LiveWindow 对齐：最近 5 分钟内有更新才算活跃。
export const LIVE_WINDOW_MS = 5 * 60_000

// is_live 是服务端在响应时刻算好的快照，列表不重拉它就不会变。客户端用
// updated_at 对当前时间做衰减——只熄灭过期徽标、绝不点亮新徽标，这样
// 客户端与服务端的时钟偏差最多让徽标晚灭，不会把闲置会话误标成活跃。
export function isSessionLive(session: Pick<SessionSummary, 'is_live' | 'updated_at'>, now: number): boolean {
  return session.is_live && now - new Date(session.updated_at).getTime() < LIVE_WINDOW_MS
}

export function formatRelativeTime(dateStr: string, now = Date.now(), locale = 'zh-CN'): string {
  const timestamp = new Date(dateStr).getTime()
  if (!Number.isFinite(timestamp)) return dateStr

  const elapsedMinutes = Math.max(0, Math.floor((now - timestamp) / 60_000))
  const relative = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' })
  if (elapsedMinutes < 1) return relative.format(0, 'minute')
  if (elapsedMinutes < 60) return relative.format(-elapsedMinutes, 'minute')
  const hours = Math.floor(elapsedMinutes / 60)
  if (hours < 24) return relative.format(-hours, 'hour')
  const days = Math.floor(hours / 24)
  if (days < 30) return relative.format(-days, 'day')
  const months = Math.floor(days / 30)
  if (months < 12) return relative.format(-months, 'month')
  return relative.format(-Math.floor(days / 365), 'year')
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
