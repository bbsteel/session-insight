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
  const englishLanguageSwitch = page.getByRole('button', { name: 'Language', exact: true }).first()
  await englishLanguageSwitch.click()
  await page.keyboard.press('Escape')
  assert.equal(await englishLanguageSwitch.evaluate(element => document.activeElement === element), true)

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
  console.log(`PASS: language choice persists and both locales render (${screenshot})`)
} finally {
  await browser.close()
}
