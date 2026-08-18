// Live check: PR/MR lookup shows local creation evidence without host approval.
// Run after ./run.sh all from the worktree root:
//   node frontend/scripts/validate-change-request-lookup.mjs
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

const CREATED_URL = 'https://github.com/acme/widgets/pull/42'
const STALE_URL = 'https://github.com/acme/widgets/pull/1'
const CREATED_SESSION = {
  root_agent_type: 'codex',
  root_session_id: 'rollout-sanitized-created',
}
const COPY = {
  en: {
    created: 'Sessions with this PR/MR · 1',
    hosted: 'Contact the host',
    search: 'Search',
  },
  'zh-CN': {
    created: '包含该 PR/MR 的会话 · 1',
    hosted: '向平台请求',
    search: '搜索',
  },
}

const exact = { state: 'exact', reasons: [] }
const creationResponse = {
  reference: {
    provider: 'github',
    display_origin: 'https://github.com',
    target_repository_slug: 'acme/widgets',
    display_number: '42',
    normalized_url: CREATED_URL,
  },
  creation_sessions: [{
    ...CREATED_SESSION,
    evidence: {
      evidence_id: 'cr-create-sanitized',
      reference: {
        provider: 'github',
        display_origin: 'https://github.com',
        target_repository_slug: 'acme/widgets',
        display_number: '42',
        normalized_url: CREATED_URL,
      },
      command_kind: 'github_cli_pr_create',
      tool_name: 'exec',
      event_id: 'invoke',
      turn_index: 7,
      recorded_at: '2026-08-11T16:17:21Z',
      source_revision: 'sha256:source',
      assessment: exact,
    },
  }],
  matches: [],
  assessment: exact,
}
const staleResponse = {
  ...creationResponse,
  reference: { ...creationResponse.reference, display_number: '1', normalized_url: STALE_URL },
  creation_sessions: [{
    ...creationResponse.creation_sessions[0],
    root_session_id: 'rollout-stale',
    evidence: {
      ...creationResponse.creation_sessions[0].evidence,
      evidence_id: 'cr-create-stale',
      reference: { ...creationResponse.reference, display_number: '1', normalized_url: STALE_URL },
    },
  }],
}

let failures = 0
function check(name, condition, detail = '') {
  if (condition) console.log(`PASS: ${name}`)
  else {
    failures++
    console.error(`FAIL: ${name}${detail ? ` — ${detail}` : ''}`)
  }
}

function resolveURL() {
  return new URL('/api/change-requests/resolve', BASE_URL).toString()
}

async function seedLocale(page, locale) {
  await page.addInitScript(loc => {
    localStorage.setItem('si-locale', loc)
  }, locale)
}

async function openLookup(page) {
  await page.goto(BASE_URL, { waitUntil: 'domcontentloaded' })
  await page.locator('[data-testid="sidebar-change-request-lookup"]').waitFor({ state: 'visible', timeout: 30_000 })
  await page.locator('[data-testid="sidebar-change-request-lookup"]').click()
  await page.locator('[data-testid="change-request-lookup-dialog"]').waitFor({ state: 'visible', timeout: 10_000 })
}

async function searchReference(page, reference, locale, { submit = 'button' } = {}) {
  const input = page.locator('[data-testid="change-request-lookup-dialog"] input[type="text"]')
  await input.fill(reference)
  if (submit === 'enter') {
    await input.press('Enter')
    return
  }
  await page.locator('[data-testid="change-request-lookup-dialog"] button', { hasText: COPY[locale].search }).click()
}

async function assertCreatedGroup(page, locale) {
  const group = page.locator('[data-testid="change-request-creation-sessions"]')
  await group.waitFor({ state: 'visible', timeout: 10_000 })
  const title = (await group.locator('h3').innerText()).trim()
  check(`${locale} created group title`, title === COPY[locale].created, `got ${title}`)
  check(`${locale} no approval on first search`, await page.locator('[data-testid="change-host-approval"]').count() === 0)
  check(`${locale} hosted details remain separate`, await page.locator('[data-testid="change-request-hosted-details"]').isVisible())
  const hostedLabel = (await page.locator('[data-testid="change-request-hosted-details"] button').innerText()).trim()
  check(`${locale} hosted details action`, hostedLabel === COPY[locale].hosted, `got ${hostedLabel}`)
}

async function runLocale(browser, locale) {
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 } })
  const page = await context.newPage()
  const consoleErrors = []
  page.on('pageerror', error => consoleErrors.push(String(error)))
  await seedLocale(page, locale)
  await page.route('**/api/change-requests/resolve', async route => {
    const body = route.request().postDataJSON() ?? {}
    if (body.include_hosted_details) {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          ...creationResponse,
          assessment: { state: 'missing', reason_code: 'change_host_not_approved', reasons: ['change_host_not_approved'] },
        }),
      })
      return
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(creationResponse),
    })
  })
  await openLookup(page)
  await searchReference(page, CREATED_URL, locale)
  await assertCreatedGroup(page, locale)
  await page.screenshot({
    path: path.join(SHOT_DIR, `change-request-lookup-${locale}.png`),
    fullPage: false,
  })
  const sessionDetail = page.waitForRequest(request => {
    if (request.method() !== 'GET') return false
    try {
      return decodeURIComponent(new URL(request.url()).pathname) === `/api/sessions/${CREATED_SESSION.root_session_id}`
    } catch {
      return false
    }
  }, { timeout: 10_000 })
  await page.locator('[data-testid="change-request-creation-sessions"] button').click()
  await sessionDetail
  check(`${locale} session navigation`, await page.locator('[data-testid="change-request-lookup-dialog"]').count() === 0)
  check(`${locale} no console errors`, consoleErrors.length === 0, consoleErrors.join(' | '))
  await context.close()
}

async function runStaleGuard(browser) {
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 } })
  const page = await context.newPage()
  await seedLocale(page, 'en')
  let releaseStale
  const staleGate = new Promise(resolve => { releaseStale = resolve })
  await page.route('**/api/change-requests/resolve', async route => {
    const body = route.request().postDataJSON() ?? {}
    if (String(body.reference).includes('/pull/1')) {
      await staleGate
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(staleResponse),
      })
      return
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(creationResponse),
    })
  })
  await openLookup(page)
  await searchReference(page, STALE_URL, 'en')
  await searchReference(page, CREATED_URL, 'en', { submit: 'enter' })
  releaseStale()
  await page.locator('[data-testid="change-request-creation-sessions"]').waitFor({ state: 'visible', timeout: 10_000 })
  const sessionText = await page.locator('[data-testid="change-request-creation-sessions"]').innerText()
  check('stale resolve cannot replace current query', sessionText.includes(CREATED_SESSION.root_session_id.slice(0, 16)))
  check('stale resolve did not keep older session', !sessionText.includes('rollout-stale'))
  await context.close()
}

const browser = await chromium.launch({ headless: true })
try {
  fs.mkdirSync(SHOT_DIR, { recursive: true })
  console.log(`Validating change-request lookup at ${BASE_URL}`)
  for (const locale of ['en', 'zh-CN']) {
    await runLocale(browser, locale)
  }
  await runStaleGuard(browser)
} finally {
  await browser.close()
}

if (failures) {
  console.error(`${failures} assertion(s) failed`)
  process.exit(1)
}
console.log('Change request lookup live checks passed')
console.log(`Screenshots: ${path.join(SHOT_DIR, 'change-request-lookup-en.png')}`)
console.log(`Screenshots: ${path.join(SHOT_DIR, 'change-request-lookup-zh-CN.png')}`)
