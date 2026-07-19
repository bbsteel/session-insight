// Live validation for the interaction-panel kind icons and custom user avatar:
// - user rows show the default person icon; assistant rows show the agent icon
// - settings → 外观 has a 用户头像 section with upload (≤200KB) and reset
// - oversized upload is rejected with an error; valid upload replaces the icon
// - avatar persists across reloads; 恢复默认 restores the person icon
// Run: node frontend/scripts/validate-avatar-icons.mjs
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
const SHOT_DIR = '/tmp/session-insight-ui'
// Non-live codex session (see validate-interaction-panel.mjs).
const SESSION_NAME = process.env.SI_SESSION_NAME || '如何开启你的yolo模式'

// 1x1 red PNG (~70 bytes) and an oversized (>200KB) PNG payload.
const TINY_PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
  'base64',
)
const BIG_PNG = Buffer.concat([TINY_PNG, Buffer.alloc(210 * 1024)])

let failures = 0
function check(name, cond, detail = '') {
  if (cond) {
    console.log(`PASS: ${name}`)
  } else {
    failures++
    console.error(`FAIL: ${name}${detail ? ` — ${detail}` : ''}`)
  }
}

const panel = page => page.locator('aside:has(button[aria-label="关闭交互消息面板"])')
const userRows = page => panel(page).locator('div[title^="跳转到终端第"]:has(span[title="用户消息"])')

async function openSession(page) {
  await page.goto(BASE_URL)
  await page.waitForSelector('header', { timeout: 15000 })
  await page.locator('input[placeholder*="过滤会话"]').first().fill(SESSION_NAME)
  await page.locator(`text=${SESSION_NAME}`).first().click()
  await page.waitForSelector('button[title="交互消息面板"]', { timeout: 20000 })
  if ((await panel(page).count()) === 0) {
    await page.locator('button[title="交互消息面板"]').click()
  }
  await panel(page).waitFor({ state: 'visible', timeout: 5000 })
  await page.waitForFunction(
    () => document.querySelectorAll('aside div[title^="跳转到终端第"]').length > 0,
    { timeout: 30000 },
  )
}

async function openSettings(page) {
  await page.locator('button[title="设置"]').first().click()
  await page.waitForSelector('[role="dialog"]', { timeout: 5000 })
}

async function run() {
  fs.mkdirSync(SHOT_DIR, { recursive: true })
  const browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const page = await context.newPage()
  const consoleErrors = []
  page.on('console', m => { if (m.type() === 'error') consoleErrors.push(m.text()) })
  page.on('pageerror', e => consoleErrors.push(String(e)))

  try {
    await openSession(page)

    // 1. Default state: user rows show the person SVG (no img avatar);
    //    assistant rows show the agent icon.
    const firstUserRow = userRows(page).first()
    check('user row shows default person icon (svg, no img)',
      (await firstUserRow.locator('svg').count()) > 0 && (await firstUserRow.locator('img[alt="用户头像"]').count()) === 0)
    const firstAsstRow = panel(page).locator('div[title^="跳转到终端第"]:has(span[title="助手回复"])').first()
    check('assistant row shows agent icon',
      (await firstAsstRow.locator('[data-agent-icon-theme]').count()) > 0,
      `agent-icon nodes=${await firstAsstRow.locator('[data-agent-icon-theme]').count()}`)

    // 2. Settings → 外观 exposes the avatar section.
    await openSettings(page)
    check('settings shows 用户头像 section', await page.locator('[role="dialog"] :text-is("用户头像")').isVisible())
    check('reset button hidden without custom avatar',
      (await page.locator('[role="dialog"] button:has-text("恢复默认")').count()) === 0)

    // 3. Oversized upload is rejected with an error message.
    const fileInput = page.locator('[role="dialog"] input[type="file"][aria-label="上传用户头像图像"]')
    await fileInput.setInputFiles({ name: 'big.png', mimeType: 'image/png', buffer: BIG_PNG })
    const alert = page.locator('[role="dialog"] [role="alert"]')
    await alert.waitFor({ state: 'visible', timeout: 5000 })
    const errText = await alert.textContent()
    check('oversized upload rejected with 200KB error', /200KB/.test(errText), errText)
    check('avatar unchanged after rejection',
      (await page.locator('[role="dialog"] img[alt="用户头像"]').count()) === 0)

    // 4. Valid upload: preview becomes an img; reset button appears.
    await fileInput.setInputFiles({ name: 'tiny.png', mimeType: 'image/png', buffer: TINY_PNG })
    await page.locator('[role="dialog"] img[alt="用户头像"]').waitFor({ state: 'visible', timeout: 5000 })
    check('valid upload shows img preview', true)
    check('error cleared after valid upload', !(await alert.isVisible().catch(() => false)))
    check('恢复默认 button appears after upload',
      await page.locator('[role="dialog"] button:has-text("恢复默认")').isVisible())
    await page.screenshot({ path: path.join(SHOT_DIR, 'settings-avatar.png') })
    console.log(`Screenshot saved: ${path.join(SHOT_DIR, 'settings-avatar.png')}`)

    // 5. Panel user rows switch to the uploaded avatar (live, no reload).
    await page.keyboard.press('Escape')
    await userRows(page).first().locator('img[alt="用户头像"]').waitFor({ state: 'visible', timeout: 5000 })
    const avatarImgs = await userRows(page).locator('img[alt="用户头像"]').count()
    const userRowCount = await userRows(page).count()
    check('all user rows show the uploaded avatar', avatarImgs === userRowCount && userRowCount > 0, `${avatarImgs}/${userRowCount}`)
    await page.screenshot({ path: path.join(SHOT_DIR, 'panel-avatars.png') })
    console.log(`Screenshot saved: ${path.join(SHOT_DIR, 'panel-avatars.png')}`)

    // 6. Avatar persists across reload.
    await page.reload()
    await openSession(page)
    check('avatar persists across reload',
      (await userRows(page).first().locator('img[alt="用户头像"]').count()) === 1)

    // 7. 恢复默认 restores the person icon and clears storage.
    await openSettings(page)
    await page.locator('[role="dialog"] button:has-text("恢复默认")').click()
    check('reset clears the stored avatar',
      (await page.evaluate(() => localStorage.getItem('si-user-avatar'))) === null)
    await page.keyboard.press('Escape')
    const userRowAfter = userRows(page).first()
    check('user row back to default person icon after reset',
      (await userRowAfter.locator('svg').count()) > 0 && (await userRowAfter.locator('img[alt="用户头像"]').count()) === 0)

    check('no console errors', consoleErrors.length === 0, consoleErrors.slice(0, 3).join(' | '))
  } catch (err) {
    failures++
    console.error('FATAL:', err.message)
    await page.screenshot({ path: path.join(SHOT_DIR, 'avatar-icons-error.png') }).catch(() => {})
  } finally {
    await browser.close()
  }

  console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECK(S) FAILED`)
  process.exit(failures === 0 ? 0 : 1)
}

run()
