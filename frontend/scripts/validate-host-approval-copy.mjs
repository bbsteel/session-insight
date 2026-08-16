// Live check: host-approval copy says SessionInsight will contact the host.
// Run after ./run.sh all from the worktree root:
//   node frontend/scripts/validate-host-approval-copy.mjs
import { chromium } from 'playwright'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const urlFiles = [
  path.join(repoRoot, '.runtime/session-insight.url'),
  path.join(repoRoot, 'session-insight.url'),
]
const BASE_URL = urlFiles.find(fs.existsSync)
  ? fs.readFileSync(urlFiles.find(fs.existsSync), 'utf8').trim()
  : 'http://127.0.0.1:8080/'
const SHOT_DIR = path.join(repoRoot, '.runtime')
const PR_URL = 'https://github.com/acme/widgets/pull/42'

const COPY = {
  en: {
    search: 'Search',
    contact: 'Contact the host',
    title: 'Allow SessionInsight to contact GitHub?',
    help: 'This fetches the title, status, commits, and diff from GitHub.',
    inspect: 'Show the addresses first',
    endpoints: 'Addresses SessionInsight will call',
    confirm: 'I allow SessionInsight to call only these addresses, and only to read.',
    allow: 'Allow these requests',
  },
  'zh-CN': {
    search: '搜索',
    contact: '向平台请求',
    title: '允许 SessionInsight 向 GitHub 发请求？',
    help: '这会从 GitHub 读取标题、状态、提交和差异。',
    inspect: '先看会访问哪些地址',
    endpoints: 'SessionInsight 将访问的地址',
    confirm: '我允许 SessionInsight 只向这些地址发起只读请求。',
    allow: '允许这些请求',
  },
}

const localResult = {
  reference: {
    provider: 'github',
    display_origin: 'https://github.com',
    target_repository_slug: 'acme/widgets',
    display_number: '42',
    normalized_url: PR_URL,
  },
  creation_sessions: [],
  matches: [],
  assessment: { state: 'missing', reason_code: 'change_request_not_found', reasons: ['change_request_not_found'] },
}

let failures = 0
function check(name, condition, detail = '') {
  if (condition) console.log(`PASS: ${name}`)
  else {
    failures++
    console.error(`FAIL: ${name}${detail ? ` — ${detail}` : ''}`)
  }
}

async function runLocale(browser, locale) {
  const copy = COPY[locale]
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 } })
  const page = await context.newPage()
  await page.addInitScript(loc => {
    localStorage.setItem('si-locale', loc)
  }, locale)
  await page.route('**/api/change-requests/resolve', async route => {
    const body = route.request().postDataJSON() ?? {}
    if (body.include_hosted_details) {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          ...localResult,
          assessment: { state: 'missing', reason_code: 'change_host_not_approved', reasons: ['change_host_not_approved'] },
        }),
      })
      return
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(localResult),
    })
  })
  await page.route('**/api/change-hosts/preview', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        host: {
          key: 'host-github-public',
          provider: 'github',
          display_origin: 'https://github.com',
          endpoint_origins: ['https://github.com', 'https://api.github.com'],
        },
        requires_http_approval: false,
        requires_private_network_approval: false,
      }),
    })
  })

  await page.goto(BASE_URL, { waitUntil: 'domcontentloaded' })
  await page.locator('[data-testid="sidebar-change-request-lookup"]').click()
  await page.locator('[data-testid="change-request-lookup-dialog"]').waitFor({ state: 'visible' })
  await page.locator('[data-testid="change-request-lookup-dialog"] input[type="text"]').fill(PR_URL)
  await page.locator('[data-testid="change-request-lookup-dialog"] button', { hasText: copy.search }).click()
  const hosted = page.locator('[data-testid="change-request-hosted-details"]')
  await hosted.waitFor({ state: 'visible', timeout: 10_000 })
  check(`${locale} first search has no approval`, await page.locator('[data-testid="change-host-approval"]').count() === 0)
  await hosted.getByRole('button', { name: copy.contact }).click()
  const approval = page.locator('[data-testid="change-host-approval"]')
  await approval.waitFor({ state: 'visible', timeout: 10_000 })
  const title = (await approval.locator('h3').innerText()).trim()
  const help = (await approval.locator('p').first().innerText()).trim()
  check(`${locale} approval title`, title === copy.title, `got ${title}`)
  check(`${locale} approval help names the fetch`, help.includes(copy.help), `got ${help}`)
  await approval.getByText(copy.endpoints).waitFor({ state: 'visible', timeout: 10_000 })
  check(`${locale} lists github.com`, await approval.getByText('https://github.com', { exact: true }).count() > 0)
  check(`${locale} lists api.github.com`, await approval.getByText('https://api.github.com', { exact: true }).count() > 0)
  check(`${locale} confirm copy`, (await approval.locator('label').innerText()).includes(copy.confirm))
  const allow = approval.getByRole('button', { name: copy.allow })
  check(`${locale} allow stays disabled until confirm`, await allow.isDisabled())
  await approval.locator('input[type="checkbox"]').check()
  check(`${locale} allow enables after confirm`, await allow.isEnabled())
  await page.screenshot({ path: path.join(SHOT_DIR, `host-approval-copy-${locale}.png`) })
  await context.close()
}

const browser = await chromium.launch({ headless: true })
try {
  fs.mkdirSync(SHOT_DIR, { recursive: true })
  console.log(`Validating host-approval copy at ${BASE_URL}`)
  for (const locale of ['en', 'zh-CN']) {
    await runLocale(browser, locale)
  }
} finally {
  await browser.close()
}

if (failures) {
  console.error(`${failures} assertion(s) failed`)
  process.exit(1)
}
console.log('Host-approval copy live checks passed')
console.log(`Screenshots: ${path.join(SHOT_DIR, 'host-approval-copy-en.png')}`)
console.log(`Screenshots: ${path.join(SHOT_DIR, 'host-approval-copy-zh-CN.png')}`)
