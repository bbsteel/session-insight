import assert from 'node:assert/strict'
import { pathToFileURL } from 'node:url'
import path from 'node:path'

const compiledModule = path.join('/tmp', 'session-insight-minimapGeometry.mjs')

const { getScrollBoundaryTop, getScrollTopFromTrackPosition, getViewportFrame } = await import(pathToFileURL(compiledModule).href)

function approx(actual, expected) {
  assert.ok(Math.abs(actual - expected) < 0.001, `expected ${actual} to be close to ${expected}`)
}

const metrics = {
  scrollTop: 250,
  scrollHeight: 1000,
  clientHeight: 200,
}

const frame = getViewportFrame(metrics, 100)

const explicitSmallFrame = getViewportFrame(metrics, 100, 4)

approx(explicitSmallFrame.top, 25)
approx(explicitSmallFrame.height, 20)
approx(frame.top, 16.25)
approx(frame.height, 48)

const scrollTop = getScrollTopFromTrackPosition({
  pointerPosition: 50,
  trackStart: 0,
  trackLength: 100,
  viewportLength: frame.height,
  scrollHeight: metrics.scrollHeight,
  clientHeight: metrics.clientHeight,
  dragOffset: 10,
})

approx(scrollTop, 615.3846153846155)

assert.equal(getScrollBoundaryTop(metrics, 'top'), 0)
assert.equal(getScrollBoundaryTop(metrics, 'bottom'), 800)
assert.equal(getScrollBoundaryTop({ scrollTop: 0, scrollHeight: 120, clientHeight: 200 }, 'bottom'), 0)
