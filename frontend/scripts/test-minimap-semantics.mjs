import assert from 'node:assert/strict'
import { pathToFileURL } from 'node:url'
import path from 'node:path'

const compiledModule = path.join('/tmp', 'session-insight-minimap-semantics', 'minimapSemantics.js')

const {
  getMiniMapEventKind,
  getMiniMapTurnPositionPercent,
  getTokenPressureTone,
} = await import(pathToFileURL(compiledModule).href)

function turn(overrides = {}) {
  return {
    turn_index: 0,
    user_message: '',
    assistant_message: '',
    token_usage: {
      prompt_tokens: 0,
      completion_tokens: 0,
      cache_read_tokens: 0,
      cache_write_tokens: 0,
      premium_requests: 0,
    },
    tool_call_count: 0,
    error_count: 0,
    duration_ms: 0,
    ...overrides,
  }
}

assert.equal(getTokenPressureTone(0), 'empty')
assert.equal(getTokenPressureTone(0.2), 'low')
assert.equal(getTokenPressureTone(0.55), 'medium')
assert.equal(getTokenPressureTone(0.85), 'high')
assert.equal(getTokenPressureTone(1), 'critical')

assert.equal(getMiniMapEventKind(turn({ user_message: 'hello' })), 'user')
assert.equal(getMiniMapEventKind(turn({ anomalies: ['compaction'] })), 'compaction')
assert.equal(getMiniMapEventKind(turn({ error_count: 1, user_message: 'hello' })), 'anomaly')
assert.equal(getMiniMapEventKind(turn({ anomalies: ['compression'], user_message: 'hello' })), 'compaction')
assert.equal(getMiniMapEventKind(turn()), null)

assert.equal(getMiniMapTurnPositionPercent(0, 1), 0)
assert.equal(getMiniMapTurnPositionPercent(0, 3), 0)
assert.equal(getMiniMapTurnPositionPercent(1, 3), 50)
assert.equal(getMiniMapTurnPositionPercent(2, 3), 100)
