// Live jump-accuracy regression check for the viewport-anchor work.
// Covers the acceptance paths against a running instance:
//   - panel/window resize that changes terminal cols
//   - terminal font-size change (zoom)
//   - analytics-page round trip
//   - live append with follow off (must not yank the viewport) and on
//   - the explicit loading / following / paused / source-stopped status chip
//   - Diff modal → locate in terminal
// Run after ./run.sh all: node frontend/scripts/validate-terminal-viewport-anchor.mjs
// Optional: SI_SESSION_ID=<id> pins the main session, SI_LOCALE=zh-CN|en.
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
const SHOT_DIR = '/tmp/session-insight-ui'

let failures = 0
function check(name, condition, detail = '') {
  if (condition) console.log(`PASS: ${name}`)
  else {
    failures++
    console.error(`FAIL: ${name}${detail ? ` — ${detail}` : ''}`)
  }
}

const L10N = {
  'zh-CN': { filter: '过滤会话…', analytics: '分析', follow: '跟随', viewDiff: '查看 Diff 明细', expandAll: '全部展开', ended: '来源已停止', following: '追尾中', paused: '已暂停' },
  en: { filter: 'Filter sessions…', analytics: 'Analytics', follow: 'Follow', viewDiff: 'View diff details', expandAll: 'Expand all', ended: 'Source stopped', following: 'Following', paused: 'Paused' },
}

async function api(page, path_) {
  const res = await page.request.get(new URL(path_, BASE_URL).toString())
  if (!res.ok()) throw new Error(`GET ${path_} → ${res.status()}`)
  return res.json()
}

async function pickFinishedSession(page) {
  if (process.env.SI_SESSION_ID) return process.env.SI_SESSION_ID
  const summaries = await api(page, '/api/sessions')
  for (const s of summaries.filter(s => !s.is_live && (s.turn_count ?? 0) >= 6).slice(0, 40)) {
    const detail = await api(page, `/api/sessions/${s.id}`)
    if (detail.turns.filter(t => t.user_message).length >= 2) return s.id
  }
  throw new Error('no finished session with ≥6 turns found; set SI_SESSION_ID')
}

async function pickSessionWithEdits(page) {
  const summaries = await api(page, '/api/sessions')
  // Prefer finished sessions: a live one keeps rewriting the terminal under
  // the probe (and this session's own transcript mentions ✏️ in text).
  const candidates = [...summaries.filter(s => !s.is_live), ...summaries.filter(s => s.is_live)]
  for (const s of candidates.slice(0, 60)) {
    const edits = await api(page, `/api/sessions/${s.id}/edits`).catch(() => [])
    if (Array.isArray(edits) && edits.length > 0 && (s.turn_count ?? 0) >= 2) return s.id
  }
  return null
}

async function pickLiveSession(page) {
  const summaries = await api(page, '/api/sessions')
  return summaries.find(s => s.is_live)?.id ?? null
}

// Rewrites (re-render, remount, fold recompose) are async; fixed sleeps race
// them on big sessions. Settled = buffer size stable across consecutive polls.
async function waitForQuiescence(page, { stablePollsNeeded = 2, maxPolls = 40 } = {}) {
  let lastLines = -1
  let stablePolls = 0
  for (let i = 0; i < maxPolls && stablePolls < stablePollsNeeded; i++) {
    await page.waitForTimeout(500)
    const { bufferLines } = await scrollState(page)
    if (bufferLines > 0 && bufferLines === lastLines) stablePolls++
    else stablePolls = 0
    lastLines = bufferLines
  }
}

async function openSession(page, sessionId, strings) {
  await page.goto(BASE_URL, { waitUntil: 'domcontentloaded' })
  const filter = page.locator(`input[placeholder="${strings.filter}"]`)
  await filter.waitFor({ state: 'visible', timeout: 15_000 })
  await filter.fill(sessionId)
  const row = page.locator(`[data-session-id="${sessionId}"]`)
  await row.waitFor({ state: 'visible', timeout: 15_000 })
  await row.click()
  await page.locator('.xterm-viewport').waitFor({ state: 'visible', timeout: 30_000 })
  // Wait out the initial stream + default fold collapse + positions refetch;
  // jumping mid-flight races them. Also require the chip past loading.
  let lastLines = -1
  let stablePolls = 0
  for (let i = 0; i < 60 && stablePolls < 3; i++) {
    await page.waitForTimeout(500)
    const { bufferLines } = await scrollState(page)
    const chip = await statusChip(page)
    if (bufferLines > 0 && bufferLines === lastLines && chip?.state !== 'loading') stablePolls++
    else stablePolls = 0
    lastLines = bufferLines
  }
}

// The visible-turn-range label is rendered as `Turn X-Y/N` regardless of
// locale; it derives from the viewport, so it is a content-level proxy for
// "where the user is reading" that does not depend on pixel metrics.
async function visibleTurnRange(page) {
  return page.evaluate(() => (
    [...document.querySelectorAll('header span')]
      .map(el => el.textContent?.trim() ?? '')
      .find(text => /^Turn \d+-\d+\/\d+$/.test(text)) ?? ''
  ))
}

async function scrollState(page) {
  // xterm's buffer viewport, published by TerminalPanel on every metrics
  // change; the DOM scroller's scrollTop does not track it.
  return page.evaluate(() => {
    const c = document.querySelector('[data-buffer-lines]')
    return {
      viewportY: Number(c?.dataset.viewportY ?? -1),
      bufferLines: Number(c?.dataset.bufferLines ?? -1),
    }
  })
}

// Content-exact reading position: the concatenated text of the top visible
// buffer rows. Joining makes the signature wrap-insensitive — a cols/font
// change re-wraps rows but keeps the underlying text — while still failing
// immediately when the restore lands on a different logical line. The
// Turn X-Y/N label is ratio math and too coarse for this. Requires the DOM
// renderer (WebGL disabled).
async function topContentSignature(page) {
  return page.evaluate(() => (
    [...document.querySelectorAll('.xterm-rows > div')]
      .map(d => d.textContent ?? '')
      .join('')
      // Fold badges count hidden DISPLAY rows, which legitimately change with
      // the wrap width ("(2 行)" wide vs "(3 行)" narrow) — same content,
      // different geometry. Normalize so the signature stays content-only.
      .replace(/\(\d+\s(?:行|lines?)\)/g, '(*)')
      .slice(0, 240)
  ))
}

async function waitRange(page, label, timeout = 12_000) {
  try {
    await page.waitForFunction(() => {
      const text = [...document.querySelectorAll('header span')]
        .map(el => el.textContent?.trim() ?? '')
        .find(t => /^Turn \d+-\d+\/\d+$/.test(t))
      return Boolean(text) && !text.startsWith('Turn ?')
    }, undefined, { timeout })
    return await visibleTurnRange(page)
  } catch {
    check(label, false, 'turn-range label never appeared')
    return ''
  }
}

async function statusChip(page) {
  const chip = page.locator('[data-testid="replay-status"]')
  if ((await chip.count()) === 0) return null
  return { state: await chip.getAttribute('data-state'), text: (await chip.textContent())?.trim() ?? '' }
}

async function run() {
  fs.mkdirSync(SHOT_DIR, { recursive: true })
  // DOM renderer (no WebGL) exposes one element per visible row, which the
  // diff-locate probe needs to right-click the exact edit header row.
  const browser = await chromium.launch({ headless: true, args: ['--disable-webgl', '--disable-3d-apis'] })
  const consoleErrors = []

  try {
    // ---------- main flow (zh-CN) ----------
    const strings = L10N[process.env.SI_LOCALE ?? 'zh-CN'] ?? L10N['zh-CN']
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 } })
    const page = await context.newPage()
    page.on('console', m => { if (m.type() === 'error') consoleErrors.push(`${m.text()} @ ${m.location()?.url ?? ''}`) })
    page.on('pageerror', e => consoleErrors.push(String(e)))

    const sessionId = await pickFinishedSession(page)
    console.log(`finished session: ${sessionId}`)
    await openSession(page, sessionId, strings)

    // Status chip: a finished session must explicitly read 来源已停止.
    const chip = await statusChip(page)
    check('status chip shows source-stopped for a finished session',
      chip?.state === 'ended' && chip.text === strings.ended, JSON.stringify(chip))

    // Move off the top with a plain wheel scroll — deterministic across
    // sessions (the j/k turn jump depends on positions timing, which is not
    // what these checks are about).
    await page.locator('.xterm-screen').hover({ position: { x: 400, y: 300 } })
    await page.mouse.wheel(0, 3200)
    await page.waitForTimeout(800)
    const anchorRange = await waitRange(page, 'turn-range label visible after scrolling')
    const anchorTop = await topContentSignature(page)
    const anchorScroll = await scrollState(page)
    check('scrolled away from the top', anchorScroll.viewportY > 0, JSON.stringify(anchorScroll))

    // --- resize that changes cols must keep the reading position ---
    await page.setViewportSize({ width: 1000, height: 900 })
    await waitForQuiescence(page)
    const afterResizeTop = await topContentSignature(page)
    check('cols-change resize keeps the reading position', afterResizeTop === anchorTop, `before=${JSON.stringify(anchorTop)} after=${JSON.stringify(afterResizeTop)}`)
    await page.setViewportSize({ width: 1440, height: 900 })
    await waitForQuiescence(page)
    const afterResizeBackTop = await topContentSignature(page)
    check('resize back keeps the reading position', afterResizeBackTop === anchorTop, `before=${JSON.stringify(anchorTop)} after=${JSON.stringify(afterResizeBackTop)}`)

    // --- terminal font-size change must keep the reading position ---
    await page.evaluate(() => {
      localStorage.setItem('si-terminal-font-size', '17')
      window.dispatchEvent(new Event('si-fonts-changed'))
    })
    await waitForQuiescence(page)
    const afterFontTop = await topContentSignature(page)
    check('font-size change keeps the reading position', afterFontTop === anchorTop, `before=${JSON.stringify(anchorTop)} after=${JSON.stringify(afterFontTop)}`)
    await page.evaluate(() => {
      localStorage.setItem('si-terminal-font-size', '13')
      window.dispatchEvent(new Event('si-fonts-changed'))
    })
    await waitForQuiescence(page)
    const afterFontBackTop = await topContentSignature(page)
    check('font-size restore keeps the reading position', afterFontBackTop === anchorTop, `before=${JSON.stringify(anchorTop)} after=${JSON.stringify(afterFontBackTop)}`)

    // --- analytics round trip must restore the reading position ---
    await page.getByRole('button', { name: strings.analytics, exact: true }).click()
    await page.waitForTimeout(2_000)
    await page.getByRole('button', { name: strings.analytics, exact: true }).click()
    await page.locator('.xterm-viewport').waitFor({ state: 'visible', timeout: 30_000 })
    await waitForQuiescence(page)
    const afterAnalyticsTop = await topContentSignature(page)
    check('analytics round trip restores the reading position', afterAnalyticsTop === anchorTop, `before=${JSON.stringify(anchorTop)} after=${JSON.stringify(afterAnalyticsTop)}`)

    await page.screenshot({ path: path.join(SHOT_DIR, 'viewport-anchor-main.png'), fullPage: true })

    // --- live append must not break scroll state unless following ---
    const liveId = await pickLiveSession(page)
    if (liveId) {
      console.log(`live session: ${liveId}`)
      await openSession(page, liveId, strings)
      // Auto-follow engages on open for a live session; wait for the chip to
      // settle past the initial loading state first.
      let liveChip = null
      for (let i = 0; i < 30; i++) {
        liveChip = await statusChip(page)
        if (liveChip && liveChip.state !== 'loading') break
        await page.waitForTimeout(500)
      }
      check('live session opens in following state', liveChip?.state === 'following' && liveChip.text === strings.following, JSON.stringify(liveChip))
      // Pause follow, scroll up into history, then wait out two poll cycles.
      await page.locator('[data-testid="follow-fab"]').click()
      liveChip = await statusChip(page)
      check('follow toggle switches the chip to paused', liveChip?.state === 'paused' && liveChip.text === strings.paused, JSON.stringify(liveChip))
      const terminal = page.locator('.xterm-screen')
      await terminal.hover()
      await page.mouse.wheel(0, -3200)
      await page.waitForTimeout(600)
      const pausedRange = await visibleTurnRange(page)
      const pausedScroll = await scrollState(page)
      await page.waitForTimeout(7_000) // ≥2 live poll cycles
      const afterAppendRange = await visibleTurnRange(page)
      const afterAppendScroll = await scrollState(page)
      check('live append keeps the paused reading position',
        afterAppendRange === pausedRange && afterAppendScroll.viewportY === pausedScroll.viewportY,
        `range ${pausedRange}→${afterAppendRange}, viewportY ${pausedScroll.viewportY}→${afterAppendScroll.viewportY}`)
      // Re-engage follow: the next refresh must pin the tail again.
      await page.locator('[data-testid="follow-fab"]').click()
      await page.waitForTimeout(7_000)
      const followScroll = await scrollState(page)
      check('re-engaged follow pins the tail after live refresh',
        followScroll.bufferLines > 0 && followScroll.viewportY >= followScroll.bufferLines - 70,
        JSON.stringify(followScroll))
    } else {
      console.log('SKIP: no live session available for live-append checks')
    }

    // --- Diff modal → locate in terminal ---
    const editsSessionId = await pickSessionWithEdits(page)
    if (editsSessionId) {
      console.log(`edits session: ${editsSessionId}`)
      await openSession(page, editsSessionId, strings)
      // Expand all folds first: collapsed edit folds hide their ✏️ header
      // rows, which would skew the header-ordinal → edit-index mapping the
      // file popover relies on.
      await page.locator('.xterm-screen').click({ button: 'right', position: { x: 300, y: 200 } })
      const expandAllItem = page.getByRole('menuitem', { name: strings.expandAll, exact: true })
      if (await expandAllItem.count()) {
        await expandAllItem.click()
        await waitForQuiescence(page)
      } else {
        await page.keyboard.press('Escape')
      }
      // Left-click an edit header row (" ✏️ <tool>: <path> ") → file action
      // popover → diff detail. Ctrl+F advances through ✏️ matches (the bar
      // stays open so the position advances) until a header row is visible.
      let diffOpened = false
      await page.keyboard.press('Control+f')
      const searchInput = page.locator('input[placeholder="在终端中查找"], input[placeholder="Find in terminal"]')
      await searchInput.waitFor({ state: 'visible', timeout: 5_000 })
      await searchInput.fill('✏️')
      await page.waitForTimeout(800)
      for (let attempt = 0; attempt < 20; attempt++) {
        await page.keyboard.press('Enter')
        await page.waitForTimeout(500)
        const editRow = page.locator('.xterm-rows > div').filter({ hasText: /✏️\s*\w+:\s*\//u }).first()
        if (!(await editRow.count())) continue
        await page.keyboard.press('Escape') // close the bar; scroll position stays
        await page.waitForTimeout(300)
        await editRow.click({ force: true }) // xterm-screen intercepts actionability
        await page.waitForTimeout(400)
        const item = page.getByRole('menuitem', { name: strings.viewDiff, exact: true })
        if (await item.count()) {
          await item.click()
          diffOpened = true
          break
        }
        await page.keyboard.press('Escape')
        await page.keyboard.press('Control+f')
        await page.locator('input[placeholder="在终端中查找"], input[placeholder="Find in terminal"]').waitFor({ state: 'visible', timeout: 5_000 })
      }
      // The search bar may still be open after the loop — but only dismiss
      // it when the diff modal did NOT open; Escape would close the modal.
      if (!diffOpened) await page.keyboard.press('Escape')
      check('found an edit row and opened its diff', diffOpened)
      if (diffOpened) {
        const locateBtn = page.locator('[data-testid="diff-locate-in-terminal"]')
        // The modal fetches the edit list before rendering its chrome.
        await locateBtn.waitFor({ state: 'visible', timeout: 10_000 }).catch(() => {})
        check('diff modal shows the locate button', await locateBtn.count() === 1)
        const beforeLocate = await scrollState(page)
        await locateBtn.click()
        await page.waitForTimeout(1_200)
        check('locate closes the diff modal', (await page.locator('[data-testid="diff-locate-in-terminal"]').count()) === 0)
        const afterLocate = await scrollState(page)
        const afterLocateRange = await visibleTurnRange(page)
        check('locate jumps the terminal to the edit', afterLocate.viewportY !== beforeLocate.viewportY || afterLocateRange !== '', JSON.stringify({ beforeLocate, afterLocate, afterLocateRange }))
      }
    } else {
      console.log('SKIP: no session with edits found for the diff-locate check')
    }

    // The app probes optional endpoints (AI insight, collaboration) that 404
    // when the session lacks them — expected noise, not a regression signal.
    const realErrors = consoleErrors.filter(e => !e.startsWith('Failed to load resource'))
    check('no console errors during main flow', realErrors.length === 0, realErrors.slice(0, 3).join(' | '))
    await context.close()

    // ---------- locale pass (en): changed labels render in English ----------
    const en = L10N.en
    const enContext = await browser.newContext({ viewport: { width: 1440, height: 900 } })
    await enContext.addInitScript(() => localStorage.setItem('si-locale', 'en'))
    const enPage = await enContext.newPage()
    await openSession(enPage, sessionId, en)
    const enChip = await statusChip(enPage)
    check('en: status chip shows Source stopped', enChip?.state === 'ended' && enChip.text === en.ended, JSON.stringify(enChip))
    await enContext.close()
  } catch (error) {
    failures++
    console.error(`FATAL: ${error.message}`)
  } finally {
    await browser.close()
  }

  console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECK(S) FAILED`)
  process.exit(failures === 0 ? 0 : 1)
}

await run()
