import assert from 'node:assert/strict'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const compiledModule = path.join('/tmp', 'session-insight-sidebarRows.mjs')
const { buildSidebarRows } = await import(pathToFileURL(compiledModule).href)

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
