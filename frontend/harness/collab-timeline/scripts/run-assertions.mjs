/**
 * Structural browser assertions for the production CollaborationTimeline
 * component. Assert-first (no pixel review required): alignment, bounded
 * mounting, tooltip on hover and focus, keyboard model, selection callbacks,
 * causal-path edges, collapse, localized labels, reduced motion, themes, and
 * 30/200-lane datasets.
 *
 * Usage:
 *   node frontend/harness/collab-timeline/scripts/run-assertions.mjs
 *
 * Exit code 0 = all assertions pass. Not part of the `npm test` aggregator.
 */

import { chromium } from 'playwright'
import { buildHarness, serveHarness } from './lib.mjs'

const failures = []
let checks = 0

function assert(cond, label) {
  checks++
  if (!cond) failures.push(label)
  console.log(`  [${cond ? 'PASS' : 'FAIL'}] ${label}`)
}

async function newPage(browser, url, dataset, theme, lang) {
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
  const consoleErrors = []
  const failedRequests = []
  page.on('console', (msg) => {
    if (msg.type() === 'error') consoleErrors.push(msg.text())
  })
  page.on('pageerror', (err) => consoleErrors.push(String(err)))
  page.on('requestfailed', (req) => failedRequests.push(req.url()))
  page.on('response', (res) => {
    if (res.status() >= 400) failedRequests.push(`${res.status()} ${res.url()}`)
  })
  await page.goto(`${url}?dataset=${dataset}&theme=${theme}&lang=${lang}`)
  await page.waitForFunction(() => window.__collab?.ready)
  return { page, consoleErrors, failedRequests }
}

async function runCombo(browser, url, dataset, theme) {
  console.log(`\n== production / ${dataset} / ${theme} / en ==`)
  const { page, consoleErrors, failedRequests } = await newPage(browser, url, dataset, theme, 'en')

  // Mounted label rows bounded by the viewport + overscan.
  const bounded = await page.evaluate(() => {
    const { stats, labelRows } = window.__collab.counts()
    const scroll = document.querySelector('.ct-scroll')
    const viewportRows = Math.ceil(scroll.clientHeight / window.__collab.rowHeight)
    const bound = viewportRows + 2 * window.__collab.overscan + 1
    return { stats, labelRows, viewportRows, bound }
  })
  assert(bounded.labelRows > 0, `label rows mounted (${bounded.labelRows})`)
  assert(
    bounded.labelRows <= bounded.bound,
    `mounted label rows ${bounded.labelRows} <= viewport ${bounded.viewportRows} + overscan bound ${bounded.bound}`,
  )
  assert(
    bounded.stats.visibleRows === bounded.labelRows,
    `graphics rows (${bounded.stats.visibleRows}) match label rows (${bounded.labelRows})`,
  )

  // Label rows: non-zero, non-overlapping, 28px pitch, aligned with SVG lanes.
  const boxes = await page.evaluate(() => {
    const rows = [...document.querySelectorAll('.ct-label')]
    return rows.map((el) => {
      const r = el.getBoundingClientRect()
      return { row: Number(el.dataset.row), top: r.top, height: r.height, width: r.width }
    })
  })
  assert(boxes.every((b) => b.height === 28 && b.width > 0), 'all label rows 28px high, non-zero width')
  let overlap = false
  for (let i = 1; i < boxes.length; i++) {
    if (boxes[i].top < boxes[i - 1].top + boxes[i - 1].height - 1) overlap = true
  }
  assert(!overlap, 'label rows do not overlap')
  const aligned = boxes.every((b, i) => i === 0 || Math.abs(b.top - boxes[i - 1].top - 28) < 1.5)
  assert(aligned, 'label rows on fixed 28px pitch')
  const svgAligned = await page.evaluate(() => {
    const svg = document.querySelector('svg.ct-svg')
    const svgTop = svg.getBoundingClientRect().top
    const rows = [...document.querySelectorAll('.ct-label')]
    // SVG y coordinates are absolute row coordinates sharing the grid origin.
    return rows.every((el) => {
      const row = Number(el.dataset.row)
      return Math.abs(el.getBoundingClientRect().top - (svgTop + row * 28)) < 1.5
    })
  })
  assert(svgAligned, 'DOM label and SVG lane coordinates aligned')

  // Hit regions: at least 12px effective height (full 28px rows here).
  const hitHeight = await page.evaluate(() => {
    const hit = document.querySelector('.ct-hit-region')
    return hit ? hit.getBoundingClientRect().height : 0
  })
  assert(hitHeight >= 12, `hit region effective height ${hitHeight}px >= 12px`)

  // Hover: tooltip opens with localized status/duration content.
  const point = await page.evaluate(() => window.__collab.laneClientPoint(2))
  await page.mouse.move(point.x, point.y)
  await page.waitForTimeout(80)
  const hover = await page.evaluate(() => {
    const tip = document.querySelector('.ct-tooltip')
    return { visible: Boolean(tip), text: tip?.textContent ?? '' }
  })
  assert(hover.visible, 'tooltip visible on lane hover')
  assert(hover.text.includes('Status') && hover.text.includes('Duration'), `tooltip has status + duration (${hover.text.slice(0, 60)}…)`)

  // Click selects: aria-selected row + onSelect callback, no navigation side effect.
  await page.mouse.click(point.x, point.y)
  const sel = await page.evaluate(() => {
    const el = document.querySelector('.ct-label[aria-selected="true"]')
    return { id: el?.dataset.invocation ?? null, calls: window.__collab.calls.select }
  })
  assert(sel.id !== null, `click selects a lane (${sel.id})`)
  assert(sel.calls.length > 0 && sel.calls[sel.calls.length - 1] === sel.id, 'onSelect callback fired with the lane id')
  // Selection highlights the causal path (result edges render for the path).
  const pathEdges = await page.evaluate(
    () => document.querySelectorAll('.ct-edge-result').length,
  )
  assert(pathEdges > 0, 'result edges render on the selected causal path')

  // Action bar: callbacks fire for wired actions.
  const actions = await page.evaluate(async () => {
    const out = {}
    const btns = [...document.querySelectorAll('.ct-actions .ct-action')]
    out.count = btns.length
    out.enabled = btns.filter((b) => !b.disabled).length
    for (const b of btns) {
      if (!b.disabled) {
        b.click()
        break
      }
    }
    return out
  })
  assert(actions.count === 2, 'two explicit actions rendered (jump to launch / result)')
  assert(actions.enabled >= 1, 'at least one action enabled for a typical selected lane')
  const callbackFired = await page.evaluate(
    () =>
      window.__collab.calls.jumpLaunch.length +
        window.__collab.calls.jumpResult.length >
      0,
  )
  assert(callbackFired, 'an enabled action fired its explicit callback')

  // Keyboard: roving tabindex, ArrowDown moves focus, Enter selects, tooltip on focus.
  await page.evaluate(() => {
    const first = document.querySelector('.ct-label[tabindex="0"]')
    first?.focus()
  })
  await page.keyboard.press('ArrowDown')
  const focusMoved = await page.evaluate(() => {
    const active = document.activeElement
    return active?.classList?.contains('ct-label') && active.getAttribute('tabindex') === '0'
  })
  assert(focusMoved, 'ArrowDown moves focus with roving tabindex')
  const focusTooltip = await page.evaluate(() => Boolean(document.querySelector('.ct-tooltip')))
  assert(focusTooltip, 'tooltip visible on keyboard focus')
  await page.keyboard.press('Enter')
  const kbSelected = await page.evaluate(
    () => document.activeElement?.getAttribute('aria-selected') === 'true',
  )
  assert(kbSelected, 'Enter selects the focused lane')

  // Status markers: shape per state (never color-only), collected while
  // scrolling so off-viewport states are covered.
  const shapes = await page.evaluate(async () => {
    const found = new Set()
    const scroll = document.querySelector('.ct-scroll')
    const frame = () => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)))
    for (let y = 0; y <= scroll.scrollHeight - scroll.clientHeight; y += scroll.clientHeight) {
      scroll.scrollTop = y
      scroll.dispatchEvent(new Event('scroll'))
      await frame()
      for (const el of document.querySelectorAll('.ct-marker')) found.add(el.getAttribute('data-shape'))
    }
    scroll.scrollTop = 0
    scroll.dispatchEvent(new Event('scroll'))
    await frame()
    return [...found]
  })
  assert(shapes.length > 0, `status markers rendered (${shapes.length})`)
  assert(shapes.length >= 4, `distinct marker shapes for distinct states (${shapes.join(',')})`)

  // Mounted primitive bound.
  const mountedCount = await page.evaluate(() => window.__collab.counts().mounted)
  assert(mountedCount > 0, `mounted SVG primitives > 0 (${mountedCount})`)
  assert(mountedCount < 5000, `mounted SVG primitives bounded (${mountedCount} < 5000)`)

  // Collapse: clicking the branch toggle hides descendant rows (DOM row count).
  const beforeCollapse = await page.evaluate(() => document.querySelectorAll('.ct-label').length)
  const collapsedId = await page.evaluate(() => window.__collab.collapseFirstBranch())
  const afterCollapse = await page.evaluate(() => document.querySelectorAll('.ct-label').length)
  const ariaCollapsed = await page.evaluate(
    (id) => document.querySelector(`.ct-label[data-invocation="${id}"]`)?.getAttribute('aria-expanded'),
    collapsedId,
  )
  assert(collapsedId !== null, `branch toggle found (${collapsedId})`)
  assert(afterCollapse < beforeCollapse, `collapse hides descendants (${beforeCollapse} -> ${afterCollapse} mounted rows)`)
  assert(ariaCollapsed === 'false', 'aria-expanded reflects the collapsed branch')
  // Re-expand so pan/zoom below exercises the full dataset.
  await page.evaluate(() => window.__collab.collapseFirstBranch())

  // Pan and ctrl+wheel zoom repaint.
  const beforePan = await page.evaluate(() => window.__collab.renderCount())
  await page.evaluate(() => window.__collab.panStep(60))
  const afterPan = await page.evaluate(() => window.__collab.renderCount())
  assert(afterPan > beforePan, `drag-pan repaints (${beforePan} -> ${afterPan})`)
  const beforeZoom = await page.evaluate(() => window.__collab.renderCount())
  await page.evaluate(() => window.__collab.zoomStep(0.8))
  const afterZoom = await page.evaluate(() => window.__collab.renderCount())
  assert(afterZoom > beforeZoom, `ctrl+wheel zoom repaints (${beforeZoom} -> ${afterZoom})`)

  // Reduced motion: the running pulse is disabled.
  await page.emulateMedia({ reducedMotion: 'reduce' })
  const pulse = await page.evaluate(() => {
    const el = document.querySelector('.ct-st-running')
    return el ? getComputedStyle(el).animationName : 'none'
  })
  assert(pulse === 'none', `prefers-reduced-motion disables pulse (animation-name: ${pulse})`)
  await page.emulateMedia({ reducedMotion: 'no-preference' })

  // Chinese: fixed row height holds; localized chrome and status text.
  await page.evaluate(() => window.__collab.setLang('zh'))
  await page.waitForFunction(() => window.__collab?.ready)
  const zh = await page.evaluate(() => {
    const rows = [...document.querySelectorAll('.ct-label')]
    const heightsOk = rows.length > 0 && rows.every((el) => el.getBoundingClientRect().height === 28)
    const treeLabel = document.querySelector('.ct-labels')?.getAttribute('aria-label') ?? ''
    const zoomTitle = document.querySelector('.ct-btn')?.getAttribute('title') ?? ''
    return { heightsOk, treeLabel, zoomTitle }
  })
  assert(zh.heightsOk, 'Chinese labels keep fixed 28px row height')
  assert(zh.treeLabel === 'Agent 泳道', `tree aria-label localized (${zh.treeLabel})`)
  assert(zh.zoomTitle === '缩小', `zoom control localized (${zh.zoomTitle})`)
  const zhTooltip = await page.evaluate(async () => {
    const pt = window.__collab.laneClientPoint(2)
    const hit = document.elementFromPoint(pt.x, pt.y)
    hit?.dispatchEvent(new PointerEvent('pointermove', { bubbles: true, clientX: pt.x, clientY: pt.y }))
    await new Promise((r) => setTimeout(r, 80))
    return document.querySelector('.ct-tooltip')?.textContent ?? ''
  })
  assert(zhTooltip.includes('状态') && zhTooltip.includes('时长'), `tooltip localized (${zhTooltip.slice(0, 50)}…)`)
  await page.evaluate(() => window.__collab.setLang('en'))

  assert(consoleErrors.length === 0, `no console errors (${consoleErrors.join(' | ') || 'none'})`)
  assert(failedRequests.length === 0, `no failed requests (${failedRequests.join(' | ') || 'none'})`)

  await page.close()
}

async function main() {
  buildHarness()
  const { server, url } = await serveHarness()
  const browser = await chromium.launch()
  try {
    await runCombo(browser, url, 'typical', 'dark')
    await runCombo(browser, url, 'stress', 'light')
  } finally {
    await browser.close()
    server.close()
  }
  console.log(`\n${checks - failures.length}/${checks} assertions passed`)
  if (failures.length > 0) {
    console.error(`FAILURES:\n- ${failures.join('\n- ')}`)
    process.exit(1)
  }
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
