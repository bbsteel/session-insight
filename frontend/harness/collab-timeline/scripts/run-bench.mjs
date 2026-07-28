/**
 * Production-core benchmark: drives the production CollaborationTimeline
 * harness (React component + production i18n/CSS) in headless Chromium and
 * checks the accepted performance gates from the collaboration design §12.4.
 *
 * Usage:
 *   node frontend/harness/collab-timeline/scripts/run-bench.mjs [--quick]
 *
 * Writes report/results.json and prints a markdown results table.
 * Not part of the frontend `npm test` aggregator by design (environment-dependent).
 */

import { chromium } from 'playwright'
import { mkdirSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { buildHarness, envInfo, fmt, median, p95, serveHarness } from './lib.mjs'

const QUICK = process.argv.includes('--quick')

const RUNS = {
  layout: QUICK ? 12 : 25,
  layoutWarmup: QUICK ? 3 : 5,
  firstRender: QUICK ? 6 : 12,
  firstRenderWarmup: QUICK ? 1 : 2,
  scroll: QUICK ? 40 : 120,
  pan: QUICK ? 24 : 60,
  zoom: QUICK ? 12 : 30,
  hover: QUICK ? 16 : 40,
  select: QUICK ? 12 : 30,
  live: QUICK ? 20 : 50,
  switches: 20,
}

const DATASETS = ['typical', 'large', 'stress']

const BUDGETS = {
  typical: { layoutPlusFirstMs: 100, hoverMs: 50 },
  stress: { layoutMs: 50, visibleMs: 250, frameMs: 20 },
}

async function measureDataset(browser, baseUrl, dataset) {
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

  await page.goto(`${baseUrl}?dataset=${dataset}&theme=dark&lang=en`)
  await page.waitForFunction(() => window.__collab?.ready)

  const hash = await page.evaluate(() => window.__collab.hash())

  // Pure layout time (pure TS, no DOM).
  const layoutRaw = await page.evaluate((runs) => window.__collab.measureLayout(runs), RUNS.layout)
  const layout = layoutRaw.slice(RUNS.layoutWarmup)

  // First visible render after data (remount + layout + paint).
  const fr = await page.evaluate((runs) => window.__collab.measureFirstRender(runs), RUNS.firstRender)
  const firstMount = fr.mount.slice(RUNS.firstRenderWarmup)
  const firstVisible = fr.visible.slice(RUNS.firstRenderWarmup)

  const counts = await page.evaluate(() => window.__collab.counts())
  const range = await page.evaluate(() => window.__collab.visibleRowRange())

  // Vertical scroll responsiveness.
  const scrollFrames = []
  for (let i = 0; i < RUNS.scroll; i++) {
    const delta = i % 2 === 0 ? 3 * 28 : -3 * 28
    const r = await page.evaluate((d) => window.__collab.scrollStep(d), delta)
    scrollFrames.push(r.frame)
  }

  // Horizontal pan responsiveness (pointer drag on the graphics viewport).
  const panFrames = []
  for (let i = 0; i < RUNS.pan; i++) {
    const dx = (i % 2 === 0 ? 1 : -1) * 48
    const r = await page.evaluate((d) => window.__collab.panStep(d), dx)
    panFrames.push(r.frame)
  }

  // Zoom responsiveness (ctrl+wheel on the graphics viewport).
  const zoomFrames = []
  for (let i = 0; i < RUNS.zoom; i++) {
    const r = await page.evaluate((f) => window.__collab.zoomStep(f), i % 2 === 0 ? 1.25 : 0.8)
    zoomFrames.push(r.frame)
  }

  // Hover latency across visible rows.
  const hoverFrames = []
  let hoverHits = 0
  for (let i = 0; i < RUNS.hover; i++) {
    const row = 1 + (i % Math.max(1, Math.min(10, range.total - 1)))
    const r = await page.evaluate((rw) => window.__collab.hoverRow(rw), row)
    if (r.id) hoverHits++
    hoverFrames.push(r.frame)
  }

  // Select latency.
  const selectFrames = []
  for (let i = 0; i < RUNS.select; i++) {
    const row = 1 + (i % Math.max(1, Math.min(10, range.total - 1)))
    const r = await page.evaluate((rw) => window.__collab.selectRow(rw), row)
    selectFrames.push(r.frame)
  }

  // Live-geometry refresh cost (nowMs advance through props).
  const liveFrames = []
  for (let i = 0; i < RUNS.live; i++) {
    const r = await page.evaluate(() => window.__collab.liveUpdateStep())
    liveFrames.push(r.frame)
  }

  // Memory behavior over dataset switches.
  const heaps = []
  const firstHeap = await page.evaluate(() => window.__collab.gcHeap())
  if (firstHeap !== null) heaps.push(firstHeap)
  for (let i = 0; i < RUNS.switches; i++) {
    await page.evaluate(() => window.__collab.switchDataset())
    const h = await page.evaluate(() => window.__collab.gcHeap())
    if (h !== null) heaps.push(h)
  }

  await page.close()

  const frameStats = (frames) => ({
    median: median(frames),
    p95: p95(frames),
    under20ms: frames.filter((f) => f < 20).length / Math.max(1, frames.length),
  })

  // Percentiles are not additive: derive the combined statistic from
  // pairwise sums of the underlying samples.
  const pairCount = Math.min(layout.length, firstVisible.length)
  const layoutPlusFirstSamples = Array.from({ length: pairCount }, (_, i) => layout[i] + firstVisible[i])

  return {
    dataset,
    hash,
    consoleErrors,
    failedRequests,
    counts,
    range,
    layout: { median: median(layout), p95: p95(layout) },
    firstMount: { median: median(firstMount), p95: p95(firstMount) },
    firstVisible: { median: median(firstVisible), p95: p95(firstVisible) },
    layoutPlusFirst: { median: median(layoutPlusFirstSamples), p95: p95(layoutPlusFirstSamples) },
    scroll: frameStats(scrollFrames),
    pan: frameStats(panFrames),
    zoom: frameStats(zoomFrames),
    hover: { ...frameStats(hoverFrames), hits: hoverHits, samples: RUNS.hover },
    select: frameStats(selectFrames),
    live: frameStats(liveFrames),
    memoryMB: heaps.map((h) => h / 1048576),
  }
}

function budgetChecks(r) {
  const checks = []
  if (r.dataset === 'typical') {
    checks.push({
      name: 'typical layout+first render < 100 ms',
      pass: r.layoutPlusFirst.median < BUDGETS.typical.layoutPlusFirstMs,
      value: `${fmt(r.layoutPlusFirst.median)} ms (median)`,
    })
    checks.push({
      name: 'typical hover/select < 50 ms',
      pass: r.hover.median < BUDGETS.typical.hoverMs && r.select.median < BUDGETS.typical.hoverMs,
      value: `hover ${fmt(r.hover.median)} / select ${fmt(r.select.median)} ms (median)`,
    })
  }
  if (r.dataset === 'stress') {
    checks.push({
      name: 'stress pure layout < 50 ms',
      pass: r.layout.median < BUDGETS.stress.layoutMs,
      value: `${fmt(r.layout.median)} ms (median)`,
    })
    checks.push({
      name: 'stress visible result < 250 ms',
      pass: r.firstVisible.median < BUDGETS.stress.visibleMs,
      value: `${fmt(r.firstVisible.median)} ms (median)`,
    })
    checks.push({
      name: 'stress most direct-manipulation frames < 20 ms',
      pass: r.scroll.under20ms > 0.5 && r.pan.under20ms > 0.5,
      value: `scroll ${(r.scroll.under20ms * 100).toFixed(0)}% / pan ${(r.pan.under20ms * 100).toFixed(0)}% under 20 ms`,
    })
  }
  return checks
}

async function main() {
  buildHarness()
  const { server, url } = await serveHarness()
  const browser = await chromium.launch({
    args: ['--enable-precise-memory-info', '--js-flags=--expose-gc'],
  })
  const browserVersion = browser.version()
  const results = []
  try {
    for (const dataset of DATASETS) {
      process.stdout.write(`measuring production / ${dataset} ... `)
      const r = await measureDataset(browser, url, dataset)
      results.push(r)
      console.log(
        `layout ${fmt(r.layout.median)} ms, first-visible ${fmt(r.firstVisible.median)} ms, ` +
          `scroll p95 ${fmt(r.scroll.p95)} ms, mounted ${r.counts.mounted}`,
      )
      if (r.consoleErrors.length > 0) console.error(`  console errors: ${r.consoleErrors.join(' | ')}`)
      if (r.failedRequests.length > 0) console.error(`  failed requests: ${r.failedRequests.join(' | ')}`)
    }
  } finally {
    await browser.close()
    server.close()
  }

  const env = { ...envInfo(), chromium: browserVersion }
  const outDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'report')
  mkdirSync(outDir, { recursive: true })
  const payload = {
    generatedAt: new Date().toISOString(),
    quick: QUICK,
    runs: RUNS,
    budgets: BUDGETS,
    environment: env,
    results: results.map((r) => ({ ...r, budgetChecks: budgetChecks(r) })),
  }
  writeFileSync(join(outDir, 'results.json'), JSON.stringify(payload, null, 2) + '\n')

  console.log('\n| Dataset | Layout med/p95 (ms) | First visible med/p95 (ms) | Scroll frame med/p95 (ms) | Pan frame med/p95 (ms) | Hover med/p95 (ms) | Select med/p95 (ms) | Live update med/p95 (ms) | Mounted | Heap first→last (MB) |')
  console.log('|---|---|---|---|---|---|---|---|---|---|')
  for (const r of results) {
    const heap = r.memoryMB.length > 0 ? `${r.memoryMB[0].toFixed(1)}→${r.memoryMB[r.memoryMB.length - 1].toFixed(1)}` : 'n/a'
    console.log(
      `| ${r.dataset} | ${fmt(r.layout.median)}/${fmt(r.layout.p95)} | ` +
        `${fmt(r.firstVisible.median)}/${fmt(r.firstVisible.p95)} | ${fmt(r.scroll.median)}/${fmt(r.scroll.p95)} | ` +
        `${fmt(r.pan.median)}/${fmt(r.pan.p95)} | ${fmt(r.hover.median)}/${fmt(r.hover.p95)} | ` +
        `${fmt(r.select.median)}/${fmt(r.select.p95)} | ${fmt(r.live.median)}/${fmt(r.live.p95)} | ` +
        `${r.counts.mounted} (${r.counts.stats?.mountedIntervals ?? '?'} intervals) | ${heap} |`,
    )
  }
  console.log('\nBudget checks:')
  let failed = 0
  for (const r of results) {
    for (const c of budgetChecks(r)) {
      if (!c.pass) failed++
      console.log(`- [${c.pass ? 'PASS' : 'FAIL'}] production/${r.dataset}: ${c.name} — ${c.value}`)
    }
  }
  console.log(`\nresults written to ${join(outDir, 'results.json')}`)
  if (failed > 0) process.exit(1)
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
