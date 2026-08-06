// Live terminal-search regression check (labels + typing latency).
// Run after ./run.sh all from the worktree root:
//   node frontend/scripts/validate-terminal-search.mjs
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

function log(k, v) {
  console.log(`${k}: ${v}`)
}

async function chooseSession(page) {
  // Optional override via SI_SESSION_ID — never print env-derived ids (CodeQL).
  const fromEnv = typeof process.env.SI_SESSION_ID === 'string' ? process.env.SI_SESSION_ID.trim() : ''
  if (fromEnv) return fromEnv
  const summaries = await (await page.request.get(new URL('/api/sessions', BASE_URL).toString())).json()
  const ranked = [...summaries]
    .filter(s => !s.is_live)
    .sort((a, b) => (b.message_count ?? 0) - (a.message_count ?? 0))
  if (!ranked.length) throw new Error('No recorded sessions available')
  return ranked[0].id
}

async function openSession(page, sessionId) {
  await page.goto(BASE_URL, { waitUntil: 'domcontentloaded' })
  // Filter may be en or zh depending on locale
  const filter = page.locator('input[placeholder*="会话"], input[placeholder*="session" i], input[placeholder*="Filter" i], input[placeholder*="过滤"]').first()
  await filter.waitFor({ state: 'visible', timeout: 15_000 })
  await filter.fill(sessionId)
  const row = page.locator(`[data-session-id="${sessionId}"]`)
  await row.waitFor({ state: 'visible', timeout: 15_000 })
  await row.click()
  await page.locator('.xterm-viewport').waitFor({ state: 'visible', timeout: 60_000 })
  // Large sessions need stream time
  await page.waitForTimeout(4_000)
}

async function findHighlightButton(bar) {
  const buttons = bar.locator('button')
  const n = await buttons.count()
  for (let i = 0; i < n; i++) {
    const t = (await buttons.nth(i).innerText()).trim()
    if (['HL', 'All', '高亮', '全亮'].includes(t)) {
      return { btn: buttons.nth(i), text: t, title: await buttons.nth(i).getAttribute('title') }
    }
  }
  return { btn: null, text: '', title: '' }
}

async function openSearch(page) {
  await page.locator('.xterm').first().click({ force: true }).catch(() => {})
  await page.keyboard.press('Control+f')
  await page.locator('[data-testid="terminal-search-bar"]').waitFor({ state: 'visible', timeout: 15_000 })
  return page.locator('[data-testid="terminal-search-bar"]')
}

const browser = await chromium.launch({ headless: true })
const page = await browser.newPage({ viewport: { width: 1400, height: 900 } })

try {
  // Prefer an explicit locale via storage; do not pin en in addInitScript
  // (that would overwrite a later zh-CN reload).
  await page.addInitScript(() => {
    if (!localStorage.getItem('si-locale')) localStorage.setItem('si-locale', 'en')
  })
  const sessionId = await chooseSession(page)
  log('sessionSelected', true)
  await openSession(page, sessionId)

  let bar = await openSearch(page)
  log('searchBarOpen', true)
  let hl = await findHighlightButton(bar)
  log('enShortLabel', hl.text)
  log('enTitle', hl.title)
  if (hl.text !== 'HL') throw new Error(`expected HL, got ${JSON.stringify(hl.text)}`)

  const input = bar.locator('input')
  await input.click()
  await input.fill('')
  const t0 = Date.now()
  await input.type('function', { delay: 25 })
  // debounce 180ms + full buffer scan
  await page.waitForTimeout(2_000)
  const elapsed = Date.now() - t0
  let countText = ''
  const spans = bar.locator('span')
  for (let i = 0; i < await spans.count(); i++) {
    const t = (await spans.nth(i).innerText()).trim()
    if (t) countText = t
  }
  log('searchResultText', countText)
  log('typeAndSearchMs', elapsed)
  // Soft budget: typing+debounce+search should not freeze multi-second UI
  if (elapsed > 12_000) throw new Error(`search too slow: ${elapsed}ms`)

  const t2 = Date.now()
  await hl.btn.click()
  await page.waitForTimeout(40)
  log('ariaPressedAfterToggle', await hl.btn.getAttribute('aria-pressed'))
  log('toggleMs', Date.now() - t2)

  // Chinese labels — set before navigation so init script does not clobber.
  await page.evaluate(() => localStorage.setItem('si-locale', 'zh-CN'))
  await openSession(page, sessionId)
  bar = await openSearch(page)
  hl = await findHighlightButton(bar)
  log('zhShortLabel', hl.text)
  log('zhTitle', hl.title)
  if (hl.text !== '高亮') throw new Error(`expected 高亮, got ${JSON.stringify(hl.text)}`)

  fs.mkdirSync(SHOT_DIR, { recursive: true })
  const shot = path.join(SHOT_DIR, 'terminal-search-zh.png')
  await page.screenshot({ path: shot })
  log('screenshot', shot)
  console.log('PASS')
} finally {
  await browser.close()
}
