// Live i18n smoke check. Run after ./run.sh all.
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright'

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const baseURL = fs.readFileSync(path.join(repoRoot, '.runtime/session-insight.url'), 'utf8').trim()
const screenshot = path.join(repoRoot, '.runtime/i18n-validation.png')

console.log(`Launching browser for ${baseURL}`)
const browser = await chromium.launch({ headless: true })
const context = await browser.newContext({ viewport: { width: 1280, height: 800 } })
const page = await context.newPage()
const problems = []
page.on('console', message => { if (message.type() === 'error') problems.push(message.text()) })
page.on('pageerror', error => problems.push(String(error)))

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

  await page.locator('button[aria-label="Settings"]').click()
  await page.locator('select[aria-label="Language"]').selectOption('zh-CN')
  await assert.doesNotReject(page.getByPlaceholder(/全文搜索/).waitFor({ state: 'visible' }))
  assert.equal(await page.locator('html').getAttribute('lang'), 'zh-CN')
  assert.equal(await page.locator('#settings-title').textContent(), '外观')
  assert.deepEqual(problems, [])
  await page.screenshot({ path: screenshot })
  console.log(`PASS: language choice persists and both locales render (${screenshot})`)
} finally {
  await browser.close()
}
