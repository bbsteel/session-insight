// Unit coverage for computeAnchoredPanelRect (frontend/src/components/useAnchoredPanelRect.ts).
// The compiled module is loaded from /tmp/session-insight-anchored-panel-rect
// (see the test:anchored-panel npm script). Run via `npm run test:anchored-panel`.

import assert from 'node:assert/strict'

const { computeAnchoredPanelRect } = await import('/tmp/session-insight-anchored-panel-rect/anchoredPanelRect.js')

function setViewport(width, height) {
  globalThis.window = { innerWidth: width, innerHeight: height }
}

function anchor(rect, sidebarRight) {
  return {
    getBoundingClientRect: () => rect,
    closest: () => (sidebarRight == null
      ? null
      : { getBoundingClientRect: () => ({ right: sidebarRight }) }),
  }
}

const MAX_WIDTH = 660

// 1. Regular desktop window: panel sits beside the anchor at full width.
setViewport(1440, 900)
{
  const rect = computeAnchoredPanelRect(anchor({ right: 244, top: 120 }, 260), MAX_WIDTH)
  assert.equal(rect.left, 268)
  assert.equal(rect.width, 660)
  assert.equal(rect.top, 120)
  assert.equal(rect.maxHeight, 900 - 120 - 16)
  assert.ok(rect.left > 260, 'panel starts after the sidebar edge and its resize handle')
}

// 1b. Anchors outside the sidebar fall back to the anchor edge.
setViewport(1440, 900)
{
  const rect = computeAnchoredPanelRect(anchor({ right: 260, top: 120 }), MAX_WIDTH)
  assert.equal(rect.left, 268)
  assert.equal(rect.width, 660)
}

// 2. Narrow window (768 px) with the default sidebar: width is clamped to the
// space after the anchor; the panel never slides back over the sidebar.
setViewport(768, 900)
{
  const rect = computeAnchoredPanelRect(anchor({ right: 244, top: 120 }, 260), MAX_WIDTH)
  assert.equal(rect.left, 268)
  assert.equal(rect.width, 768 - 268 - 8)
  assert.ok(rect.left >= 260 + 8, 'panel must start after the anchor')
  assert.ok(rect.left + rect.width <= 768 - 8, 'panel must stay inside the viewport')
}

// 3. Wide sidebar, narrow window: the panel shrinks instead of overlapping.
setViewport(700, 900)
{
  const rect = computeAnchoredPanelRect(anchor({ right: 640, top: 100 }), MAX_WIDTH)
  assert.equal(rect.left, 648)
  assert.equal(rect.width, 44)
  assert.ok(rect.left + rect.width <= 700 - 8)
}

// 4. No usable space beside the anchor: no rect, no zero-width overlay.
setViewport(400, 900)
{
  const rect = computeAnchoredPanelRect(anchor({ right: 399, top: 100 }), MAX_WIDTH)
  assert.equal(rect, null)
}

// 5. Short viewport with a low anchor: the panel shifts up to keep its usable
// height, and never crosses the bottom edge.
setViewport(1440, 300)
{
  const rect = computeAnchoredPanelRect(anchor({ right: 260, top: 200 }), MAX_WIDTH)
  assert.equal(rect.top, 300 - 16 - 240)
  assert.equal(rect.maxHeight, 240)
  assert.ok(rect.top + rect.maxHeight <= 300 - 16, 'panel must not overflow the bottom edge')
}

// 6. Extremely short viewport: height is capped by the real remaining space
// (the content scrolls inside the panel instead of overflowing).
setViewport(1440, 200)
{
  const rect = computeAnchoredPanelRect(anchor({ right: 260, top: 150 }), MAX_WIDTH)
  assert.equal(rect.top, 8)
  assert.equal(rect.maxHeight, 200 - 8 - 16)
  assert.ok(rect.maxHeight < 240, 'no overflowing minimum height')
  assert.ok(rect.top + rect.maxHeight <= 200 - 16)
}

// 7. Anchor above the top edge is clamped to the gutter.
setViewport(1440, 900)
{
  const rect = computeAnchoredPanelRect(anchor({ right: 260, top: -50 }), MAX_WIDTH)
  assert.equal(rect.top, 8)
}

// 8. Missing anchor yields no rect.
setViewport(1440, 900)
assert.equal(computeAnchoredPanelRect(null, MAX_WIDTH), null)

console.log('test-anchored-panel-rect: all assertions passed')
