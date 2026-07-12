import assert from 'node:assert/strict'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const compiledModule = path.join('/tmp', 'session-insight-sidebarRows.mjs')
const { buildSidebarRows, formatRelativeTime } = await import(pathToFileURL(compiledModule).href)

const now = Date.parse('2026-07-12T12:00:00Z')
assert.equal(formatRelativeTime('2026-07-12T11:59:31Z', now), '刚刚')
assert.equal(formatRelativeTime('2026-07-12T11:42:00Z', now), '18分钟前')
assert.equal(formatRelativeTime('2026-07-12T09:00:00Z', now), '3小时前')
assert.equal(formatRelativeTime('2026-07-07T12:00:00Z', now), '5天前')
assert.equal(formatRelativeTime('2026-05-12T12:00:00Z', now), '2个月前')
assert.equal(formatRelativeTime('2024-07-12T12:00:00Z', now), '2年前')
assert.equal(formatRelativeTime('not-a-date', now), 'not-a-date')

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
