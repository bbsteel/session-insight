// Live replay-navigation regression check.
// Run after ./run.sh all: node frontend/scripts/validate-replay-navigation.mjs
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
const SHOT_DIR = '/tmp/session-insight-ui'

let failures = 0
function check(name, condition, detail = '') {
  if (condition) console.log(`PASS: ${name}`)
  else {
    failures++
    console.error(`FAIL: ${name}${detail ? ` — ${detail}` : ''}`)
  }
}

async function chooseSession(page) {
  if (process.env.SI_SESSION_ID) return process.env.SI_SESSION_ID
  const summaries = await (await page.request.get(new URL('/api/sessions', BASE_URL).toString())).json()
  for (const summary of summaries.filter(s => !s.is_live && (s.turn_count ?? 0) >= 4).slice(0, 40)) {
    const detail = await (await page.request.get(new URL(`/api/sessions/${summary.id}`, BASE_URL).toString())).json()
    if (detail.turns.filter(turn => turn.user_message).length >= 2) return summary.id
  }
  throw new Error('No non-live session with at least four turns and two user messages was found; set SI_SESSION_ID to a suitable recorded session.')
}

async function openSession(page, sessionId) {
  await page.goto(BASE_URL, { waitUntil: 'domcontentloaded' })
  const filter = page.locator('input[placeholder="过滤会话..."]')
  await filter.waitFor({ state: 'visible', timeout: 15_000 })
  await filter.fill(sessionId)
  const row = page.locator(`[data-session-id="${sessionId}"]`)
  await row.waitFor({ state: 'visible', timeout: 15_000 })
  await row.click()
  await page.locator('.xterm-viewport').waitFor({ state: 'visible', timeout: 30_000 })
  await page.waitForTimeout(1_500)
}

async function navigationState(page) {
  return page.locator('.xterm-viewport').evaluate(viewport => ({
    scrollTop: viewport.scrollTop,
    range: [...document.querySelectorAll('header span')]
      .map(el => el.textContent?.trim() ?? '')
      .find(text => /^Turn \d+-\d+\/\d+$/.test(text)) ?? '',
  }))
}

async function waitForNavigation(page, before, label) {
  try {
    await page.waitForFunction(
      prior => {
        const viewport = document.querySelector('.xterm-viewport')
        const range = [...document.querySelectorAll('header span')]
          .map(el => el.textContent?.trim() ?? '')
          .find(text => /^Turn \d+-\d+\/\d+$/.test(text)) ?? ''
        return viewport && (viewport.scrollTop !== prior.scrollTop || range !== prior.range)
      },
      before,
      { timeout: 10_000 },
    )
    check(label, true)
  } catch {
    const after = await navigationState(page)
    check(label, false, JSON.stringify({ before, after }))
  }
}

async function assertNoNavigation(page, before, label) {
  await page.waitForTimeout(500)
  const after = await navigationState(page)
  check(label, after.scrollTop === before.scrollTop && after.range === before.range, JSON.stringify({ before, after }))
}

async function chooseMenuItem(page, label) {
  const terminal = page.locator('.xterm').first()
  for (const y of [80, 180, 280]) {
    await terminal.click({ button: 'right', position: { x: 120, y } })
    const item = page.getByRole('menuitem', { name: label, exact: true })
    if (await item.count()) {
      await item.click()
      return
    }
    await page.keyboard.press('Escape')
  }
  throw new Error(`Could not open terminal context menu item: ${label}`)
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
    const sessionId = await chooseSession(page)
    console.log('Using a recorded session for replay navigation validation')
    await openSession(page, sessionId)

    let state = await navigationState(page)
    await page.locator('.xterm-screen').click({ position: { x: 120, y: 80 } })
    await page.keyboard.press('k')
    await assertNoNavigation(page, state, 'k at the first turn does not leave the boundary')

    await chooseMenuItem(page, '上一 Turn')
    await assertNoNavigation(page, state, 'context-menu previous turn respects the first-turn boundary')

    await page.keyboard.press('j')
    await waitForNavigation(page, state, 'j jumps to the next turn')
    state = await navigationState(page)
    await page.keyboard.press('k')
    await waitForNavigation(page, state, 'k jumps to the previous turn')

    state = await navigationState(page)
    await chooseMenuItem(page, '下一 Turn')
    await waitForNavigation(page, state, 'context-menu next turn jumps')
    state = await navigationState(page)
    await chooseMenuItem(page, '上一 Turn')
    await waitForNavigation(page, state, 'context-menu previous turn jumps')

    state = await navigationState(page)
    await chooseMenuItem(page, '上一条用户消息')
    await assertNoNavigation(page, state, 'context-menu previous user message respects the first-message boundary')

    await chooseMenuItem(page, '下一条用户消息')
    await waitForNavigation(page, state, 'context-menu next user message jumps')
    state = await navigationState(page)
    await chooseMenuItem(page, '上一条用户消息')
    await waitForNavigation(page, state, 'context-menu previous user message jumps')

    check('no console errors', consoleErrors.length === 0, consoleErrors.slice(0, 3).join(' | '))
    await page.screenshot({ path: path.join(SHOT_DIR, 'replay-navigation.png'), fullPage: true })
    console.log(`Screenshot saved: ${path.join(SHOT_DIR, 'replay-navigation.png')}`)
  } catch (error) {
    failures++
    console.error(`FATAL: ${error.message}`)
    await page.screenshot({ path: path.join(SHOT_DIR, 'replay-navigation-error.png'), fullPage: true }).catch(() => {})
  } finally {
    await browser.close()
  }

  console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECK(S) FAILED`)
  process.exit(failures === 0 ? 0 : 1)
}

await run()
