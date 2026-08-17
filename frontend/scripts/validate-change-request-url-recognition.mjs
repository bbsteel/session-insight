// Live check: PR/MR-shaped URLs resolve without CLI or host approval.
// Run after ./run.sh all from the worktree root:
//   node frontend/scripts/validate-change-request-url-recognition.mjs
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

const GITEE_URL = 'https://gitee.com/acme/widgets/pulls/12'
const WIKI_URL = 'https://wiki.example/about'
const COPY = {
  en: {
    search: 'Search',
    mentionedTitle: 'Sessions with this PR/MR · 1',
    mentioned: 'Contains this link',
    created: 'Created here',
  },
  'zh-CN': {
    search: '搜索',
    mentionedTitle: '包含该 PR/MR 的会话 · 1',
    mentioned: '包含此链接',
    created: '在此创建',
  },
}

let failures = 0
function check(name, condition, detail = '') {
  if (condition) console.log(`PASS: ${name}`)
  else {
    failures++
    console.error(`FAIL: ${name}${detail ? ` — ${detail}` : ''}`)
  }
}

async function resolveReference(reference) {
  const response = await fetch(new URL('/api/change-requests/resolve', BASE_URL), {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ reference }),
  })
  return { status: response.status, body: await response.json().catch(() => ({})) }
}

async function assertAPIRecognition() {
  const accepted = await resolveReference(GITEE_URL)
  check('gitee pull URL is recognized', accepted.status === 200 && accepted.body.reference?.provider === 'generic',
    `status=${accepted.status} body=${JSON.stringify(accepted.body)}`)
  check('gitee pull URL keeps the exact path', accepted.body.reference?.normalized_url === GITEE_URL,
    `normalized=${accepted.body.reference?.normalized_url}`)
  const rejected = await resolveReference(WIKI_URL)
  check('non-review URL is rejected', rejected.status === 400 && rejected.body.code === 'change_alias_ambiguous',
    `status=${rejected.status} body=${JSON.stringify(rejected.body)}`)
}

function mentionedResponse() {
  const exact = { state: 'exact', reasons: [] }
  return {
    reference: {
      provider: 'generic',
      display_origin: 'https://gitee.com',
      target_repository_slug: 'acme/widgets',
      display_number: '12',
      normalized_url: GITEE_URL,
    },
    creation_sessions: [{
      root_agent_type: 'grok',
      root_session_id: 'rollout-sanitized-mentioned',
      evidence: {
        evidence_id: 'cr-create-url',
        reference: {
          provider: 'generic',
          display_origin: 'https://gitee.com',
          target_repository_slug: 'acme/widgets',
          display_number: '12',
          normalized_url: GITEE_URL,
        },
        command_kind: 'change_request_url',
        tool_name: 'message',
        event_id: 'assistant',
        turn_index: 2,
        recorded_at: '2026-08-11T16:17:21Z',
        source_revision: 'index:grok:mentioned:1',
        assessment: exact,
      },
    }],
    matches: [],
    assessment: exact,
  }
}

async function runLocale(browser, locale) {
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 } })
  const page = await context.newPage()
  const consoleErrors = []
  page.on('pageerror', error => consoleErrors.push(String(error)))
  await page.addInitScript(loc => { localStorage.setItem('si-locale', loc) }, locale)
  await page.route('**/api/change-requests/resolve', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(mentionedResponse()),
    })
  })
  await page.goto(BASE_URL, { waitUntil: 'domcontentloaded' })
  await page.locator('[data-testid="sidebar-change-request-lookup"]').click()
  const dialog = page.locator('[data-testid="change-request-lookup-dialog"]')
  await dialog.waitFor({ state: 'visible', timeout: 15_000 })
  await dialog.locator('input[type="text"]').fill(GITEE_URL)
  await dialog.locator('button', { hasText: COPY[locale].search }).click()
  const group = page.locator('[data-testid="change-request-creation-sessions"]')
  await group.waitFor({ state: 'visible', timeout: 10_000 })
  const title = (await group.locator('h3').innerText()).trim()
  const row = (await group.locator('button').innerText()).trim()
  check(`${locale} mentioned title`, title === COPY[locale].mentionedTitle, `got ${title}`)
  check(`${locale} mentioned label`, row.includes(COPY[locale].mentioned), `got ${row}`)
  check(`${locale} does not claim CLI create`, !row.includes(COPY[locale].created))
  await page.screenshot({
    path: path.join(SHOT_DIR, `change-request-url-${locale}.png`),
    fullPage: false,
  })
  check(`${locale} no console errors`, consoleErrors.length === 0, consoleErrors.join(' | '))
  await context.close()
}

await assertAPIRecognition()
const browser = await chromium.launch({ headless: true })
try {
  fs.mkdirSync(SHOT_DIR, { recursive: true })
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
console.log('PR/MR URL recognition checks passed')
console.log(`Screenshots: ${path.join(SHOT_DIR, 'change-request-url-en.png')}`)
console.log(`Screenshots: ${path.join(SHOT_DIR, 'change-request-url-zh-CN.png')}`)
