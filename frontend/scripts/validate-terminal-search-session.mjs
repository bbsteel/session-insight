// Measure Ctrl+F responsiveness on a specific long session.
// Usage:
//   SI_SESSION_ID=rollout-... node frontend/scripts/validate-terminal-search-session.mjs
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

// Optional env overrides — read once; never log env values (CodeQL clear-text).
const SESSION_ID = (typeof process.env.SI_SESSION_ID === 'string' && process.env.SI_SESSION_ID.trim())
  || 'rollout-2026-08-04T11-21-51-019fcaca-b8cf-7c10-b29a-dd7ff16d6d96'
const QUERY = (typeof process.env.SI_SEARCH_Q === 'string' && process.env.SI_SEARCH_Q.trim())
  || 'resume'

function log(k, v) {
  // Keep probes free of session identifiers and env dumps.
  if (k === 'sessionId' || k === 'SESSION_ID') return
  console.log(`${k}: ${v}`)
}

const browser = await chromium.launch({ headless: true })
const page = await browser.newPage({ viewport: { width: 1400, height: 900 } })

try {
  await page.addInitScript(() => {
    localStorage.setItem('si-locale', 'en')
    // Force HL off so we measure the default fast path
    localStorage.setItem('session-insight-terminal-search-opts', JSON.stringify({
      caseSensitive: false, wholeWord: false, regex: false, highlightAll: false,
    }))
  })

  await page.goto(BASE_URL, { waitUntil: 'domcontentloaded' })
  const filter = page.locator('input[placeholder*="会话"], input[placeholder*="session" i], input[placeholder*="Filter" i], input[placeholder*="过滤"]').first()
  await filter.waitFor({ state: 'visible', timeout: 20_000 })
  await filter.fill(SESSION_ID)
  const row = page.locator(`[data-session-id="${SESSION_ID}"]`)
  await row.waitFor({ state: 'visible', timeout: 20_000 })
  await row.click()
  await page.locator('.xterm-viewport').waitFor({ state: 'visible', timeout: 90_000 })
  // Long codex session render stream
  await page.waitForTimeout(12_000)

  // Dismiss any blocking modal (settings / resume / first-run).
  for (let i = 0; i < 3; i++) {
    const overlay = page.locator('div.fixed.inset-0.z-50')
    if (await overlay.count() === 0) break
    await page.keyboard.press('Escape').catch(() => {})
    await page.waitForTimeout(200)
    // Click a likely close/cancel if still open
    const closeBtn = page.locator('div.fixed.inset-0.z-50 button').filter({ hasText: /Close|关闭|Cancel|取消|✕|Got it|知道了/i }).first()
    if (await closeBtn.count()) await closeBtn.click({ force: true }).catch(() => {})
    await page.waitForTimeout(200)
  }

  await page.locator('.xterm').first().click({ force: true }).catch(() => {})
  await page.keyboard.press('Control+f')
  const bar = page.locator('[data-testid="terminal-search-bar"]')
  await bar.waitFor({ state: 'visible', timeout: 15_000 })

  const input = bar.locator('input')
  await input.click({ force: true })
  await input.fill('')

  // Measure time from last keystroke until counter appears / selection exists
  const t0 = Date.now()
  await input.type(QUERY, { delay: 20 })
  const typedAt = Date.now()

  // Input should stay responsive (value matches) immediately after type
  const value = await input.inputValue()
  log('inputValue', value)
  if (value !== QUERY) throw new Error(`input lag/desync: ${value}`)

  // Wait for any result text or selection
  await page.waitForFunction(() => {
    const bar = document.querySelector('[data-testid="terminal-search-bar"]')
    if (!bar) return false
    const spans = [...bar.querySelectorAll('span')].map(s => s.textContent?.trim() || '')
    return spans.some(t => /\d/.test(t) || /No results|无结果/.test(t))
  }, { timeout: 15_000 })

  const foundAt = Date.now()
  let countText = ''
  const spans = bar.locator('span')
  for (let i = 0; i < await spans.count(); i++) {
    const t = (await spans.nth(i).innerText()).trim()
    if (t) countText = t
  }
  log('queryLen', QUERY.length)
  log('searchResultText', countText)
  log('typeMs', typedAt - t0)
  log('timeToResultMs', foundAt - typedAt)
  log('totalMs', foundAt - t0)

  // Typing another character should not freeze > 1s for input update
  const tKey = Date.now()
  await input.type('x', { delay: 0 })
  const afterKey = await input.inputValue()
  const keyMs = Date.now() - tKey
  log('extraKeyInputMs', keyMs)
  log('afterExtraKey', afterKey)
  if (keyMs > 500) throw new Error(`input blocked ${keyMs}ms on keystroke`)

  // Hard budget: after debounce, first result should land without multi-second freeze
  if (foundAt - typedAt > 4000) {
    throw new Error(`timeToResult too slow: ${foundAt - typedAt}ms`)
  }

  // Step next should be snappy
  const tStep = Date.now()
  await page.keyboard.press('Enter')
  await page.waitForTimeout(100)
  log('stepEnterMs', Date.now() - tStep)

  console.log('PASS')
} finally {
  await browser.close()
}
