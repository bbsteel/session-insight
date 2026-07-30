/**
 * Durable logic tests: adaptive time-axis ticks
 * (src/collaboration/timeAxis.ts).
 */

import assert from 'node:assert/strict'
import { pathToFileURL } from 'node:url'

const core = '/tmp/session-insight-collaboration-axis/src/collaboration'
const { axisStepMs, axisTicks, AXIS_STEPS_MS } = await import(pathToFileURL(`${core}/timeAxis.js`).href)

const S = 1_000
const M = 60 * S
const H = 60 * M
const D = 24 * H

// --- Step selection stays in the 4-6 tick band --------------------------------

for (const span of [30 * S, 90 * S, 5 * M, 42 * M, 2 * H, 11 * H, 3 * D, 40 * D, 400 * D, 5 * 365 * D]) {
  const step = axisStepMs(span, 5)
  assert.ok(span / step <= 5, `span ${span}: step ${step} yields ${span / step} intervals > 5`)
  const idx = AXIS_STEPS_MS.indexOf(step)
  if (idx > 0) {
    const smaller = AXIS_STEPS_MS[idx - 1]
    assert.ok(span / smaller > 5, `span ${span}: smaller step ${smaller} would also fit — step not minimal`)
  }
}

// Degenerate spans never throw and still pick the smallest step.
assert.equal(axisStepMs(0), AXIS_STEPS_MS[0])
assert.equal(axisStepMs(-100), AXIS_STEPS_MS[0])

// --- Tick emission ---------------------------------------------------------------

{
  // Exact multiples inside the visible window only.
  const ticks = axisTicks(7 * S, 26 * S, 5)
  assert.deepEqual(ticks, [10 * S, 15 * S, 20 * S, 25 * S])
}
{
  // Window aligned on step boundaries includes both ends.
  const ticks = axisTicks(0, 60 * S, 5)
  assert.ok(ticks[0] === 0 && ticks[ticks.length - 1] === 60 * S)
  assert.ok(ticks.length >= 4 && ticks.length <= 6, `4-6 ticks, got ${ticks.length}`)
}
{
  // Realistic session spans: always 4-6 ticks, all inside the window.
  for (const span of [90 * S, 42 * M, 11 * H, 3 * D, 400 * D]) {
    const start = 1_784_000_000_000
    const ticks = axisTicks(start, start + span, 5)
    assert.ok(ticks.length >= 3 && ticks.length <= 6, `span ${span}: ${ticks.length} ticks`)
    for (const t of ticks) assert.ok(t >= start && t <= start + span)
    for (let i = 1; i < ticks.length; i++) assert.ok(ticks[i] > ticks[i - 1], 'strictly increasing')
  }
}
{
  // Invalid / empty ranges produce no ticks.
  assert.deepEqual(axisTicks(100, 100), [])
  assert.deepEqual(axisTicks(200, 100), [])
}

console.log('collaboration time axis tests passed')
