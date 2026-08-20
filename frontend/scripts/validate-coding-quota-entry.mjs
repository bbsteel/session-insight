// Live check: the coding-plan quota entry lives in the global search toolbar,
// not in the crowded session sidebar.
// Run after ./run.sh all from the worktree root:
//   node frontend/scripts/validate-coding-quota-entry.mjs
import { chromium } from 'playwright'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const urlFiles = [
  path.join(repoRoot, '.runtime/session-insight.url'),
  path.join(repoRoot, 'session-insight.url'),
]
const urlFile = urlFiles.find(fs.existsSync)
const BASE_URL = urlFile ? fs.readFileSync(urlFile, 'utf8').trim() : 'http://127.0.0.1:8080/'
const SHOT_DIR = path.join(repoRoot, '.runtime')

const expected = {
  en: { locale: 'en', label: 'Quota', title: 'Coding-plan quota', close: 'Close' },
  'zh-CN': { locale: 'zh-CN', label: '额度', title: 'Coding Plan 剩余额度', close: '关闭' },
}

let failures = 0
function check(name, condition, detail = '') {
  if (condition) console.log(`PASS: ${name}`)
  else {
    failures++
    console.error(`FAIL: ${name}${detail ? ` — ${detail}` : ''}`)
  }
}

async function checkLocale(page, locale) {
  const copy = expected[locale]
  await page.addInitScript(selectedLocale => {
    localStorage.setItem('si-locale', selectedLocale)
  }, copy.locale)
  await page.goto(BASE_URL, { waitUntil: 'domcontentloaded' })

  const input = page.locator('[data-testid="global-search-input"]')
  const quotaButton = page.locator('[data-testid="global-coding-quota"]')
  await input.waitFor({ state: 'visible', timeout: 10_000 })
  await quotaButton.waitFor({ state: 'visible', timeout: 10_000 })

  check(`${locale} quota button label`, (await quotaButton.innerText()).trim() === copy.label)
  check(`${locale} sidebar has no quota button`, await page.locator('[data-testid="sidebar-coding-quota"]').count() === 0)

  const [inputBox, quotaBox] = await Promise.all([input.boundingBox(), quotaButton.boundingBox()])
  check(`${locale} search and quota boxes present`, !!(inputBox && quotaBox), JSON.stringify({ inputBox, quotaBox }))
  if (inputBox && quotaBox) {
    const quotaBottom = quotaBox.y + quotaBox.height
    check(`${locale} quota button is in the search toolbar`, quotaBox.y >= inputBox.y - 4 && quotaBottom <= inputBox.y + inputBox.height + 4,
      JSON.stringify({ inputBox, quotaBox }))
  }

  await quotaButton.click()
  const dialog = page.locator('[data-testid="coding-quota-dialog"]')
  await dialog.waitFor({ state: 'visible', timeout: 10_000 })
  check(`${locale} quota dialog title`, (await dialog.locator('h2').innerText()).trim() === copy.title)

  await dialog.getByRole('button', { name: copy.close, exact: true }).click()
  await dialog.waitFor({ state: 'hidden', timeout: 5_000 })
  check(`${locale} quota dialog closes`, await dialog.count() === 0 || !(await dialog.isVisible()))

  const screenshotPath = path.join(SHOT_DIR, `coding-quota-entry-${locale}.png`)
  await page.screenshot({ path: screenshotPath })
  console.log(`Screenshot saved: ${screenshotPath}`)
}

const browser = await chromium.launch({ headless: true })
fs.mkdirSync(SHOT_DIR, { recursive: true })

try {
  for (const locale of ['en', 'zh-CN']) {
    const context = await browser.newContext({ viewport: { width: 1400, height: 900 } })
    const page = await context.newPage()
    const errors = []
    page.on('pageerror', error => errors.push(String(error)))
    await checkLocale(page, locale)
    check(`${locale} no page errors`, errors.length === 0, errors.join('; '))
    await context.close()
  }
} catch (error) {
  failures++
  console.error('FAIL: script', error)
} finally {
  await browser.close()
}

if (failures > 0) {
  console.error(`\n${failures} check(s) failed`)
  process.exit(1)
}
console.log('\nAll coding quota entry checks passed')
