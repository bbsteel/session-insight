// Live validation for the 交互消息 (interaction messages) panel:
// - header button renamed with combined user+assistant count
// - two kind checkboxes, both checked by default, individually toggleable
// - user/assistant rows use different background colors
// - assistant reply text is truncated (backend cap 400 runes + "…")
// - checkbox state persists across reloads
// Run: node frontend/scripts/validate-interaction-panel.mjs
// Uses a non-live session so position counts stay stable during the run.
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
// Non-live codex session "如何开启你的yolo模式": 5 user + 55 assistant.
const SESSION_NAME = process.env.SI_SESSION_NAME || '如何开启你的yolo模式'

let failures = 0
function check(name, cond, detail = '') {
  if (cond) {
    console.log(`PASS: ${name}`)
  } else {
    failures++
    console.error(`FAIL: ${name}${detail ? ` — ${detail}` : ''}`)
  }
}

// The interaction panel aside, distinguished from the session-list sidebar by
// its close button.
const panel = page => page.locator('aside:has(button[aria-label="关闭交互消息面板"])')
const panelRows = page => panel(page).locator('div[title^="跳转到终端第"]')
const kindCheckbox = (page, label) => panel(page).locator(`input[aria-label="显示${label}"]`)

async function chipCounts(page) {
  const user = await panel(page).locator('div[title^="跳转到终端第"]:has(span[title="用户消息"])').count()
  const assistant = await panel(page).locator('div[title^="跳转到终端第"]:has(span[title="助手回复"])').count()
  return { user, assistant }
}

async function openSession(page) {
  await page.goto(BASE_URL)
  await page.waitForSelector('header', { timeout: 15000 })
  // The sidebar is virtualized; filter by name so the target row renders.
  await page.locator('input[placeholder*="过滤会话"]').first().fill(SESSION_NAME)
  await page.locator(`text=${SESSION_NAME}`).first().click()
  await page.waitForSelector('button[title="交互消息面板"]', { timeout: 20000 })
  // Positions load async; wait until the header button shows a count.
  await page.waitForFunction(
    () => /^交互消息 \d+$/.test(document.querySelector('button[title="交互消息面板"]')?.textContent?.trim() ?? ''),
    { timeout: 30000 },
  )
  // Default nav pref auto-opens the panel pinned; open it manually otherwise.
  if ((await panel(page).count()) === 0) {
    await page.locator('button[title="交互消息面板"]').click()
  }
  await panel(page).waitFor({ state: 'visible', timeout: 5000 })
  await page.waitForFunction(
    () => document.querySelectorAll('aside div[title^="跳转到终端第"]').length > 0,
    { timeout: 30000 },
  )
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

    // 1. Header button renamed and shows combined count.
    const btnText = (await page.locator('button[title="交互消息面板"]').textContent()).trim()
    const totalCount = Number(btnText.split(' ')[1])
    check('header button labelled 交互消息 with count', /^交互消息 \d+$/.test(btnText) && totalCount > 0, btnText)

    // 2. Two checkboxes, both checked by default (fresh profile).
    const userBox = kindCheckbox(page, '用户消息')
    const asstBox = kindCheckbox(page, '助手回复')
    check('用户消息 checkbox exists and checked by default', await userBox.isChecked())
    check('助手回复 checkbox exists and checked by default', await asstBox.isChecked())

    // 3. Row count matches the header count; both kinds present.
    const rows = await panelRows(page).count()
    check('panel lists all interaction messages', rows === totalCount, `rows=${rows} header=${totalCount}`)
    const { user, assistant } = await chipCounts(page)
    check('user rows present', user > 0, `user=${user}`)
    check('assistant rows present', assistant > 0, `assistant=${assistant}`)
    check('chip counts sum to row count', user + assistant === rows, `${user}+${assistant}!=${rows}`)

    // 4. Reply text truncated to the backend cap (400 runes + "…").
    const labels = await panel(page).locator('div[title^="跳转到终端第"] span[title]').allTextContents()
    const longest = Math.max(...labels.map(s => s.length))
    check('message text capped at 401 chars', longest <= 401, `longest=${longest}`)

    // 5. User and assistant rows use different (non-transparent) backgrounds.
    const bgOf = async (kindTitle) => {
      const row = panel(page).locator(`div[title^="跳转到终端第"]:has(span[title="${kindTitle}"])`).first()
      return row.evaluate(el => getComputedStyle(el.parentElement).backgroundColor)
    }
    const bgUser = await bgOf('用户消息')
    const bgAsst = await bgOf('助手回复')
    check('user/assistant rows have different backgrounds', bgUser !== bgAsst, `user=${bgUser} assistant=${bgAsst}`)
    check('row backgrounds are not fully transparent', !/rgba\(0, 0, 0, 0\)/.test(bgUser) && !/rgba\(0, 0, 0, 0\)/.test(bgAsst), `user=${bgUser} assistant=${bgAsst}`)

    await page.screenshot({ path: path.join(SHOT_DIR, 'interaction-panel.png') })
    console.log(`Screenshot saved: ${path.join(SHOT_DIR, 'interaction-panel.png')}`)

    // 6. Individually toggleable: uncheck 助手回复 → only user rows remain.
    await asstBox.click()
    await page.waitForFunction(
      (n) => document.querySelectorAll('aside div[title^="跳转到终端第"]').length === n,
      user, { timeout: 5000 },
    )
    let chips = await chipCounts(page)
    check('unchecking 助手回复 hides assistant rows', chips.assistant === 0 && chips.user === user, JSON.stringify(chips))

    // 7. Uncheck both → guidance hint.
    await userBox.click()
    check('unchecking both shows guidance hint', await panel(page).locator(':text("请在上方勾选要显示的消息类型")').isVisible())

    // 8. Re-enable 用户消息 only → assistant stays hidden.
    await userBox.click()
    chips = await chipCounts(page)
    check('single-kind re-enable works', chips.user === user && chips.assistant === 0, JSON.stringify(chips))

    // 9. Checkbox state persists across reloads (assistant still off).
    await page.reload()
    await openSession(page) // session selection does not survive a reload
    check('checkbox choice persists across reload', !(await kindCheckbox(page, '助手回复').isChecked()) && (await kindCheckbox(page, '用户消息').isChecked()))
    chips = await chipCounts(page)
    check('persisted filter hides assistant rows after reload', chips.assistant === 0 && chips.user === user, JSON.stringify(chips))

    // 10. Text filter still works on the merged list.
    await kindCheckbox(page, '助手回复').click() // restore both
    await page.waitForFunction(
      (n) => document.querySelectorAll('aside div[title^="跳转到终端第"]').length === n,
      totalCount, { timeout: 5000 },
    )
    const firstLabel = (await panel(page).locator('div[title^="跳转到终端第"] span[title]').first().textContent()).slice(0, 6)
    await panel(page).locator('input[aria-label="按文字筛选交互消息"]').fill(firstLabel)
    await page.waitForTimeout(300)
    const filtered = await panelRows(page).count()
    check('text filter narrows the merged list', filtered > 0 && filtered <= totalCount, `filtered=${filtered}`)

    check('no console errors', consoleErrors.length === 0, consoleErrors.slice(0, 3).join(' | '))
  } catch (err) {
    failures++
    console.error('FATAL:', err.message)
    await page.screenshot({ path: path.join(SHOT_DIR, 'interaction-panel-error.png') }).catch(() => {})
  } finally {
    await browser.close()
  }

  console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECK(S) FAILED`)
  process.exit(failures === 0 ? 0 : 1)
}

run()
