/**
 * Benchmark runner: drives the spike harness in headless Chromium via
 * Playwright and records reproducible measurements for both renderer
 * candidates across all three dataset scales.
 *
 * Usage:
 *   node frontend/spike/collab-timeline/scripts/run-bench.mjs [--quick]
 *
 * Writes report/results.json and prints a markdown results table.
 * Not part of the frontend `npm test` aggregator by design (local spike only).
 */

import { chromium } from 'playwright'
import { mkdirSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { buildHarness, envInfo, fmt, median, p95, serveSpike } from './lib.mjs'

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
const RENDERERS = ['svg', 'canvas']

const BUDGETS = {
  typical: { layoutPlusFirstMs: 100, hoverMs: 50 },
  stress: { layoutMs: 50, visibleMs: 250, frameMs: 20 },
}

async function measureCombo(browser, baseUrl, renderer, dataset) {
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

  await page.goto(`${baseUrl}?renderer=${renderer}&dataset=${dataset}&theme=dark&lang=en`)
  await page.waitForFunction(() => window.__spike?.ready)

  const hash = await page.evaluate(() => window.__spike.hash())

  // Pure layout time (pure TS, no DOM).
  const layoutRaw = await page.evaluate((runs) => window.__spike.measureLayout(runs), RUNS.layout)
  const layout = layoutRaw.slice(RUNS.layoutWarmup)

  // First visible render after data (remount + layout + paint).
  const fr = await page.evaluate(
    (runs) => window.__spike.measureFirstRender(runs),
    RUNS.firstRender,
  )
  const firstMount = fr.mount.slice(RUNS.firstRenderWarmup)
  const firstVisible = fr.visible.slice(RUNS.firstRenderWarmup)

  const counts = await page.evaluate(() => window.__spike.counts())
  const range = await page.evaluate(() => window.__spike.visibleRowRange())

  // Vertical scroll responsiveness.
  const scrollFrames = []
  for (let i = 0; i < RUNS.scroll; i++) {
    const delta = i % 2 === 0 ? 3 * 28 : -3 * 28
    const r = await page.evaluate((d) => window.__spike.scrollStep(d), delta)
    scrollFrames.push(r.frame)
  }

  // Horizontal pan responsiveness.
  const panFrames = []
  for (let i = 0; i < RUNS.pan; i++) {
    const dxMs = (i % 2 === 0 ? 1 : -1) * 5 * 60 * 1000
    const r = await page.evaluate((d) => window.__spike.panStep(d), dxMs)
    panFrames.push(r.frame)
  }

  // Zoom responsiveness.
  const zoomFrames = []
  for (let i = 0; i < RUNS.zoom; i++) {
    const r = await page.evaluate((f) => window.__spike.zoomStep(f), i % 2 === 0 ? 1.25 : 0.8)
    zoomFrames.push(r.frame)
  }

  // Hover latency across visible rows.
  const hoverFrames = []
  let hoverHits = 0
  for (let i = 0; i < RUNS.hover; i++) {
    const row = 1 + (i % Math.max(1, Math.min(10, range.total - 1)))
    const r = await page.evaluate((rw) => window.__spike.hoverRow(rw), row)
    if (r.id) hoverHits++
    hoverFrames.push(r.frame)
  }

  // Select latency.
  const selectFrames = []
  for (let i = 0; i < RUNS.select; i++) {
    const row = 1 + (i % Math.max(1, Math.min(10, range.total - 1)))
    const r = await page.evaluate((rw) => window.__spike.selectRow(rw), row)
    selectFrames.push(r.frame)
  }

  // Active-lane update cost (live interval extension + status change).
  const liveFrames = []
  for (let i = 0; i < RUNS.live; i++) {
    const r = await page.evaluate(() => window.__spike.liveUpdateStep())
    liveFrames.push(r.frame)
  }

  // Memory behavior over dataset switches.
  const heaps = []
  const firstHeap = await page.evaluate(() => window.__spike.gcHeap())
  if (firstHeap !== null) heaps.push(firstHeap)
  for (let i = 0; i < RUNS.switches; i++) {
    await page.evaluate(() => window.__spike.switchDataset())
    const h = await page.evaluate(() => window.__spike.gcHeap())
    if (h !== null) heaps.push(h)
  }

  await page.close()

  const frameStats = (frames) => ({
    median: median(frames),
    p95: p95(frames),
    under20ms: frames.filter((f) => f < 20).length / Math.max(1, frames.length),
  })

  return {
    renderer,
    dataset,
    hash,
    consoleErrors,
    failedRequests,
    counts,
    range,
    layout: { median: median(layout), p95: p95(layout) },
    firstMount: { median: median(firstMount), p95: p95(firstMount) },
    firstVisible: { median: median(firstVisible), p95: p95(firstVisible) },
    layoutPlusFirst: {
      median: median(layout) + median(firstVisible),
      p95: p95(layout) + p95(firstVisible),
    },
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
  const { server, url } = await serveSpike()
  const browser = await chromium.launch({
    args: ['--enable-precise-memory-info', '--js-flags=--expose-gc'],
  })
  const browserVersion = browser.version()
  const results = []
  try {
    for (const dataset of DATASETS) {
      for (const renderer of RENDERERS) {
        process.stdout.write(`measuring ${renderer} / ${dataset} ... `)
        const r = await measureCombo(browser, url, renderer, dataset)
        results.push(r)
        console.log(
          `layout ${fmt(r.layout.median)} ms, first-visible ${fmt(r.firstVisible.median)} ms, ` +
            `scroll p95 ${fmt(r.scroll.p95)} ms, mounted ${r.counts.mounted}`,
        )
        if (r.consoleErrors.length > 0) console.error(`  console errors: ${r.consoleErrors.join(' | ')}`)
        if (r.failedRequests.length > 0) console.error(`  failed requests: ${r.failedRequests.join(' | ')}`)
      }
    }
  } finally {
    await browser.close()
    server.close()
  }

  const env = { ...envInfo(), chromium: browserVersion }
  const outDir = join(dirname(new URL(import.meta.url).pathname), '..', 'report')
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

  // Markdown summary to stdout.
  console.log('\n| Candidate | Dataset | Layout med/p95 (ms) | First visible med/p95 (ms) | Scroll frame med/p95 (ms) | Pan frame med/p95 (ms) | Hover med/p95 (ms) | Select med/p95 (ms) | Live update med/p95 (ms) | Mounted | Heap first→last (MB) |')
  console.log('|---|---|---|---|---|---|---|---|---|---|---|')
  for (const r of results) {
    const heap = r.memoryMB.length > 0 ? `${r.memoryMB[0].toFixed(1)}→${r.memoryMB[r.memoryMB.length - 1].toFixed(1)}` : 'n/a'
    console.log(
      `| ${r.renderer} | ${r.dataset} | ${fmt(r.layout.median)}/${fmt(r.layout.p95)} | ` +
        `${fmt(r.firstVisible.median)}/${fmt(r.firstVisible.p95)} | ${fmt(r.scroll.median)}/${fmt(r.scroll.p95)} | ` +
        `${fmt(r.pan.median)}/${fmt(r.pan.p95)} | ${fmt(r.hover.median)}/${fmt(r.hover.p95)} | ` +
        `${fmt(r.select.median)}/${fmt(r.select.p95)} | ${fmt(r.live.median)}/${fmt(r.live.p95)} | ` +
        `${r.counts.mounted} (${r.counts.stats?.mountedIntervals ?? '?'} intervals) | ${heap} |`,
    )
  }
  console.log('\nBudget checks:')
  for (const r of results) {
    for (const c of budgetChecks(r)) {
      console.log(`- [${c.pass ? 'PASS' : 'FAIL'}] ${r.renderer}/${r.dataset}: ${c.name} — ${c.value}`)
    }
  }
  console.log(`\nresults written to ${join(outDir, 'results.json')}`)
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
