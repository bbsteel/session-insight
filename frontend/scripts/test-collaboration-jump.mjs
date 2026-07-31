/**
 * Durable logic tests: collaboration anchor → replay position resolution
 * (src/collaboration/jumpTargets.ts).
 *
 * The jump-to-launch/result actions resolve frozen source anchors against the
 * existing positions API payload: exact event_id first, then tool_call_id,
 * turn_index, and nearest timestamp. Result actions use the result coordinates
 * attached to the tool position. Unresolvable anchors return null.
 */

import assert from 'node:assert/strict'
import { pathToFileURL } from 'node:url'

const core = '/tmp/session-insight-collaboration-jump/src/collaboration'
const { resolveAnchorJump } = await import(pathToFileURL(`${core}/jumpTargets.js`).href)

const positions = [
  { kind: 'user', position_key: 'user:0:10', turn_index: 0, line_start: 10, payload: { logical_start: 5, ts_ms: 1000, text: 'hi' } },
  {
    kind: 'tool',
    position_key: 'tool:0:33',
    turn_index: 0,
    line_start: 33,
    payload: {
      logical_start: 17,
      tool_call_id: 'tool_aaa',
      event_id: 'call-tool_aaa',
      ts_ms: 2000,
      result_event_id: 'result-tool_aaa',
      result_line_start: 55,
      result_logical_start: 29,
      result_ts_ms: 8000,
    },
  },
  { kind: 'tool', position_key: 'tool:3:90', turn_index: 3, line_start: 90, payload: { tool_call_id: 'tool_bbb', ts_ms: 9000 } },
  { kind: 'turn', position_key: 'turn:4:120', turn_index: 4, line_start: 120, payload: {} },
]

const anchor = (fields) => ({
  agent_type: 'chrys',
  session_id: 's1',
  precision: { state: 'exact' },
  ...fields,
})

// Null / empty anchors never resolve.
assert.equal(resolveAnchorJump(null, positions), null)
assert.equal(resolveAnchorJump(anchor({}), positions), null)

// Exact tool_call_id wins over every other signal.
assert.deepEqual(
  resolveAnchorJump(anchor({ tool_call_id: 'tool_aaa', turn_index: 4, timestamp: '1970-01-01T00:00:09Z' }), positions),
  { lineStart: 33, logicalStart: 17 },
)
assert.deepEqual(resolveAnchorJump(anchor({ tool_call_id: 'tool_bbb' }), positions), { lineStart: 90, logicalStart: undefined })

// Result navigation must distinguish the result from the invocation that
// shares its tool_call_id.
assert.deepEqual(
  resolveAnchorJump(anchor({ event_id: 'call-tool_aaa', tool_call_id: 'tool_aaa' }), positions, 'launch'),
  { lineStart: 33, logicalStart: 17 },
)
assert.deepEqual(
  resolveAnchorJump(anchor({ event_id: 'result-tool_aaa', tool_call_id: 'tool_aaa' }), positions, 'result'),
  { lineStart: 55, logicalStart: 29 },
)
assert.deepEqual(
  resolveAnchorJump(anchor({ tool_call_id: 'tool_aaa' }), positions, 'result'),
  { lineStart: 55, logicalStart: 29 },
)
assert.equal(resolveAnchorJump(anchor({ tool_call_id: 'tool_bbb' }), positions, 'result'), null)

// Unknown tool_call_id falls through to the next signal, not to null.
assert.deepEqual(resolveAnchorJump(anchor({ tool_call_id: 'tool_gone', turn_index: 0 }), positions), { lineStart: 10, logicalStart: 5 })

// turn_index resolves the turn/user position for that turn.
assert.deepEqual(resolveAnchorJump(anchor({ turn_index: 4 }), positions), { lineStart: 120, logicalStart: undefined })

// Timestamp-only anchors land on the nearest recorded position timestamp.
assert.deepEqual(resolveAnchorJump(anchor({ timestamp: '1970-01-01T00:00:08.500Z' }), positions), { lineStart: 90, logicalStart: undefined })
assert.deepEqual(resolveAnchorJump(anchor({ timestamp: '1970-01-01T00:00:01.200Z' }), positions), { lineStart: 10, logicalStart: 5 })
assert.deepEqual(
  resolveAnchorJump(anchor({ timestamp: '1970-01-01T00:00:08.100Z' }), positions, 'result'),
  { lineStart: 55, logicalStart: 29 },
)

// Nothing reliable → null (invalid timestamp, no positions, empty payload).
assert.equal(resolveAnchorJump(anchor({ timestamp: 'not-a-date' }), positions), null)
assert.equal(resolveAnchorJump(anchor({ tool_call_id: 'tool_aaa' }), []), null)
assert.equal(resolveAnchorJump(anchor({ turn_index: 99 }), positions), null)

console.log('collaboration jump target tests passed')
