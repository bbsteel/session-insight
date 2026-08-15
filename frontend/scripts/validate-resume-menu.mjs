/**
 * Live Playwright check for the continue-work menu hierarchy and writing lock.
 * Usage: READY_URL=http://127.0.0.1:PORT node scripts/validate-resume-menu.mjs
 */
import { chromium } from 'playwright'
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'

const ready = (process.env.READY_URL || '').replace(/\/$/, '')
if (!ready) {
  console.error('READY_URL required')
  process.exit(1)
}

const outDir = process.env.SCREENSHOT_DIR || join(process.cwd(), '..', '.runtime', 'resume-menu')
mkdirSync(outDir, { recursive: true })

const hourAgo = new Date(Date.now() - 60 * 60_000).toISOString()
const justNow = new Date().toISOString()

const idleId = 'fixture-resume-idle'
const writingId = 'fixture-resume-writing'
const liveId = 'fixture-resume-live'

function terminalNone() {
  return {
    state: 'none',
    session_live: false,
    liveness_state: 'exact',
    confidence: 'unknown',
    focusable: false,
  }
}

function detail(id, { isLive, updatedAt }) {
  return {
    id,
    agent_type: 'claude',
    name: `Resume menu ${id}`,
    repository: '',
    branch: '',
    project: '',
    cwd: '/home/deck/projects/session-insight',
    turn_count: 1,
    message_count: 2,
    is_live: isLive,
    bookmarked: false,
    record_available: true,
    created_at: hourAgo,
    updated_at: updatedAt,
    model_name: 'fixture-model',
    turns: [{
      turn_index: 0,
      user_message: 'hello',
      assistant_message: 'world',
      token_usage: {
        prompt_tokens: 0, completion_tokens: 0,
        cache_read_tokens: 0, cache_write_tokens: 0, premium_requests: 0,
      },
      tool_call_count: 0, error_count: 0, duration_ms: 10,
      events: [], tool_names: [], tool_details: [], skills: [], request_count: 1,
    }],
    agent_capabilities: {
      agent_type: 'claude',
      adapter_revision: 1,
      status: { resume: { state: 'exact' } },
      actions: { resume: { availability: 'available' } },
      liveness: {
        is_live: isLive,
        state: isLive ? 'estimated' : 'exact',
        reason_code: isLive ? 'timestamp_heuristic' : 'session_not_live',
      },
    },
  }
}

function planFor(id, { running, live }) {
  return {
    status: running ? 'session_running' : 'ready',
    agent_type: 'claude',
    session_id: id,
    cwd: '/home/deck/projects/session-insight',
    command: 'claude --resume fixture',
    supports_unsafe: true,
    liveness: {
      is_live: live,
      state: live ? 'estimated' : 'exact',
      reason_code: live ? 'timestamp_heuristic' : 'session_not_live',
    },
    terminal: terminalNone(),
  }
}

const fixtures = {
  [idleId]: {
    detail: detail(idleId, { isLive: false, updatedAt: hourAgo }),
    plan: planFor(idleId, { running: false, live: false }),
  },
  [writingId]: {
    detail: detail(writingId, { isLive: false, updatedAt: justNow }),
    plan: planFor(writingId, { running: false, live: false }),
  },
  [liveId]: {
    detail: detail(liveId, { isLive: true, updatedAt: justNow }),
    plan: planFor(liveId, { running: true, live: true }),
  },
}

function sessionIdFromUrl(url) {
  try {
    const path = new URL(url).pathname.replace(/\/$/, '')
    const match = path.match(/^\/api\/sessions\/([^/]+)(?:\/(.*))?$/)
    return match ? { id: decodeURIComponent(match[1]), rest: match[2] || '' } : null
  } catch {
    return null
  }
}

async function installFixtures(page) {
  await page.route('**/api/agents', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([{
        type: 'claude', display_name: 'Claude Code', session_count: 3,
        discovered: true, adapter_revision: 1, can_delete: true, can_terminate: true,
        capabilities: { resume: { state: 'exact' } },
      }]),
    })
  })
  await page.route('**/api/bookmarks', async route => {
    await route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
  })
  await page.route(url => {
    try {
      const path = new URL(url).pathname.replace(/\/$/, '') || '/'
      return path === '/api/sessions'
    } catch {
      return false
    }
  }, async route => {
    if (route.request().method() !== 'GET') return route.continue()
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(Object.values(fixtures).map(item => ({
        id: item.detail.id,
        agent_type: item.detail.agent_type,
        name: item.detail.name,
        model_name: item.detail.model_name,
        repository: '', branch: '', project: '',
        cwd: item.detail.cwd,
        turn_count: 1, message_count: 2,
        is_live: item.detail.is_live,
        bookmarked: false,
        created_at: item.detail.created_at,
        updated_at: item.detail.updated_at,
      }))),
    })
  })
  await page.route(url => sessionIdFromUrl(url) !== null, async route => {
    const parsed = sessionIdFromUrl(route.request().url())
    const fixture = parsed ? fixtures[parsed.id] : null
    if (!fixture) return route.continue()
    if (!parsed.rest) {
      const payload = parsed.id === writingId
        ? { ...fixture.detail, updated_at: new Date().toISOString() }
        : fixture.detail
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(payload),
      })
      return
    }
    if (parsed.rest === 'resume') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(fixture.plan),
      })
      return
    }
    if (parsed.rest === 'positions') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          session_id: fixture.detail.id, agent_type: 'claude',
          revision: 0, cols: 80, total_lines: 2, positions: [],
        }),
      })
      return
    }
    if (parsed.rest === 'live-revision') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ revision: 0, is_live: fixture.detail.is_live }),
      })
      return
    }
    if (parsed.rest === 'render') {
      await route.fulfill({ status: 200, contentType: 'text/plain', body: 'hello\nworld\n' })
      return
    }
    if (parsed.rest === 'collaboration') {
      await route.fulfill({
        status: 404,
        contentType: 'application/json',
        body: JSON.stringify({ code: 'collaboration_unsupported', error: 'unsupported' }),
      })
      return
    }
    if (parsed.rest === 'edits' || parsed.rest === 'tool-outputs') {
      await route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
      return
    }
    await route.fulfill({
      status: 404,
      contentType: 'application/json',
      body: JSON.stringify({ code: 'not_found', error: parsed.rest }),
    })
  })
}

function copy(locale) {
  return locale === 'zh-CN'
    ? {
      status: '状态',
      actions: '操作',
      continue: '继续工作',
      copy: '复制恢复命令',
      unsafe: '跳过权限检查继续…',
      writing: '正在输出',
      writingHint: '会话正在输出消息，此时无法在这里继续工作。',
      running: '会话运行中 · 终端未知',
      runningHint: '会话仍在运行。请返回对应终端，不要再启动一份。',
    }
    : {
      status: 'Status',
      actions: 'Actions',
      continue: 'Continue work',
      copy: 'Copy resume command',
      unsafe: 'Continue without permission checks…',
      writing: 'Writing…',
      writingHint: 'This session is currently writing messages, so it cannot be continued here.',
      running: 'Running · terminal unknown',
      runningHint: 'This session is still running. Return to its terminal instead of starting another copy.',
    }
}

async function openSession(page, locale, id) {
  await page.addInitScript(loc => {
    localStorage.setItem('si-locale', loc)
  }, locale)
  await installFixtures(page)
  await page.goto(`${ready}/#/session/claude/${id}`, { waitUntil: 'domcontentloaded', timeout: 60_000 })
  try {
    await page.locator('[data-testid="session-toolbar"]').waitFor({ state: 'visible', timeout: 20_000 })
    await page.locator('[data-testid="resume-primary-button"]').waitFor({ state: 'visible', timeout: 10_000 })
  } catch (err) {
    await page.screenshot({ path: join(outDir, `open-failed-${locale}-${id}.png`) })
    const body = (await page.locator('body').innerText().catch(() => '')).slice(0, 800)
    throw new Error(`failed to open ${id} (${locale}): ${err}\nbody=${body}`)
  }
}

async function openMenu(page) {
  await page.locator('[data-testid="resume-menu-button"]').click()
  const menu = page.locator('[data-testid="resume-menu"]')
  await menu.waitFor({ state: 'visible', timeout: 8_000 })
  return menu
}

function assert(condition, message) {
  if (!condition) throw new Error(message)
}

async function assertHierarchy(menu, labels) {
  const status = menu.locator('[data-testid="resume-menu-status"]')
  await status.waitFor({ state: 'visible' })
  const statusText = (await status.innerText()).replace(/\s+/g, ' ')
  assert(statusText.includes(labels.status), `status section missing label: ${statusText}`)
  assert(!(await status.locator('button').count()), 'status section must not contain buttons')

  const actionsLabel = menu.locator('[data-testid="resume-menu-actions-label"]')
  assert((await actionsLabel.innerText()).trim() === labels.actions, 'actions section label mismatch')

  const continueBtn = menu.locator('[data-testid="resume-menu-continue"]')
  const copyBtn = menu.locator('[data-testid="resume-menu-copy"]')
  const unsafeBtn = menu.locator('[data-testid="resume-menu-unsafe"]')
  assert(await continueBtn.isVisible(), 'continue action missing')
  assert(await copyBtn.isVisible(), 'copy action missing')
  assert(await unsafeBtn.isVisible(), 'unsafe action missing')
  assert((await continueBtn.innerText()).includes(labels.continue), `continue label: ${await continueBtn.innerText()}`)
  assert((await copyBtn.innerText()).includes(labels.copy), `copy label: ${await copyBtn.innerText()}`)
  assert((await unsafeBtn.innerText()).includes(labels.unsafe), `unsafe label: ${await unsafeBtn.innerText()}`)

  const statusTitleColor = await menu.locator('[data-testid="resume-menu-status-title"]').evaluate(el => getComputedStyle(el).color)
  const continueColor = await continueBtn.evaluate(el => getComputedStyle(el).color)
  const copyColor = await copyBtn.evaluate(el => getComputedStyle(el).color)
  const unsafeColor = await unsafeBtn.evaluate(el => getComputedStyle(el).color)
  return { continueBtn, copyBtn, unsafeBtn, statusTitleColor, continueColor, copyColor, unsafeColor }
}

async function runLocale(browser, locale) {
  const labels = copy(locale)
  const errors = []

  const idlePage = await browser.newPage({ viewport: { width: 1400, height: 900 } })
  idlePage.on('pageerror', e => errors.push(String(e)))
  await openSession(idlePage, locale, idleId)
  const idlePrimary = (await idlePage.locator('[data-testid="resume-primary-button"]').innerText()).trim()
  assert(idlePrimary.includes(labels.continue), `${locale} idle primary should be continue, got "${idlePrimary}"`)
  const idleMenu = await openMenu(idlePage)
  const idle = await assertHierarchy(idleMenu, labels)
  assert(await idle.continueBtn.isEnabled(), `${locale} idle continue should be enabled`)
  assert(await idle.copyBtn.isEnabled(), `${locale} idle copy should be enabled`)
  assert(await idle.unsafeBtn.isEnabled(), `${locale} idle unsafe should be enabled`)
  assert(idle.continueColor === idle.copyColor, `${locale} idle continue/copy colors should match: ${idle.continueColor} vs ${idle.copyColor}`)
  assert(idle.unsafeColor === idle.continueColor, `${locale} idle unsafe should share action color, not a one-off warning fill: ${idle.unsafeColor}`)
  assert(idle.statusTitleColor !== idle.continueColor, `${locale} status title should not use the action color (${idle.statusTitleColor} vs ${idle.continueColor})`)
  assert(await idlePage.locator('[data-testid="resume-menu-blocked"]').count() === 0, `${locale} idle menu should not show a blocked note`)
  await idlePage.screenshot({ path: join(outDir, `idle-${locale}.png`) })
  await idlePage.close()

  const writingPage = await browser.newPage({ viewport: { width: 1400, height: 900 } })
  writingPage.on('pageerror', e => errors.push(String(e)))
  await openSession(writingPage, locale, writingId)
  const writingPrimary = (await writingPage.locator('[data-testid="resume-primary-button"]').innerText()).trim()
  assert(writingPrimary.includes(labels.writing), `${locale} writing primary should be writing, got "${writingPrimary}"`)
  const writingMenu = await openMenu(writingPage)
  const writing = await assertHierarchy(writingMenu, labels)
  assert(!(await writing.continueBtn.isEnabled()), `${locale} writing continue should be disabled`)
  assert(await writing.copyBtn.isEnabled(), `${locale} writing copy should stay enabled`)
  assert(!(await writing.unsafeBtn.isEnabled()), `${locale} writing unsafe should be disabled`)
  const writingNote = (await writingPage.locator('[data-testid="resume-menu-blocked"]').innerText()).trim()
  assert(writingNote === labels.writingHint, `${locale} writing hint mismatch: ${writingNote}`)
  await writingPage.screenshot({ path: join(outDir, `writing-${locale}.png`) })
  await writingPage.close()

  const livePage = await browser.newPage({ viewport: { width: 1400, height: 900 } })
  livePage.on('pageerror', e => errors.push(String(e)))
  await openSession(livePage, locale, liveId)
  const livePrimary = (await livePage.locator('[data-testid="resume-primary-button"]').innerText()).trim()
  assert(livePrimary.includes(labels.running), `${locale} live primary should be running-unknown, got "${livePrimary}"`)
  const liveMenu = await openMenu(livePage)
  const live = await assertHierarchy(liveMenu, labels)
  assert(!(await live.continueBtn.isEnabled()), `${locale} live continue should be disabled`)
  assert(await live.copyBtn.isEnabled(), `${locale} live copy should stay enabled`)
  const liveNote = (await livePage.locator('[data-testid="resume-menu-blocked"]').innerText()).trim()
  assert(liveNote === labels.runningHint, `${locale} live hint mismatch: ${liveNote}`)
  await livePage.screenshot({ path: join(outDir, `live-${locale}.png`) })
  await livePage.close()

  if (errors.length) throw new Error(`${locale} page errors:\n${errors.join('\n')}`)
  console.log(`${locale} resume menu checks passed`)
}

const browser = await chromium.launch({ headless: true })
try {
  for (const locale of ['zh-CN', 'en']) {
    await runLocale(browser, locale)
  }
} finally {
  await browser.close()
}
console.log(`screenshots: ${outDir}`)
