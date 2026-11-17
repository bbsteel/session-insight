// Live check: node frontend/scripts/check-home-navigation.mjs <Ready URL>
import assert from 'node:assert/strict'
import { chromium } from 'playwright'

const baseURL = process.argv[2]
assert.ok(baseURL, 'Pass the Ready URL from ./run.sh all')
const browser = await chromium.launch({ headless: true })
try {
  for (const locale of ['en', 'zh-CN']) {
    const context = await browser.newContext({ baseURL, locale, viewport: { width: 1440, height: 1000 } })
    await context.addInitScript(locale => localStorage.setItem('si-locale', locale), locale)
    const page = await context.newPage()
    const pageErrors = []
    page.on('pageerror', error => pageErrors.push(error.message))
    const backendReport = await context.request.get('/api/home?days=7', { timeout: 120000 })
    assert.equal(backendReport.status(), 200, 'Full backend home endpoint works')
    const homeReport = await backendReport.json()
    await page.route('**/api/home?*', route => route.fulfill({ json: homeReport }))
    await page.route('**/api/sessions/navigation-fixture', route => route.fulfill({ json: {
      id: 'navigation-fixture', agent_type: 'codex', name: 'Navigation fixture',
      turns: [], token_usage: {}, anomaly_summary: {},
    } }))
    await page.goto('/?navigation-review=before')
    await page.getByTestId('session-home').waitFor()
    await page.goto('/?navigation-review=session#/session/codex/navigation-fixture')
    const overview = page.getByTestId('overview-button')
    await overview.waitFor()
    assert.equal(await overview.innerText(), locale === 'en' ? 'Overview' : '概览')
    const historyLength = await page.evaluate(() => history.length)
    await overview.click()
    await page.getByTestId('session-home').waitFor()
    assert.equal(new URL(page.url()).hash, '')
    assert.equal(new URL(page.url()).search, '?navigation-review=session')
    assert.equal(await page.evaluate(() => history.length), historyLength, 'Overview replaces the deep-link history entry')
    await page.getByTestId('global-snippets').click()
    await page.getByTestId('snippets-overlay').waitFor()
    const snippetsBack = page.getByRole('button', { name: locale === 'en' ? 'Back to sessions' : '返回会话', exact: true })
    await snippetsBack.click()
    await page.getByTestId('snippets-overlay').waitFor({ state: 'detached' })
    assert.equal(new URL(page.url()).hash, '', 'Snippets return to home rather than the stale session hash')
    await page.reload()
    await page.getByTestId('home-summary').waitFor()
    assert.equal(await overview.count(), 0, 'Reload keeps the home view')
    // Re-enter a deep link and check that Back skips its replaced entry.
    await page.goto('/?navigation-review=before-back')
    await page.getByTestId('session-home').waitFor()
    await page.goto('/?navigation-review=session-back#/session/codex/navigation-fixture')
    await overview.waitFor()
    await overview.click()
    await page.getByTestId('session-home').waitFor()
    await page.goBack()
    await page.waitForURL('**/?navigation-review=before-back')
    await page.getByTestId('session-home').waitFor()
    await page.goForward()
    await page.waitForURL('**/?navigation-review=session-back')
    await page.getByTestId('session-home').waitFor()
    assert.deepEqual(pageErrors, [])
    console.log(`${locale}: Overview label, history replacement, snippets return, reload, Back and Forward passed`)
    await context.close()
  }
} finally {
  await browser.close()
}
