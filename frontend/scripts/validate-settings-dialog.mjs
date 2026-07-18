import { chromium } from 'playwright'
import fs from 'node:fs'
import path from 'node:path'

const BASE_URL = (() => {
  const urlPath = path.resolve(process.cwd(), '../session-insight.url')
  try {
    return fs.readFileSync(urlPath, 'utf8').trim()
  } catch {
    return 'http://127.0.0.1:8080/'
  }
})()
const TABS = ['外观', '会话导航', '全文搜索', '终端外观', '文件查看器', 'AI']

async function waitForHeader(page) {
  await page.waitForSelector('header', { timeout: 10000 })
}

async function openSettings(page) {
  const button = page.locator('button[title="设置"]').first()
  await button.click()
  await page.waitForSelector('[role="dialog"]', { timeout: 5000 })
}

async function assertTabContent(page, tabLabel) {
  const title = page.locator('#settings-title').first()
  await title.waitFor({ state: 'visible' })
  const text = await title.textContent()
  if (text.trim() !== tabLabel) {
    throw new Error(`Expected tab title "${tabLabel}", got "${text.trim()}"`)
  }
}

async function screenshot(page, path) {
  await page.screenshot({ path, fullPage: false })
  console.log(`Screenshot saved: ${path}`)
}

async function run() {
  const browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 } })
  const page = await context.newPage()

  try {
    console.log(`Navigating to ${BASE_URL}`)
    await page.goto(BASE_URL)
    await waitForHeader(page)
    console.log('App loaded')

    // Open the settings dialog and verify all tabs exist.
    await openSettings(page)
    console.log('Settings dialog opened')

    for (const label of TABS) {
      const tab = page.locator(`[role="dialog"] nav button:has-text("${label}")`).first()
      const visible = await tab.isVisible()
      if (!visible) throw new Error(`Tab "${label}" not visible`)
      console.log(`Tab found: ${label}`)
    }

    // Click through each tab and verify the title updates.
    for (const label of TABS) {
      const tab = page.locator(`[role="dialog"] nav button:has-text("${label}")`).first()
      await tab.click()
      await assertTabContent(page, label)
      console.log(`Tab "${label}" content verified`)
    }

    // Switch to dark theme via the dialog's theme select and capture.
    const appearanceTab = page.locator('[role="dialog"] nav button:has-text("外观")').first()
    await appearanceTab.click()
    const themeSelect = page.locator('[role="dialog"] select').first()
    await themeSelect.selectOption('dark')
    await page.waitForTimeout(300)
    await screenshot(page, '/tmp/session-insight-ui/settings-dark.png')

    // Switch to light theme and capture again.
    await themeSelect.selectOption('light')
    await page.waitForTimeout(300)
    await screenshot(page, '/tmp/session-insight-ui/settings-light.png')

    // Verify AI model source modal closes back to the settings dialog.
    const aiTab = page.locator('[role="dialog"] nav button:has-text("AI")').first()
    await aiTab.click()
    const manageAiBtn = page.locator('button:has-text("管理 AI 模型源")').first()
    await manageAiBtn.click()
    // Wait for AISettingsModal.
    await page.waitForSelector('.fixed.inset-0', { timeout: 5000 })
    const aiCloseBtn = page.locator('button:has-text("✕"), button[aria-label="关闭"]').first()
    await aiCloseBtn.click()
    // The settings dialog should remain/become visible again.
    const settingsDialog = page.locator('[role="dialog"]').first()
    if (!(await settingsDialog.isVisible())) {
      throw new Error('Settings dialog did not reappear after closing AI settings modal')
    }
    console.log('AI settings modal close-to-return verified')

    // Close the dialog and verify it disappears.
    const closeBtn = page.locator('[role="dialog"] button[aria-label="关闭"]').first()
    await closeBtn.click()
    await page.waitForSelector('[role="dialog"]', { state: 'hidden', timeout: 5000 })
    console.log('Settings dialog closed successfully')

    console.log('All assertions passed')
  } catch (err) {
    console.error('Validation failed:', err.message)
    await screenshot(page, '/tmp/session-insight-ui/settings-failure.png')
    process.exitCode = 1
  } finally {
    await browser.close()
  }
}

await run()
