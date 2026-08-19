// Live regression check for the shared navigation-panel filter capsules.
// Run from this worktree after ./run.sh all:
//   node frontend/scripts/validate-navigation-filter-pills.mjs
import { chromium } from 'playwright'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const baseUrl = process.env.SI_VERIFY_URL
  ?? fs.readFileSync(path.join(repoRoot, '.runtime/session-insight.url'), 'utf8').trim()
const screenshotDir = '/tmp/session-insight-ui'

const locales = [
  {
    code: 'zh-CN',
    sessionFilter: '过滤会话',
    messagesTitle: '交互消息',
    toolsTitle: '工具调用',
    outlineTitle: '关键事件',
    selectAll: '全选',
    selectNone: '全不选',
    noMessages: '请选择要显示的消息类型',
    noOutline: '所有事件类别均已关闭',
    noTools: '没有匹配当前筛选的调用',
  },
  {
    code: 'en',
    sessionFilter: 'Filter sessions',
    messagesTitle: 'Messages',
    toolsTitle: 'Tool calls',
    outlineTitle: 'Key events',
    selectAll: 'Select all',
    selectNone: 'Select none',
    noMessages: 'Select the message types to show above',
    noOutline: 'All event categories are off',
    noTools: 'No calls match the current filter',
  },
]

let failures = 0
function check(name, condition, detail = '') {
  if (condition) console.log('PASS: ' + name)
  else {
    failures++
    console.error('FAIL: ' + name + (detail ? ' — ' + detail : ''))
  }
}

async function chooseSession(page) {
  const summaries = await (await page.request.get(new URL('/api/sessions', baseUrl).toString())).json()
  for (const summary of summaries.filter(item => !item.is_live).slice(0, 120)) {
    const response = await page.request.get(new URL('/api/sessions/' + summary.id + '/positions', baseUrl).toString())
    if (!response.ok()) continue
    const positions = (await response.json()).positions ?? []
    const counts = positions.reduce((result, position) => {
      result[position.kind] = (result[position.kind] ?? 0) + 1
      return result
    }, {})
    if (counts.user > 0 && counts.assistant > 0 && counts.tool > 0 && counts.outline > 0) return summary.id
  }
  throw new Error('No recorded session with messages, tools, and key events was found')
}

async function openSession(page, spec, sessionId) {
  await page.goto(baseUrl, { waitUntil: 'domcontentloaded' })
  const sessionFilter = page.locator('input[placeholder*="' + spec.sessionFilter + '"]').first()
  await sessionFilter.waitFor({ state: 'visible', timeout: 15_000 })
  await sessionFilter.fill(sessionId)
  await page.locator('[data-session-id="' + sessionId + '"]').waitFor({ state: 'visible', timeout: 15_000 })
  await page.locator('[data-session-id="' + sessionId + '"]').click()
  await page.locator('.xterm-viewport').waitFor({ state: 'visible', timeout: 30_000 })
  await page.waitForTimeout(1_000)
}

function panelFor(page, title) {
  return page.locator('aside[data-testid="navigation-panel"]').filter({ hasText: title }).last()
}

async function openPanel(page, title) {
  const panel = panelFor(page, title)
  if (await panel.count()) return panel
  const openButton = page.locator('button[title="' + title + '"]').first()
  await openButton.waitFor({ state: 'visible', timeout: 15_000 })
  await openButton.click()
  await panel.waitFor({ state: 'visible', timeout: 15_000 })
  return panel
}

async function closePanel(panel) {
  await panel.locator(':scope > div').first().locator('button').last().click()
  await panel.waitFor({ state: 'detached', timeout: 5_000 })
}

async function assertCapsuleGeometry(panel, label) {
  const geometry = await panel.locator('[data-testid="navigation-filter-pill"]').evaluateAll(elements =>
    elements.map(element => {
      const box = element.getBoundingClientRect()
      return { height: box.height, width: box.width }
    }),
  )
  const heights = [...new Set(geometry.map(item => item.height))]
  check(label + ' filter capsules share one height', geometry.length > 0 && heights.length === 1 && heights[0] === 24, JSON.stringify(geometry))
}

async function checkLocale(page, spec, sessionId) {
  await page.addInitScript(locale => localStorage.setItem('si-locale', locale), spec.code)
  await page.goto(baseUrl, { waitUntil: 'domcontentloaded' })
  await page.evaluate(() => {
    localStorage.removeItem('si-interaction-kinds')
    localStorage.removeItem('si-outline-categories')
  })
  await openSession(page, spec, sessionId)

  const messages = await openPanel(page, spec.messagesTitle)
  check(spec.code + ' interaction panel has no native checkboxes', await messages.locator('input[type="checkbox"]').count() === 0)
  check(spec.code + ' interaction panel has selection actions', await messages.getByTestId('navigation-filter-select-all').count() === 1 && await messages.getByTestId('navigation-filter-select-none').count() === 1)
  await assertCapsuleGeometry(messages, spec.code + ' interaction')
  const messageRows = messages.locator('[title^="跳转到终端第"], [title^="Jump to terminal line"]')
  const messageCount = await messageRows.count()
  await messages.getByTestId('navigation-filter-select-none').click()
  check(spec.code + ' interaction select-none is active', await messages.getByTestId('navigation-filter-select-none').getAttribute('aria-pressed') === 'true')
  check(spec.code + ' interaction select-none hides rows', await messageRows.count() === 0 && await messages.getByText(spec.noMessages, { exact: true }).isVisible())
  await messages.getByTestId('navigation-filter-select-all').click()
  check(spec.code + ' interaction select-all restores rows', await messages.getByTestId('navigation-filter-select-all').getAttribute('aria-pressed') === 'true' && await messageRows.count() === messageCount)
  await page.screenshot({ path: path.join(screenshotDir, 'navigation-filter-pills-' + spec.code + '-messages.png'), fullPage: true })
  await closePanel(messages)

  const tools = await openPanel(page, spec.toolsTitle)
  check(spec.code + ' tool panel has selection actions', await tools.getByTestId('navigation-filter-select-all').count() === 1 && await tools.getByTestId('navigation-filter-select-none').count() === 1)
  await assertCapsuleGeometry(tools, spec.code + ' tool')
  const toolRows = tools.locator('[title^="跳转到终端第"], [title^="Jump to terminal line"]')
  const toolCount = await toolRows.count()
  await tools.getByTestId('navigation-filter-select-none').click()
  check(spec.code + ' tool select-none is active', await tools.getByTestId('navigation-filter-select-none').getAttribute('aria-pressed') === 'true')
  check(spec.code + ' tool select-none hides rows', await toolRows.count() === 0 && await tools.getByText(spec.noTools, { exact: true }).isVisible())
  await tools.getByTestId('navigation-filter-select-all').click()
  check(spec.code + ' tool select-all restores rows', await tools.getByTestId('navigation-filter-select-all').getAttribute('aria-pressed') === 'true' && await toolRows.count() === toolCount)
  await page.screenshot({ path: path.join(screenshotDir, 'navigation-filter-pills-' + spec.code + '-tools.png'), fullPage: true })
  await closePanel(tools)

  const outline = await openPanel(page, spec.outlineTitle)
  check(spec.code + ' key-event panel has no native checkboxes', await outline.locator('input[type="checkbox"]').count() === 0)
  check(spec.code + ' key-event panel has selection actions', await outline.getByTestId('navigation-filter-select-all').count() === 1 && await outline.getByTestId('navigation-filter-select-none').count() === 1)
  await assertCapsuleGeometry(outline, spec.code + ' key-event')
  const outlineRows = outline.locator('[data-outline-key]')
  const outlineCount = await outlineRows.count()
  await outline.getByTestId('navigation-filter-select-none').click()
  check(spec.code + ' key-event select-none is active', await outline.getByTestId('navigation-filter-select-none').getAttribute('aria-pressed') === 'true')
  check(spec.code + ' key-event select-none hides rows', await outlineRows.count() === 0 && await outline.getByText(spec.noOutline, { exact: true }).isVisible())
  await outline.getByTestId('navigation-filter-select-all').click()
  check(spec.code + ' key-event select-all restores rows', await outline.getByTestId('navigation-filter-select-all').getAttribute('aria-pressed') === 'true' && await outlineRows.count() === outlineCount)
  await page.screenshot({ path: path.join(screenshotDir, 'navigation-filter-pills-' + spec.code + '.png'), fullPage: true })
  await closePanel(outline)
}

async function run() {
  fs.mkdirSync(screenshotDir, { recursive: true })
  const browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const page = await context.newPage()
  const consoleErrors = []
  page.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()) })
  page.on('pageerror', error => consoleErrors.push(String(error)))

  try {
    const sessionId = process.env.SI_SESSION_ID ?? await chooseSession(page)
    console.log('Using session ' + sessionId)
    for (const spec of locales) {
      await checkLocale(page, spec, sessionId)
    }
    check('no browser console errors', consoleErrors.length === 0, consoleErrors.slice(0, 3).join(' | '))
  } catch (error) {
    failures++
    console.error('FATAL: ' + error.message)
  } finally {
    await browser.close()
  }

  console.log(failures === 0 ? '\nALL CHECKS PASSED' : '\n' + failures + ' CHECK(S) FAILED')
  process.exit(failures === 0 ? 0 : 1)
}

await run()
