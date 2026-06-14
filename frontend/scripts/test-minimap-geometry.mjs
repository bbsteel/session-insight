import assert from 'node:assert/strict'
import { pathToFileURL } from 'node:url'
import path from 'node:path'

const compiledModule = path.join('/tmp', 'session-insight-minimapGeometry.mjs')

const { getScrollTopFromTrackPosition, getViewportFrame } = await import(pathToFileURL(compiledModule).href)

function approx(actual, expected) {
  assert.ok(Math.abs(actual - expected) < 0.001, `expected ${actual} to be close to ${expected}`)
}

const metrics = {
  scrollTop: 250,
  scrollHeight: 1000,
  clientHeight: 200,
}

const frame = getViewportFrame(metrics, 100)

approx(frame.top, 25)
approx(frame.height, 20)

const scrollTop = getScrollTopFromTrackPosition({
  pointerPosition: 50,
  trackStart: 0,
  trackLength: 100,
  viewportLength: frame.height,
  scrollHeight: metrics.scrollHeight,
  clientHeight: metrics.clientHeight,
  dragOffset: 10,
})

approx(scrollTop, 400)
