// Live regression check: node frontend/scripts/check-home-review.mjs <Ready URL>
// Requires the complete app; only home reports are replaced to control response order.
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
    const realReport = await context.request.get('/api/home?days=7', { timeout: 120000 })
    assert.equal(realReport.status(), 200, 'Live backend home endpoint works')
    const report = await realReport.json()
    const requests = []
    await page.route('**/api/home?*', route => { requests.push(route) })
    await page.goto('/')
    await page.getByTestId('session-home').waitFor()
    await waitForRequests(1)
    const days30 = page.getByRole('button', { name: locale === 'en' ? '30 days' : '30 天', exact: true })
    const days7 = page.getByRole('button', { name: locale === 'en' ? '7 days' : '7 天', exact: true })
    await days30.click()
    await waitForRequests(2)
    assert.equal(new URL(requests[1].request().url()).searchParams.get('days'), '30')
    await fulfillReport(requests[1], 'Current window', 321)
    await page.getByTestId('home-summary').getByText('321', { exact: true }).waitFor()
    await fulfillReport(requests[0], 'Superseded window', 987)
    // A render round after delivery catches a late overwrite without a fixed sleep.
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
    assert.equal(await page.getByText('Superseded window', { exact: true }).count(), 0)
    assert.equal(await page.getByTestId('home-summary').getByText('321', { exact: true }).count(), 1)
    const tooltip = locale === 'en'
      ? '2 file changes have unavailable line counts; line totals are partial'
      : '2 条文件改动缺少行数统计；行数合计不完整'
    const coverageMark = page.getByLabel(tooltip, { exact: true })
    await coverageMark.waitFor()
    assert.equal(await coverageMark.getAttribute('title'), tooltip)
    await days7.click()
    await waitForRequests(3)
    assert.equal(await page.getByTestId('home-summary').count(), 0, 'Old report cleared while new query loads')
    await requests[2].fulfill({ status: 500, body: 'test failure' })
    const retry = page.getByRole('button', { name: locale === 'en' ? 'Retry' : '重试', exact: true })
    await retry.waitFor()
    await retry.click()
    await waitForRequests(4)
    await fulfillReport(requests[3], 'Recovered window', 654)
    await page.getByTestId('home-summary').getByText('654', { exact: true }).waitFor()
    assert.equal(await retry.count(), 0)
    assert.deepEqual(pageErrors, [])
    console.log(`${locale}: superseded response, loading, missing-line tooltip, failure and retry passed`)
    await context.close()

    async function waitForRequests(count) {
      await page.waitForFunction(() => true)
      const deadline = Date.now() + 10000
      while (requests.length < count && Date.now() < deadline) await new Promise(resolve => setTimeout(resolve, 20))
      assert.equal(requests.length, count, 'Expected home request count')
    }
    async function fulfillReport(route, sessionName, sessions) {
      const fixture = structuredClone(report)
      fixture.summary.sessions = sessions
      fixture.coverage.untimed_code_changes = 0
      fixture.coverage.missing_code_line_changes = 2
      fixture.recent = [{ id: sessionName, name: sessionName, agent_type: 'grok', updated_at: new Date().toISOString() }]
      await route.fulfill({ json: fixture })
    }
  }
} finally {
  await browser.close()
}
