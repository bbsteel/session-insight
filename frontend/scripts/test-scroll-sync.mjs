import assert from 'node:assert/strict'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const compiledModule = path.join('/tmp', 'session-insight-scrollSync.mjs')
const {
  createFrameBatcher,
  getVisibleTurnRange,
  isSameVisibleRange,
} = await import(pathToFileURL(compiledModule).href)

const metrics = {
  scrollTop: 400,
  scrollHeight: 1000,
  clientHeight: 200,
}

assert.deepEqual(getVisibleTurnRange(metrics, 10), { start: 4, end: 5 })
assert.equal(getVisibleTurnRange(metrics, 0), undefined)
assert.equal(isSameVisibleRange({ start: 4, end: 5 }, { start: 4, end: 5 }), true)
assert.equal(isSameVisibleRange({ start: 4, end: 5 }, { start: 5, end: 6 }), false)

const frames = []
const cancelled = []
const received = []
const batcher = createFrameBatcher(
  value => received.push(value),
  callback => {
    frames.push(callback)
    return frames.length
  },
  frame => cancelled.push(frame),
)

batcher.push(1)
batcher.push(2)
batcher.push(3)
assert.equal(frames.length, 1)
assert.deepEqual(received, [])

frames.shift()()
assert.deepEqual(received, [3])

batcher.push(4)
batcher.cancel()
assert.deepEqual(cancelled, [1])
