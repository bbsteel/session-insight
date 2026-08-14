// Live check: xterm buffer must retain the full render so MiniMap coordinates match.
// Run after ./run.sh all: node frontend/scripts/validate-minimap-scrollback.mjs
// Optional: SI_SESSION_ID=<id> chooses a specific recorded session.
import { chromium } from 'playwright'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const urlFiles = [
  path.join(repoRoot, '.runtime/session-insight.url'),
  path.join(repoRoot, 'session-insight.url'),
]
const BASE_URL = urlFiles.find(fs.existsSync)
  ? fs.readFileSync(urlFiles.find(fs.existsSync), 'utf8').trim()
  : 'http://127.0.0.1:8080/'
const SHOT_DIR = path.join(repoRoot, '.runtime')
const DEFAULT_SCROLLBACK = 20_000

let failures = 0
function check(name, condition, detail = '') {
  if (condition) console.log(`PASS: ${name}`)
  else {
    failures++
    console.error(`FAIL: ${name}${detail ? ` — ${detail}` : ''}`)
  }
}

async function chooseLongSession(page) {
  if (process.env.SI_SESSION_ID) return process.env.SI_SESSION_ID
  const summaries = await (await page.request.get(new URL('/api/sessions', BASE_URL).toString())).json()
  let best = null
  for (const summary of summaries.filter(s => !s.is_live)) {
    const posRes = await page.request.get(new URL(`/api/sessions/${summary.id}/positions?cols=120`, BASE_URL).toString())
    if (posRes.status() !== 200) continue
    const positions = await posRes.json()
    const totalLines = positions.total_lines ?? 0
    if (totalLines > DEFAULT_SCROLLBACK && (!best || totalLines > best.totalLines)) {
      best = { id: summary.id, totalLines }
    }
  }
  if (!best) throw new Error('No recorded session longer than 20000 lines was found; set SI_SESSION_ID.')
  console.log(`Using session ${best.id} (${best.totalLines} lines)`)
  return best.id
}

async function openSession(page, sessionId) {
  await page.goto(BASE_URL, { waitUntil: 'domcontentloaded' })
  const filter = page.locator('input[placeholder="过滤会话…"], input[placeholder="Filter sessions…"]')
  await filter.waitFor({ state: 'visible', timeout: 15_000 })
  await filter.fill(sessionId)
  const row = page.locator(`[data-session-id="${sessionId}"]`)
  await row.waitFor({ state: 'visible', timeout: 15_000 })
  await row.click()
  await page.locator('.xterm-viewport').waitFor({ state: 'visible', timeout: 30_000 })
}

async function bufferStats(page) {
  return page.evaluate(() => {
    const host = document.querySelector('[data-buffer-lines]')
    const viewport = document.querySelector('.xterm-viewport')
    const screen = document.querySelector('.xterm-screen')
    const track = document.querySelector('.minimap-shell .flex-1.relative')
    const frames = track ? [...track.querySelectorAll(':scope > div')] : []
    const frame = frames.find(el => el.style && el.style.transform && el.style.transform.includes('translateY'))
      ?? frames[frames.length - 1]
    if (!host || !viewport || !screen || !track || !frame) {
      return {
        ok: false,
        reason: 'missing-dom',
        hasHost: !!host,
        hasViewport: !!viewport,
        hasScreen: !!screen,
        hasTrack: !!track,
        hasFrame: !!frame,
        trackChildren: track?.childElementCount ?? 0,
      }
    }
    const cellHeight = 14
    const bufferLines = Number(host.dataset.bufferLines || 0)
    const scrollback = Number(host.dataset.scrollback || 0)
    const trackBox = track.getBoundingClientRect()
    const frameBox = frame.getBoundingClientRect()
    const range = document.querySelector('.minimap-shell span.text-meta')?.textContent?.trim() ?? ''
    return {
      ok: true,
      cellHeight,
      bufferLines,
      scrollback,
      scrollHeight: viewport.scrollHeight,
      screenHeight: screen.clientHeight,
      scrollTop: viewport.scrollTop,
      maxScroll: Math.max(0, viewport.scrollHeight - viewport.clientHeight),
      trackHeight: trackBox.height,
      frameTop: frameBox.top - trackBox.top,
      frameBottom: frameBox.bottom - trackBox.top,
      range,
    }
  })
}

async function waitForFullBuffer(page, sessionId) {
  const deadline = Date.now() + 180_000
  let last = null
  let lastLog = 0
  while (Date.now() < deadline) {
    const cols = await page.evaluate(() => {
      const screen = document.querySelector('.xterm-screen')
      return screen ? Math.max(40, Math.round(screen.clientWidth / 8.5)) : 120
    })
    const posRes = await page.request.get(new URL(`/api/sessions/${sessionId}/positions?cols=${cols}`, BASE_URL).toString())
    const positions = posRes.status() === 200 ? await posRes.json() : null
    const totalLines = positions?.total_lines ?? 0
    last = await bufferStats(page)
    const displayedTotal = Number((last.range || '').split('/').pop()?.trim() || 0)
    const expected = displayedTotal > 0 ? displayedTotal : totalLines
    if (last.ok && expected > DEFAULT_SCROLLBACK && last.bufferLines >= expected * 0.9) {
      return { ...last, totalLines: expected, apiTotalLines: totalLines }
    }
    if (Date.now() - lastLog > 5000) {
      console.log(JSON.stringify({ waiting: true, totalLines, last }))
      lastLog = Date.now()
    }
    await page.waitForTimeout(500)
  }
  throw new Error(`Buffer never reached positions total_lines: ${JSON.stringify(last)}`)
}

async function run() {
  fs.mkdirSync(SHOT_DIR, { recursive: true })
  const browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const page = await context.newPage()
  const consoleErrors = []
  page.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()) })
  page.on('pageerror', error => consoleErrors.push(String(error)))

  try {
    const sessionId = await chooseLongSession(page)
    await openSession(page, sessionId)
    const ready = await waitForFullBuffer(page, sessionId)
    console.log(JSON.stringify({ phase: 'ready', ...ready }))
    check(
      'xterm buffer retains more than the 20000-line scrollback cap',
      ready.bufferLines > DEFAULT_SCROLLBACK + 50,
      `bufferLines=${ready.bufferLines.toFixed(1)} total_lines=${ready.totalLines}`,
    )
    check(
      'xterm buffer length matches positions total_lines',
      Math.abs(ready.bufferLines - ready.totalLines) <= Math.max(80, ready.totalLines * 0.05),
      `bufferLines=${ready.bufferLines.toFixed(1)} total_lines=${ready.totalLines}`,
    )

    const bottomBtn = page.locator('.minimap-shell button').last()
    await bottomBtn.click()
    await page.waitForTimeout(400)
    const atBottom = await bufferStats(page)
    console.log(JSON.stringify({ phase: 'bottom', ...atBottom }))
    check(
      'viewport indicator sits at the bottom of the minimap track',
      atBottom.frameBottom >= atBottom.trackHeight - 8,
      JSON.stringify({ frameBottom: atBottom.frameBottom, trackHeight: atBottom.trackHeight, range: atBottom.range }),
    )

    const topBtn = page.locator('.minimap-shell button').first()
    await topBtn.click()
    await page.waitForTimeout(400)
    const atTop = await bufferStats(page)
    console.log(JSON.stringify({ phase: 'top', ...atTop }))
    check(
      'viewport indicator sits at the top of the minimap track',
      atTop.frameTop <= 8,
      JSON.stringify({ frameTop: atTop.frameTop, range: atTop.range }),
    )

    check('no console errors', consoleErrors.length === 0, consoleErrors.slice(0, 3).join(' | '))
    await page.screenshot({ path: path.join(SHOT_DIR, 'minimap-scrollback.png'), fullPage: true })
    console.log(`Screenshot saved: ${path.join(SHOT_DIR, 'minimap-scrollback.png')}`)
  } catch (error) {
    failures++
    console.error(`FATAL: ${error.message}`)
    await page.screenshot({ path: path.join(SHOT_DIR, 'minimap-scrollback-error.png'), fullPage: true }).catch(() => {})
  } finally {
    await browser.close()
  }

  console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECK(S) FAILED`)
  process.exit(failures === 0 ? 0 : 1)
}

await run()
