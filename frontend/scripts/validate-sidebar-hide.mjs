// Live check: desktop session-list hide/show so the terminal can use the leftover width.
// Run after ./run.sh all: node frontend/scripts/validate-sidebar-hide.mjs
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
const SHOT_DIR = path.join(repoRoot, '.runtime', 'sidebar-hide')
fs.mkdirSync(SHOT_DIR, { recursive: true })

const COPY = {
  en: {
    hide: 'Hide session list',
    hideTitle: 'Hide session list (Ctrl+B)',
    open: 'Open session list',
    openTitle: 'Open session list (Ctrl+B)',
    shortcut: 'Show/hide session list',
  },
  'zh-CN': {
    hide: '隐藏会话列表',
    hideTitle: '隐藏会话列表（Ctrl+B）',
    open: '打开会话列表',
    openTitle: '打开会话列表（Ctrl+B）',
    shortcut: '显示/隐藏会话列表',
  },
}

let failures = 0
function check(name, condition, detail = '') {
  if (condition) console.log(`PASS: ${name}`)
  else {
    failures++
    console.error(`FAIL: ${name}${detail ? ` — ${detail}` : ''}`)
  }
}

async function boxWidth(locator) {
  const box = await locator.boundingBox()
  return box?.width ?? 0
}

async function seedLocale(page, locale, { hidden } = {}) {
  await page.addInitScript(({ loc, hidden }) => {
    localStorage.setItem('si-locale', loc)
    if (hidden) localStorage.setItem('si-sidebar-hidden', '1')
    else localStorage.removeItem('si-sidebar-hidden')
  }, { loc: locale, hidden: Boolean(hidden) })
}

async function waitForApp(page) {
  await page.locator('[data-testid="global-search-input"]').waitFor({ state: 'visible', timeout: 30_000 })
}

async function runLocale(browser, locale) {
  const copy = COPY[locale]
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 } })
  const page = await context.newPage()
  const consoleErrors = []
  page.on('pageerror', error => consoleErrors.push(String(error)))
  await seedLocale(page, locale)
  await page.goto(BASE_URL, { waitUntil: 'domcontentloaded' })
  await waitForApp(page)

  const sidebar = page.locator('[data-testid="session-sidebar"]')
  await sidebar.waitFor({ state: 'visible', timeout: 15_000 })
  const hide = page.locator('[data-testid="sidebar-hide"]')
  await hide.waitFor({ state: 'visible', timeout: 10_000 })

  check(`${locale} hide label`, (await hide.getAttribute('aria-label')) === copy.hide, await hide.getAttribute('aria-label'))
  check(`${locale} hide title`, (await hide.getAttribute('title')) === copy.hideTitle, await hide.getAttribute('title'))
  check(`${locale} rail hidden while sidebar shown`, await page.locator('[data-testid="sidebar-show-rail"]').count() === 0)

  const replay = page.locator('main').first()
  const sidebarWidthBefore = await boxWidth(sidebar)
  const replayWidthBefore = await boxWidth(replay)
  check(`${locale} sidebar has width`, sidebarWidthBefore >= 160, `width=${sidebarWidthBefore}`)

  await hide.click()
  await page.locator('[data-testid="sidebar-show"]').waitFor({ state: 'visible', timeout: 5_000 })
  check(`${locale} sidebar hidden after click`, await sidebar.isHidden())
  const show = page.locator('[data-testid="sidebar-show"]')
  check(`${locale} show label`, (await show.getAttribute('aria-label')) === copy.open, await show.getAttribute('aria-label'))
  check(`${locale} show title`, (await show.getAttribute('title')) === copy.openTitle, await show.getAttribute('title'))
  const replayWidthHidden = await boxWidth(replay)
  check(
    `${locale} replay widened after hide`,
    replayWidthHidden > replayWidthBefore + 100,
    `before=${replayWidthBefore} after=${replayWidthHidden} sidebar=${sidebarWidthBefore}`,
  )

  await page.screenshot({ path: path.join(SHOT_DIR, `hidden-${locale}.png`) })

  await show.click()
  await sidebar.waitFor({ state: 'visible', timeout: 5_000 })
  check(`${locale} sidebar restored`, await sidebar.isVisible())
  check(`${locale} rail gone after restore`, await page.locator('[data-testid="sidebar-show-rail"]').count() === 0)

  await page.keyboard.press('Control+b')
  await page.locator('[data-testid="sidebar-show"]').waitFor({ state: 'visible', timeout: 5_000 })
  check(`${locale} Ctrl+B hides`, await sidebar.isHidden())
  await page.keyboard.press('Control+b')
  await sidebar.waitFor({ state: 'visible', timeout: 5_000 })
  check(`${locale} Ctrl+B restores`, await sidebar.isVisible())

  const sessionRow = page.locator('[data-session-id]').first()
  const hasSessionRow = await sessionRow.waitFor({ state: 'visible', timeout: 60_000 }).then(() => true).catch(() => false)
  const summaries = hasSessionRow
    ? await (await page.request.get(new URL('/api/sessions', BASE_URL).toString())).json()
    : []
  const replayable = Array.isArray(summaries)
    ? summaries.find(s => !s.is_live && (s.turn_count ?? 0) >= 1)
    : null
  if (replayable) {
    const filter = page.locator('input[placeholder="过滤会话…"], input[placeholder="Filter sessions…"]')
    await filter.fill(replayable.id)
    const row = page.locator(`[data-session-id="${replayable.id}"]`)
    await row.waitFor({ state: 'visible', timeout: 15_000 })
    await row.click()
    await page.locator('.xterm-viewport').waitFor({ state: 'visible', timeout: 30_000 })
    await page.waitForTimeout(800)
    const termBefore = await boxWidth(page.locator('.xterm').first())
    await page.locator('[data-testid="sidebar-hide"]').click()
    await page.locator('[data-testid="sidebar-show"]').waitFor({ state: 'visible', timeout: 5_000 })
    await page.waitForTimeout(400)
    const termAfter = await boxWidth(page.locator('.xterm').first())
    check(
      `${locale} terminal widened after hide`,
      termAfter > termBefore + 80,
      `before=${termBefore} after=${termAfter}`,
    )
    await page.screenshot({ path: path.join(SHOT_DIR, `terminal-hidden-${locale}.png`) })
    await page.evaluate(() => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: '?', bubbles: true }))
    })
    const help = page.getByText(copy.shortcut, { exact: true })
    await help.waitFor({ state: 'visible', timeout: 5_000 })
    check(`${locale} help lists sidebar shortcut`, await help.isVisible())
    await page.screenshot({ path: path.join(SHOT_DIR, `help-${locale}.png`) })
  } else {
    console.log(`SKIP: ${locale} terminal/help — no recorded session with turns`)
  }

  check(`${locale} no page errors`, consoleErrors.length === 0, consoleErrors.join(' | '))
  await context.close()
}

async function runPersist(browser) {
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 } })
  const page = await context.newPage()
  await seedLocale(page, 'en', { hidden: true })
  await page.goto(BASE_URL, { waitUntil: 'domcontentloaded' })
  await waitForApp(page)
  await page.locator('[data-testid="sidebar-show"]').waitFor({ state: 'visible', timeout: 15_000 })
  check('persisted hidden shows rail', await page.locator('[data-testid="session-sidebar"]').isHidden())
  await context.close()
}

async function run() {
  console.log(`Navigating to ${BASE_URL}`)
  const browser = await chromium.launch({ headless: true })
  try {
    await runLocale(browser, 'en')
    await runLocale(browser, 'zh-CN')
    await runPersist(browser)
  } finally {
    await browser.close()
  }
  if (failures > 0) {
    console.error(`\n${failures} check(s) failed`)
    process.exit(1)
  }
  console.log('\nAll sidebar hide checks passed')
  console.log(`Screenshots: ${SHOT_DIR}`)
}

run().catch(err => {
  console.error(err)
  process.exit(1)
})
