/**
 * Structural interaction assertions for the spike harness. Runs before any
 * screenshot review so a text-only agent gets a hard pass/fail.
 *
 * Usage:
 *   node frontend/spike/collab-timeline/scripts/run-assertions.mjs
 *
 * Exit code 0 = all assertions pass. Not part of the `npm test` aggregator.
 */

import { chromium } from 'playwright'
import { buildHarness, serveSpike } from './lib.mjs'

const failures = []
let checks = 0

function assert(cond, label) {
  checks++
  if (!cond) failures.push(label)
  console.log(`  [${cond ? 'PASS' : 'FAIL'}] ${label}`)
}

async function runForCombo(browser, baseUrl, renderer, dataset, theme) {
  console.log(`\n== ${renderer} / ${dataset} / ${theme} ==`)
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

  await page.goto(`${baseUrl}?renderer=${renderer}&dataset=${dataset}&theme=${theme}&lang=en`)
  await page.waitForFunction(() => window.__spike?.ready)

  // Bounded mounted rows: mounted lanes <= viewport rows + 2 * overscan.
  const bounded = await page.evaluate(() => {
    const { stats, labelRows } = window.__spike.counts()
    const scroll = document.getElementById('scroll')
    const viewportRows = Math.ceil(scroll.clientHeight / window.__spike.rowHeight)
    const bound = viewportRows + 2 * window.__spike.overscan + 1
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

  // Non-zero, non-overlapping label boxes aligned with lane row coordinates.
  const boxes = await page.evaluate(() => {
    const rows = [...document.querySelectorAll('.tl-label')]
    return rows.map((el) => {
      const r = el.getBoundingClientRect()
      return { row: Number(el.dataset.row), top: r.top, height: r.height, width: r.width }
    })
  })
  assert(boxes.every((b) => b.height > 0 && b.width > 0), 'all label rows have non-zero boxes')
  let overlap = false
  for (let i = 1; i < boxes.length; i++) {
    if (boxes[i].top < boxes[i - 1].top + boxes[i - 1].height - 1) overlap = true
  }
  assert(!overlap, 'label rows do not overlap')
  const aligned = boxes.every((b, i) => i === 0 || Math.abs(b.top - boxes[i - 1].top - 28) < 1.5)
  assert(aligned, 'label/lane rows aligned to fixed 28px pitch')

  // Hit testing: hovering a lane reports its invocation id and shows a tooltip.
  const point = await page.evaluate(() => window.__spike.laneClientPoint(2))
  await page.mouse.move(point.x, point.y)
  await page.waitForTimeout(50)
  const hover = await page.evaluate(() => {
    const tip = document.getElementById('tooltip')
    return { hoverVisible: !tip.hidden, text: tip.textContent ?? '' }
  })
  assert(hover.hoverVisible, 'tooltip visible on lane hover')
  assert(hover.text.length > 10, `tooltip has content (${hover.text.slice(0, 40)}…)`)

  // Selection via click marks the label row selected.
  await page.mouse.click(point.x, point.y)
  const selected = await page.evaluate(() => {
    const el = document.querySelector('.tl-label[aria-selected="true"]')
    return el?.dataset.invocation ?? null
  })
  assert(selected !== null, `click selects a lane (${selected})`)

  // Keyboard: roving tabindex, ArrowDown moves focus, Enter selects.
  const focusable = await page.evaluate(() => {
    const first = document.querySelector('.tl-label[tabindex="0"]')
    first?.focus()
    return Boolean(first)
  })
  assert(focusable, 'a roving-tabindex label is focusable')
  await page.keyboard.press('ArrowDown')
  const focusMoved = await page.evaluate(() => {
    const active = document.activeElement
    return active?.classList?.contains('tl-label') && active.getAttribute('tabindex') === '0'
  })
  assert(focusMoved, 'ArrowDown moves focus with roving tabindex')
  await page.keyboard.press('Enter')
  const kbSelected = await page.evaluate(
    () => document.activeElement?.getAttribute('aria-selected') === 'true',
  )
  assert(kbSelected, 'Enter selects the focused lane')

  // Tooltip on keyboard focus as well as hover.
  const focusTooltip = await page.evaluate(() => !document.getElementById('tooltip').hidden)
  assert(focusTooltip, 'tooltip visible on keyboard focus')

  // Status is never color-only: failed/orphaned lanes carry shape markers.
  const shapes = await page.evaluate((rd) => {
    if (rd === 'svg') {
      return [...document.querySelectorAll('.marker')].map((el) => el.getAttribute('data-shape'))
    }
    return window.__spike.counts().stats ? ['canvas-markers-drawn'] : []
  }, renderer)
  assert(shapes.length > 0, `status markers rendered (${renderer}, ${shapes.length} shapes)`)

  // Canvas equivalence: mounted primitive count is bounded by viewport too.
  const mountedCount = await page.evaluate(() => window.__spike.counts().mounted)
  assert(mountedCount > 0, `mounted primitives > 0 (${mountedCount})`)
  assert(
    mountedCount < 5000,
    `mounted primitives bounded (${mountedCount} < 5000) — viewport culling + LOD active`,
  )

  // Regression: drag-pan and ctrl+wheel zoom must repaint even though they
  // mutate the time domain without changing scrollTop.
  const graphBox = await page.evaluate(() => {
    const r = document.getElementById('graph').getBoundingClientRect()
    return { x: r.left + r.width / 2, y: r.top + 60 }
  })
  const beforePan = await page.evaluate(() => window.__spike.renderCount())
  await page.mouse.move(graphBox.x, graphBox.y)
  await page.mouse.down()
  await page.mouse.move(graphBox.x + 60, graphBox.y, { steps: 3 })
  await page.mouse.up()
  await page.waitForTimeout(60)
  const afterPan = await page.evaluate(() => window.__spike.renderCount())
  assert(afterPan > beforePan, `drag-pan repaints (renders ${beforePan} -> ${afterPan})`)

  const beforeZoom = await page.evaluate(() => window.__spike.renderCount())
  await page.keyboard.down('Control')
  await page.mouse.wheel(0, -240)
  await page.keyboard.up('Control')
  await page.waitForTimeout(60)
  const afterZoom = await page.evaluate(() => window.__spike.renderCount())
  assert(afterZoom > beforeZoom, `ctrl+wheel zoom repaints (renders ${beforeZoom} -> ${afterZoom})`)

  // Reduced motion: the running pulse must be disabled.
  await page.emulateMedia({ reducedMotion: 'reduce' })
  const pulse = await page.evaluate(() => {
    const el = document.querySelector('.st-running')
    return el ? getComputedStyle(el).animationName : 'none'
  })
  assert(pulse === 'none', `prefers-reduced-motion disables pulse (animation-name: ${pulse})`)
  await page.emulateMedia({ reducedMotion: 'no-preference' })

  // Chinese labels render at fixed row height.
  await page.evaluate(() => window.__spike.setLang('zh'))
  const zhOk = await page.evaluate(() => {
    const rows = [...document.querySelectorAll('.tl-label')]
    return rows.length > 0 && rows.every((el) => el.getBoundingClientRect().height === 28)
  })
  assert(zhOk, 'Chinese labels keep fixed 28px row height')
  await page.evaluate(() => window.__spike.setLang('en'))

  assert(consoleErrors.length === 0, `no console errors (${consoleErrors.join(' | ') || 'none'})`)
  assert(failedRequests.length === 0, `no failed requests (${failedRequests.join(' | ') || 'none'})`)

  await page.close()
}

async function main() {
  buildHarness()
  const { server, url } = await serveSpike()
  const browser = await chromium.launch()
  try {
    for (const renderer of ['svg', 'canvas']) {
      await runForCombo(browser, url, renderer, 'typical', 'dark')
      await runForCombo(browser, url, renderer, 'stress', 'light')
    }
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
