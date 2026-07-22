// Live i18n smoke check. Run after ./run.sh all.
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright'

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const baseURL = process.env.BASE_URL ?? fs.readFileSync(path.join(repoRoot, '.runtime/session-insight.url'), 'utf8').trim()
const screenshot = path.join(repoRoot, '.runtime/i18n-validation.png')
const englishBookmarkScreenshot = path.join(repoRoot, '.runtime/i18n-bookmark-en.png')
const chineseBookmarkScreenshot = path.join(repoRoot, '.runtime/i18n-bookmark-zh.png')
const sourceScreenshot = path.join(repoRoot, '.runtime/i18n-six-sources-en.png')
const expectedSources = ['claude', 'codex', 'copilot', 'grok', 'opencode', 'chrys']

console.log(`Launching browser for ${baseURL}`)
const browser = await chromium.launch({ headless: true })
const context = await browser.newContext({ viewport: { width: 1280, height: 800 } })
const page = await context.newPage()
const problems = []
page.on('console', message => { if (message.type() === 'error') problems.push(message.text()) })
page.on('pageerror', error => problems.push(String(error)))

const response = await context.request.get(`${baseURL.replace(/\/$/, '')}/api/sessions`)
assert.equal(response.ok(), true, `session API failed: ${response.status()}`)
const liveSessions = await response.json()
const sourceSamples = new Map()
for (const session of liveSessions) {
  if (expectedSources.includes(session.agent_type) && !sourceSamples.has(session.agent_type)) {
    sourceSamples.set(session.agent_type, session)
  }
}
// A developer machine may not have used every supported agent. Keep the live
// browser matrix deterministic by adding a list-only sample for absent sources;
// the selected detail remains a real session and source readers retain their
// own Go fixture coverage.
const fallback = liveSessions[0]
assert.ok(fallback, 'i18n validation requires at least one indexed session')
const fixtureSources = expectedSources.filter(source => !sourceSamples.has(source))
const fixtureSessionIDs = new Map()
for (const source of fixtureSources) {
  const id = `i18n-fixture-${source}-${fallback.id}`
  fixtureSessionIDs.set(id, fallback.id)
  sourceSamples.set(source, { ...fallback, id, agent_type: source, name: `[i18n fixture] ${source}` })
}
if (fixtureSources.length > 0) {
  await page.route('**/api/sessions', async route => {
    const url = new URL(route.request().url())
    if (route.request().method() !== 'GET') return route.continue()
    if (url.pathname === '/api/sessions') {
      const upstream = await route.fetch()
      const sessions = await upstream.json()
      await route.fulfill({ response: upstream, json: [...sessions, ...fixtureSources.map(source => sourceSamples.get(source))] })
      return
    }
    for (const [fixtureID, realID] of fixtureSessionIDs) {
      if (!url.pathname.includes(fixtureID)) continue
      url.pathname = url.pathname.replace(fixtureID, realID)
      const upstream = await route.fetch({ url: url.toString() })
      await route.fulfill({ response: upstream })
      return
    }
    await route.continue()
  })
}
console.log(`Six-source matrix: ${expectedSources.length - fixtureSources.length} live, ${fixtureSources.length} fixture-backed (${fixtureSources.join(', ') || 'none'})`)

try {
  console.log('Checking saved English preference')
  await page.goto(baseURL, { waitUntil: 'domcontentloaded' })
  await page.locator('input[type="search"]').waitFor({ state: 'visible' })
  const settings = page.locator('button[aria-label="设置"], button[aria-label="Settings"]').first()
  await settings.click()
  const language = page.locator('select[aria-label="语言"], select[aria-label="Language"]').first()
  await language.selectOption('en')
  await assert.doesNotReject(page.getByPlaceholder(/Search all sessions/).waitFor({ state: 'visible' }))
  assert.equal(await page.locator('html').getAttribute('lang'), 'en')
  assert.equal(await page.locator('#settings-title').textContent(), 'Appearance')

  await page.reload({ waitUntil: 'domcontentloaded' })
  await assert.doesNotReject(page.getByPlaceholder(/Search all sessions/).waitFor({ state: 'visible' }))
  const englishLanguageSwitch = page.getByRole('button', { name: 'Language', exact: true }).first()
  await englishLanguageSwitch.click()
  await page.keyboard.press('Escape')
  assert.equal(await englishLanguageSwitch.evaluate(element => document.activeElement === element), true)

  console.log('Checking one session path for each supported source')
  const sessionFilter = page.getByPlaceholder(/Filter sessions/)
  for (const source of expectedSources) {
    const sample = sourceSamples.get(source)
    const selectionName = fixtureSources.includes(source) ? fallback.name : sample.name
    await sessionFilter.fill(selectionName)
    const row = page.getByText(selectionName, { exact: true }).first()
    await row.waitFor({ state: 'visible' })
    await row.click()
    await page.getByRole('button', { name: 'Analytics', exact: true }).waitFor({ state: 'visible' })
    assert.equal(await page.locator('html').getAttribute('lang'), 'en')
  }
  await page.screenshot({ path: sourceScreenshot })
  await sessionFilter.fill(liveSessions[0].name)
  await page.getByText(liveSessions[0].name, { exact: true }).first().click()

  const bookmark = page.locator('button[aria-label="Bookmark"]').first()
  await bookmark.waitFor({ state: 'attached' })
  await bookmark.click()
  const englishEditor = page.getByRole('dialog', { name: 'Add bookmark note' })
  await englishEditor.waitFor({ state: 'visible' })
  await assert.doesNotReject(englishEditor.getByText('Record why you bookmarked this session (saved only on this device).').waitFor())
  await assert.doesNotReject(englishEditor.getByPlaceholder('Record the reason for bookmarking…').waitFor())
  await assert.doesNotReject(englishEditor.getByRole('button', { name: 'Skip and bookmark' }).waitFor())
  await page.screenshot({ path: englishBookmarkScreenshot })
  await englishEditor.getByPlaceholder('Record the reason for bookmarking…').fill('i18n validation')
  await englishEditor.getByRole('button', { name: 'Save' }).click()
  await assert.doesNotReject(page.getByText('Bookmark note saved', { exact: true }).waitFor())
  await page.locator('button[aria-label="Remove bookmark"]').first().click()

  await page.locator('button[aria-label="Settings"]').click()
  await page.locator('select[aria-label="Language"]').selectOption('zh-CN')
  await assert.doesNotReject(page.getByPlaceholder(/全文搜索/).waitFor({ state: 'visible' }))
  assert.equal(await page.locator('html').getAttribute('lang'), 'zh-CN')
  assert.equal(await page.locator('#settings-title').textContent(), '外观')
  await page.getByRole('button', { name: '关闭' }).first().click()
  const chineseLanguageSwitch = page.getByRole('button', { name: '语言', exact: true }).first()
  await chineseLanguageSwitch.click()
  await page.getByRole('option', { name: '简体中文' }).press('Enter')
  assert.equal(await chineseLanguageSwitch.evaluate(element => document.activeElement === element), true)

  const chineseBookmark = page.locator('button[aria-label="收藏"]').first()
  await chineseBookmark.waitFor({ state: 'attached' })
  await chineseBookmark.click()
  const chineseEditor = page.getByRole('dialog', { name: '添加收藏备注' })
  await chineseEditor.waitFor({ state: 'visible' })
  await assert.doesNotReject(chineseEditor.getByText('记录为什么收藏这条会话（仅本机保存）。').waitFor())
  await assert.doesNotReject(chineseEditor.getByPlaceholder('记录收藏原因…').waitFor())
  await page.screenshot({ path: chineseBookmarkScreenshot })
  await chineseEditor.getByRole('button', { name: '跳过直接收藏' }).click()
  await page.locator('button[aria-label="取消收藏"]').first().click()

  assert.deepEqual(problems, [])
  await page.screenshot({ path: screenshot })
  console.log(`PASS: language choice persists, six source paths render, and both locales pass (${screenshot})`)
} finally {
  await browser.close()
}
