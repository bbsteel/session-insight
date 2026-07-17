import assert from 'node:assert/strict'
import { applyBookmarkChange, filterBookmarks, removeBookmarkFromList } from '/tmp/session-insight-bookmark-state/bookmarkState.js'

const sessions = [
  { id: 'newer', agent_type: 'claude', updated_at: '2026-07-06T10:00:00Z', bookmarked: false },
  { id: 'older', agent_type: 'claude', updated_at: '2026-07-05T10:00:00Z', bookmarked: false },
]

const changed = applyBookmarkChange(sessions, { agentType: 'claude', sessionId: 'older', bookmarked: true })
assert.equal(changed[1].bookmarked, true)
assert.deepEqual(changed.map(s => s.id), ['newer', 'older'], 'bookmarking must not reorder sessions')
assert.equal(sessions[1].bookmarked, false, 'state helper must not mutate input')

const withNote = applyBookmarkChange(changed, {
  agentType: 'claude',
  sessionId: 'older',
  bookmarked: true,
  bookmarkNote: 'handoff context',
})
assert.equal(withNote[1].bookmark_note, 'handoff context')

const cleared = applyBookmarkChange(withNote, {
  agentType: 'claude',
  sessionId: 'older',
  bookmarked: false,
})
assert.equal(cleared[1].bookmarked, false)
assert.equal(cleared[1].bookmark_note, undefined, 'unbookmark must clear note')

const untouched = applyBookmarkChange(changed, { agentType: 'codex', sessionId: 'older', bookmarked: false })
assert.equal(untouched[1].bookmarked, true, 'agent mismatch must not change same session id')

const bookmarks = [
  { id: 'keep', agent_type: 'claude', bookmarked: true },
  { id: 'drop', agent_type: 'claude', bookmarked: true },
]
assert.deepEqual(
  removeBookmarkFromList(bookmarks, { agentType: 'claude', sessionId: 'drop', bookmarked: false }).map(s => s.id),
  ['keep'],
)

const filterable = [
  { id: 'claude-a', agent_type: 'claude', project: 'alpha', bookmarked: true },
  { id: 'codex-a', agent_type: 'codex', project: 'alpha', bookmarked: true },
  { id: 'claude-b', agent_type: 'claude', project: 'beta', bookmarked: true },
]
assert.deepEqual(
  filterBookmarks(filterable, { agentType: 'claude', project: '' }).map(s => s.id),
  ['claude-a', 'claude-b'],
)
assert.deepEqual(
  filterBookmarks(filterable, { agentType: '', project: 'alpha' }).map(s => s.id),
  ['claude-a', 'codex-a'],
)
assert.deepEqual(
  filterBookmarks(filterable, { agentType: 'claude', project: 'beta' }).map(s => s.id),
  ['claude-b'],
)

console.log('bookmark state tests passed')
