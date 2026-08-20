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
  en: { locale: 'en', label: 'Quota', title: 'Quota management', close: 'Close' },
  'zh-CN': { locale: 'zh-CN', label: '额度', title: '额度管理', close: '关闭' },
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

  const configuredFilter = dialog.locator('[data-testid="coding-quota-filter-configured"]')
  const allFilter = dialog.locator('[data-testid="coding-quota-filter-all"]')
  check(`${locale} configured/all filters are visible`, await configuredFilter.isVisible() && await allFilter.isVisible())
  check(`${locale} configured filter is selected by default`, await configuredFilter.getAttribute('aria-pressed') === 'true')
  check(`${locale} unsupported plan is hidden by default`, !(await dialog.locator('[data-testid="quota-provider-copilot"]').isVisible()))

  const documentationLinks = dialog.locator('[data-testid^="quota-documentation-"]')
  const quotaQueryLinks = dialog.locator('[data-testid^="quota-query-"]')
  const strategySummaries = dialog.locator('[data-testid^="quota-strategy-"]')
  const configuredProviderCount = await dialog.locator('[data-testid^="quota-provider-"]').count()
  check(`${locale} configured quota documentation links are present`, configuredProviderCount === 0 || await documentationLinks.count() > 0)
  check(`${locale} configured quota query links are present`, configuredProviderCount === 0 || await quotaQueryLinks.count() > 0)
  check(`${locale} configured quota strategies are present`, configuredProviderCount === 0 || await strategySummaries.count() === configuredProviderCount)
  if (await quotaQueryLinks.count() > 0) {
    const queryHref = await quotaQueryLinks.first().getAttribute('href')
    check(`${locale} quota query link has a URL`, !!queryHref && /^https?:\/\//.test(queryHref), queryHref ?? '')
  }

  const percentageWindows = dialog.locator('[data-quota-percentage-window="true"]')
  for (let index = 0; index < await percentageWindows.count(); index++) {
    const text = await percentageWindows.nth(index).innerText()
    check(`${locale} percentage window omits an upper limit`, !/(?:Limit|上限)\s*[:：]?\s*100\b/.test(text), text)
  }

  await allFilter.click()
  check(`${locale} all filter can reveal unsupported plans`, await allFilter.getAttribute('aria-pressed') === 'true' && await dialog.locator('[data-testid="quota-provider-copilot"]').isVisible())
  check(`${locale} unsupported plan includes a strategy summary`, await dialog.locator('[data-testid="quota-strategy-copilot"]').isVisible())

  const dialogScreenshotPath = path.join(SHOT_DIR, `coding-quota-dialog-${locale}.png`)
  await page.screenshot({ path: dialogScreenshotPath })
  console.log(`Dialog screenshot saved: ${dialogScreenshotPath}`)

  await dialog.getByRole('button', { name: copy.close, exact: true }).click()
  await dialog.waitFor({ state: 'hidden', timeout: 5_000 })
  check(`${locale} quota dialog closes`, await dialog.count() === 0 || !(await dialog.isVisible()))

  const screenshotPath = path.join(SHOT_DIR, `coding-quota-entry-${locale}.png`)
  await page.screenshot({ path: screenshotPath })
  console.log(`Screenshot saved: ${screenshotPath}`)
}

async function checkLowQuotaPresentation(page, locale) {
  const copy = expected[locale]
  await page.addInitScript(selectedLocale => {
    localStorage.setItem('si-locale', selectedLocale)
  }, copy.locale)
  await page.route('**/api/coding-quotas', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        providers: [{
          id: 'kimi',
          display_name_key: 'quota.provider.kimi',
          description_key: 'quota.provider.kimiDescription',
          quota_strategy_key: 'quota.provider.kimiStrategy',
          documentation_url: 'https://www.kimi.com/help/kimi-code/benefits',
          quota_url: 'https://api.kimi.com/coding/v1/usages',
          supports_exact_quota: true,
          snapshot: {
            provider_id: 'kimi',
            status: 'available',
            windows: [{ id: 'primary', remaining_percent: 9, limit_amount: 100, unit: 'percent' }],
          },
        }],
      }),
    })
  })
  await page.goto(BASE_URL, { waitUntil: 'domcontentloaded' })
  await page.locator('[data-testid="global-coding-quota"]').click()
  const dialog = page.locator('[data-testid="coding-quota-dialog"]')
  await dialog.waitFor({ state: 'visible', timeout: 10_000 })

  const remaining = dialog.locator('[data-testid="quota-remaining-kimi-primary"]')
  const percentageWindow = dialog.locator('[data-testid="quota-window-kimi-primary"]')
  const strategySummary = dialog.locator('[data-testid="quota-strategy-kimi"]')
  check(`${locale} quota strategy summary is rendered`, (await strategySummary.innerText()).length > 0)
  check(`${locale} 9% quota is rendered`, (await remaining.innerText()).includes('9%'))
  check(`${locale} 9% quota uses the critical color`, (await remaining.getAttribute('class'))?.includes('text-[var(--error)]'))
  check(`${locale} percentage quota omits the upper limit`, !(await percentageWindow.innerText()).match(/(?:Limit|上限)\s*[:：]?\s*100\b/))
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

    const fixtureContext = await browser.newContext({ viewport: { width: 1400, height: 900 } })
    const fixturePage = await fixtureContext.newPage()
    await checkLowQuotaPresentation(fixturePage, locale)
    await fixtureContext.close()
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
