// Run manually against a full SessionInsight instance with a known Codex
// collaboration root. This intentionally is not part of the headless CI unit
// aggregator because it depends on a locally indexed session and backend.
import assert from 'node:assert/strict'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright'

const appUrl = process.env.SI_VERIFY_APP_URL
const rootSessionId = process.env.SI_VERIFY_ROOT_SESSION_ID
const childAgentId = process.env.SI_VERIFY_CHILD_AGENT_ID
const expectedModelName = process.env.SI_VERIFY_CHILD_MODEL
const screenshotDirectory = fileURLToPath(new URL('../../.runtime/', import.meta.url))

assert.ok(appUrl, 'SI_VERIFY_APP_URL is required')
assert.ok(rootSessionId, 'SI_VERIFY_ROOT_SESSION_ID is required')
assert.ok(childAgentId, 'SI_VERIFY_CHILD_AGENT_ID is required')
assert.ok(expectedModelName, 'SI_VERIFY_CHILD_MODEL is required')

const browser = await chromium.launch({ headless: true })
try {
  for (const [locale, modelLabel, collaborationLabel] of [
    ['en', 'Model', 'Collaboration'],
    ['zh-CN', '模型', '协作'],
  ]) {
    const context = await browser.newContext({ viewport: { width: 1600, height: 900 } })
    const page = await context.newPage()
    const consoleErrors = []
    const failedRequests = []
    page.on('pageerror', error => consoleErrors.push(error.message))
    page.on('requestfailed', request => failedRequests.push(`${request.method()} ${request.url()}: ${request.failure()?.errorText}`))

    await page.addInitScript(selectedLocale => {
      localStorage.setItem('si-locale', selectedLocale)
      localStorage.setItem('si-collab-dock', JSON.stringify({ mode: 'closed', autoFit: true }))
    }, locale)
    await page.goto(`${appUrl}#/session/codex/${encodeURIComponent(rootSessionId)}`, { waitUntil: 'domcontentloaded' })
    const collaborationButton = page.getByTestId('collaboration-entry-button')
    await collaborationButton.waitFor({ state: 'visible', timeout: 20000 })
    assert.equal(await collaborationButton.textContent(), collaborationLabel)
    await collaborationButton.click()
    const collaborationDock = page.getByTestId('collaboration-dock')
    try {
      await page.waitForFunction(() => document.querySelector('[data-testid="collaboration-dock"]')?.getAttribute('data-state') === 'ready', null, { timeout: 20000 })
    } catch (error) {
      console.error(`${locale} dock state: ${await collaborationDock.getAttribute('data-state')}`)
      console.error(`${locale} dock text: ${await collaborationDock.textContent()}`)
      console.error(`${locale} page errors: ${JSON.stringify(consoleErrors)}`)
      console.error(`${locale} request failures: ${JSON.stringify(failedRequests)}`)
      await page.screenshot({ path: join(screenshotDirectory, `collaboration-model-failure-${locale}.png`), fullPage: true })
      throw error
    }
    const childLane = page.locator(`[role="treeitem"][data-invocation$="${childAgentId}"]`)
    await childLane.waitFor({ state: 'visible', timeout: 20000 })
    await childLane.click()
    const invocationDetail = page.getByTestId('collaboration-invocation-detail')
    await invocationDetail.waitFor({ state: 'visible' })
    const modelRow = invocationDetail.locator('dt').filter({ hasText: modelLabel }).locator('..')
    assert.equal(await modelRow.locator('dt').textContent(), modelLabel)
    assert.equal(await modelRow.locator('dd').textContent(), expectedModelName)
    const dockBox = await collaborationDock.boundingBox()
    const detailBox = await invocationDetail.boundingBox()
    assert.ok(dockBox && detailBox && detailBox.width > 0 && detailBox.height > 0)
    assert.ok(detailBox.x >= dockBox.x && detailBox.x + detailBox.width <= dockBox.x + dockBox.width + 1)
    assert.deepEqual(consoleErrors, [], `page errors in ${locale}`)
    assert.deepEqual(failedRequests, [], `failed requests in ${locale}`)
    await page.screenshot({ path: join(screenshotDirectory, `collaboration-model-${locale}.png`), fullPage: true })
    console.log(`${locale}: model ${expectedModelName} visible, detail bounds valid, no page/network errors`)
    await context.close()
  }
} finally {
  await browser.close()
}
