import assert from 'node:assert/strict'
import { chromium, request } from 'playwright'

const appUrl = process.env.SI_APP_URL
if (!appUrl) throw new Error('Set SI_APP_URL to the Ready URL from ./run.sh all')

const now = Date.now()
const sessionAt = (daysAgo, id, name, project, agent, extras = {}) => ({
  id,
  agent_type: agent,
  name,
  project,
  repository: project,
  branch: 'main',
  cwd: `/workspace/${project}`,
  model_name: '',
  turn_count: 2,
  message_count: 4,
  is_live: false,
  bookmarked: false,
  created_at: new Date(now - (daysAgo + 1) * 86_400_000).toISOString(),
  updated_at: new Date(now - daysAgo * 86_400_000).toISOString(),
  ...extras,
})

const sessions = [
  sessionAt(0, 'home-claude', 'Investigate parser', 'alpha', 'claude', { is_live: true, bookmarked: true }),
  sessionAt(1, 'home-codex', 'Fix navigation', 'alpha', 'codex'),
  sessionAt(15, 'home-opencode', 'Review release', 'beta', 'opencode'),
]

const api = await request.newContext()
for (const endpoint of ['/api/version', '/api/sessions', '/api/agents']) {
  const response = await api.get(new URL(endpoint, appUrl).toString())
  assert.equal(response.status(), 200, `${endpoint} must be served by the full app`)
}
await api.dispose()

const browser = await chromium.launch({ headless: true })
try {
  for (const locale of ['en', 'zh-CN']) {
    const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
    const pageErrors = []
    let mockMode = 'populated'
    page.on('pageerror', error => pageErrors.push(error.message))
    await page.addInitScript(selectedLocale => localStorage.setItem('si-locale', selectedLocale), locale)
    await page.route('**/api/sessions?*', route => mockMode === 'error'
      ? route.fulfill({ status: 500, json: { error: 'synthetic failure' } })
      : route.fulfill({ json: mockMode === 'empty' ? [] : sessions }))
    await page.goto(appUrl, { waitUntil: 'domcontentloaded' })

    const expectedTitle = locale === 'en' ? 'Workspace overview' : '工作区概览'
    await page.getByRole('heading', { name: expectedTitle }).waitFor()
    await page.locator('[data-testid="home-session-row"]').first().waitFor()
    assert.equal(await page.locator('[data-testid="home-session-row"]').count(), 2)
    assert.equal(await page.locator('[data-testid="home-activity-chart"] > div').count(), 7)
    assert.equal(await page.locator('[data-testid="home-summary"] > div').count(), 4)
    assert.equal(await page.locator('[data-testid="home-projects"] button').count(), 1)
    const summaryText = await page.locator('[data-testid="home-summary"]').innerText()
    assert.match(summaryText, locale === 'en' ? /Updated sessions\s*2/ : /更新的会话\s*2/)
    assert.match(summaryText, locale === 'en' ? /Live sessions\s*1/ : /活跃会话\s*1/)

    await page.getByRole('button', { name: locale === 'en' ? 'Last 30 days' : '近 30 天' }).click()
    assert.equal(await page.locator('[data-testid="home-session-row"]').count(), 3)
    assert.equal(await page.locator('[data-testid="home-activity-chart"] > div').count(), 30)
    assert.equal(await page.locator('[data-testid="home-projects"] button').count(), 2)
    assert.match(await page.locator('[data-testid="home-summary"]').innerText(), locale === 'en' ? /Updated sessions\s*3/ : /更新的会话\s*3/)

    const screenshotDirectory = process.env.SI_SCREENSHOT_DIR
    if (screenshotDirectory) await page.screenshot({ path: `${screenshotDirectory}/home-${locale}.png`, fullPage: true })

    await page.locator('[data-testid="home-session-row"]').first().click()
    await page.locator('[data-testid="home-dashboard"]').waitFor({ state: 'hidden' })
    await page.locator('[data-testid="sidebar-home"]').click()
    await page.getByRole('heading', { name: expectedTitle }).waitFor()

    mockMode = 'empty'
    await page.reload({ waitUntil: 'domcontentloaded' })
    await page.getByRole('heading', { name: locale === 'en' ? 'No indexed sessions yet' : '还没有已索引的会话' }).waitFor()

    mockMode = 'error'
    await page.reload({ waitUntil: 'domcontentloaded' })
    await page.getByRole('alert').filter({ hasText: locale === 'en' ? 'Could not load the session overview.' : '无法加载会话概览。' }).waitFor()
    mockMode = 'populated'
    await page.getByRole('button', { name: locale === 'en' ? 'Retry' : '重试' }).click()
    await page.locator('[data-testid="home-session-row"]').first().waitFor()
    assert.deepEqual(pageErrors, [], `${locale} should not throw browser errors`)
    await page.close()
  }
  console.log('Home dashboard live app checks passed in en and zh-CN')
} finally {
  await browser.close()
}
