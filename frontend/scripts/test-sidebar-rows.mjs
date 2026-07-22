import assert from 'node:assert/strict'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const compiledModule = path.join('/tmp', 'session-insight-sidebarRows.mjs')
const { buildSidebarRows, formatRelativeTime, isSessionLive } = await import(pathToFileURL(compiledModule).href)

const now = Date.parse('2026-07-12T12:00:00Z')
assert.equal(formatRelativeTime('2026-07-12T11:59:31Z', now), '此刻')
assert.equal(formatRelativeTime('2026-07-12T11:42:00Z', now), '18分钟前')
assert.equal(formatRelativeTime('2026-07-12T09:00:00Z', now), '3小时前')
assert.equal(formatRelativeTime('2026-07-07T12:00:00Z', now), '5天前')
assert.equal(formatRelativeTime('2026-05-12T12:00:00Z', now), '2个月前')
assert.equal(formatRelativeTime('2024-07-12T12:00:00Z', now), '2年前')
assert.equal(formatRelativeTime('2026-07-12T11:42:00Z', now, 'en'), '18 minutes ago')
assert.equal(formatRelativeTime('not-a-date', now), 'not-a-date')

// isSessionLive：服务端快照 + 客户端衰减，只熄灭不点亮
assert.equal(isSessionLive({ is_live: true, updated_at: '2026-07-12T11:58:00Z' }, now), true)
// updated_at 超过 5 分钟窗口 → 徽标熄灭，即使快照仍是 true
assert.equal(isSessionLive({ is_live: true, updated_at: '2026-07-12T11:54:00Z' }, now), false)
// 服务端说不活跃时，客户端绝不点亮
assert.equal(isSessionLive({ is_live: false, updated_at: '2026-07-12T11:59:59Z' }, now), false)
// updated_at 不可解析 → 视为不活跃
assert.equal(isSessionLive({ is_live: true, updated_at: 'not-a-date' }, now), false)

const sessions = [
  { id: 'c1', agent_type: 'codex' },
  { id: 'c2', agent_type: 'codex' },
  { id: 'a1', agent_type: 'claude' },
]

const flat = buildSidebarRows(sessions, false, new Set())
assert.deepEqual(flat.map(row => row.type), ['session', 'session', 'session'])

const grouped = buildSidebarRows(sessions, true, new Set())
assert.deepEqual(
  grouped.map(row => row.type === 'group' ? `group:${row.label}` : row.session.id),
  ['group:Claude Code', 'a1', 'group:Codex', 'c1', 'c2'],
)

const collapsed = buildSidebarRows(sessions, true, new Set(['Codex']))
assert.deepEqual(
  collapsed.map(row => row.type === 'group' ? `group:${row.label}` : row.session.id),
  ['group:Claude Code', 'a1', 'group:Codex'],
)
