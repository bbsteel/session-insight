import assert from 'node:assert/strict'
import { pathToFileURL } from 'node:url'
import path from 'node:path'

const compiledModule = path.join('/tmp', 'session-insight-minimap-semantics', 'minimapSemantics.js')

const semantics = await import(pathToFileURL(compiledModule).href)
const { getMiniMapTurnPositionPercent, getTokenPressureTone } = semantics

// v0.6.1: event-marker mapping was removed from the MiniMap (precise event
// navigation lives in the key-event outline). The pure module must no longer
// export marker logic.
assert.equal('getMiniMapEventKind' in semantics, false, 'event-marker mapping removed')
assert.equal('hasCompaction' in semantics, false, 'compaction marker helper removed')

assert.equal(getTokenPressureTone(0), 'empty')
assert.equal(getTokenPressureTone(0.2), 'low')
assert.equal(getTokenPressureTone(0.55), 'medium')
assert.equal(getTokenPressureTone(0.85), 'high')
assert.equal(getTokenPressureTone(1), 'critical')

assert.equal(getMiniMapTurnPositionPercent(0, 1), 0)
assert.equal(getMiniMapTurnPositionPercent(0, 3), 0)
assert.equal(getMiniMapTurnPositionPercent(1, 3), 50)
assert.equal(getMiniMapTurnPositionPercent(2, 3), 100)
