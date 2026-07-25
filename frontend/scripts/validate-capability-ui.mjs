/**
 * Live Playwright validation for Agent capability UI (Phase 5 + skeptic fixes).
 * Usage: READY_URL=http://127.0.0.1:PORT node scripts/validate-capability-ui.mjs
 *
 * Uses deterministic API route fixtures for missing/unsupported/runtime_check_required
 * plus an unmocked smoke path against the real backend.
 */
import { chromium } from 'playwright'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

const ready = (process.env.READY_URL || '').replace(/\/$/, '')
if (!ready) {
  console.error('READY_URL required')
  process.exit(1)
}

const outDir = process.env.SCREENSHOT_DIR || join(process.cwd(), '..', '.runtime', 'capability-ui')
mkdirSync(outDir, { recursive: true })

const CAP_IDS = [
  'discovery', 'replay', 'realtime', 'tokens', 'tool_results',
  'diff', 'subtasks', 'resume', 'delete', 'terminate',
]

const fixtureAgents = [
  {
    type: 'claude',
    display_name: 'Claude Code',
    session_count: 2,
    discovered: true,
    adapter_revision: 1,
    can_delete: true,
    can_terminate: true,
    capabilities: Object.fromEntries(CAP_IDS.map(id => [id, { state: 'exact' }])),
  },
  {
    type: 'copilot',
    display_name: 'GitHub Copilot',
    session_count: 0,
    discovered: false,
    adapter_revision: 1,
    can_delete: true,
    can_terminate: true,
    capabilities: {
      discovery: { state: 'exact' },
      replay: { state: 'exact' },
      realtime: { state: 'exact' },
      tokens: { state: 'exact' },
      tool_results: { state: 'exact' },
      diff: { state: 'exact' },
      subtasks: { state: 'exact' },
      resume: { state: 'unsupported', reason_code: 'adapter_not_implemented' },
      delete: { state: 'exact' },
      terminate: { state: 'exact' },
    },
  },
  {
    type: 'grok',
    display_name: 'Grok',
    session_count: 1,
    discovered: true,
    adapter_revision: 1,
    can_delete: true,
    can_terminate: true,
    capabilities: {
      discovery: { state: 'exact' },
      replay: { state: 'exact' },
      realtime: { state: 'exact' },
      tokens: { state: 'estimated', reason_code: 'timestamp_heuristic' },
      tool_results: { state: 'exact' },
      diff: { state: 'exact' },
      subtasks: { state: 'unsupported', reason_code: 'adapter_not_implemented' },
      resume: { state: 'exact' },
      delete: { state: 'exact' },
      terminate: { state: 'not_applicable', reason_code: 'concept_absent' },
    },
  },
]

const fixtureCaps = {
  agent_type: 'claude',
  adapter_revision: 1,
  status: Object.fromEntries(
    CAP_IDS.map(id => {
      if (id === 'tokens') return [id, { state: 'missing', reason_code: 'session_not_finalized' }]
      if (id === 'subtasks') return [id, { state: 'unsupported', reason_code: 'adapter_not_implemented' }]
      return [id, { state: 'exact' }]
    }),
  ),
  actions: {
    resume: { availability: 'available' },
    delete: { availability: 'unavailable', reason_code: 'session_running' },
    terminate: { availability: 'runtime_check_required', reason_code: 'runtime_check_required' },
  },
  liveness: {
    is_live: true,
    state: 'estimated',
    reason_code: 'timestamp_heuristic',
  },
}

function buildFixtureDetail() {
  const now = new Date().toISOString()
  return {
    id: 'fixture-cap-session',
    agent_type: 'claude',
    name: 'Capability fixture session',
    repository: '',
    branch: '',
    project: '',
    cwd: '/tmp',
    turn_count: 1,
    message_count: 2,
    is_live: true,
    bookmarked: false,
    created_at: now,
    updated_at: now,
    model_name: 'fixture-model',
    turns: [{
      turn_index: 0,
      user_message: 'hello',
      assistant_message: 'world',
      token_usage: {
        prompt_tokens: 0,
        completion_tokens: 0,
        cache_read_tokens: 0,
        cache_write_tokens: 0,
        premium_requests: 0,
      },
      tool_call_count: 0,
      error_count: 0,
      duration_ms: 10,
      events: [],
      tool_names: [],
      tool_details: [],
      skills: [],
      request_count: 1,
    }],
    billing: {
      precision: 'missing',
      totals: {
        prompt_tokens: 0,
        completion_tokens: 0,
        cache_read_tokens: 0,
        cache_write_tokens: 0,
        premium_requests: 0,
      },
    },
    agent_capabilities: fixtureCaps,
  }
}

function isSessionsList(url) {
  try {
    const u = new URL(url)
    const path = u.pathname.replace(/\/$/, '') || '/'
    return path === '/api/sessions'
  } catch {
    return false
  }
}

function isFixtureDetail(url) {
  try {
    const u = new URL(url)
    const path = u.pathname.replace(/\/$/, '')
    return path === '/api/sessions/fixture-cap-session'
  } catch {
    return false
  }
}

function isFixtureSubresource(url) {
  try {
    const u = new URL(url)
    return u.pathname.startsWith('/api/sessions/fixture-cap-session/')
  } catch {
    return false
  }
}

async function installSessionFixtures(page, fixtureDetail) {
  await page.route('**/api/agents', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(fixtureAgents),
    })
  })
  await page.route('**/api/bookmarks', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: '[]',
    })
  })
  // List must not swallow detail paths — match pathname exactly.
  await page.route(url => isSessionsList(url), async route => {
    if (route.request().method() !== 'GET') return route.continue()
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([{
        id: fixtureDetail.id,
        agent_type: fixtureDetail.agent_type,
        name: fixtureDetail.name,
        model_name: fixtureDetail.model_name,
        repository: '',
        branch: '',
        project: '',
        cwd: '/tmp',
        turn_count: 1,
        message_count: 2,
        is_live: true,
        bookmarked: false,
        created_at: fixtureDetail.created_at,
        updated_at: fixtureDetail.updated_at,
      }]),
    })
  })
  await page.route(url => isFixtureDetail(url), async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(fixtureDetail),
    })
  })
  await page.route(url => isFixtureSubresource(url), async route => {
    const path = new URL(route.request().url()).pathname
    // fetchPositions expects PositionsResponse body (not a status wrapper).
    if (path.endsWith('/positions')) {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          session_id: fixtureDetail.id,
          agent_type: fixtureDetail.agent_type,
          revision: 0,
          cols: 80,
          total_lines: 2,
          positions: [],
        }),
      })
      return
    }
    if (path.endsWith('/live-revision')) {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ revision: 0, is_live: true }),
      })
      return
    }
    if (path.endsWith('/render')) {
      await route.fulfill({
        status: 200,
        contentType: 'text/plain',
        body: 'hello\nworld\n',
      })
      return
    }
    // edits, tool-outputs, analytics, etc.
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: '[]',
    })
  })
}

async function runLocale(browser, locale) {
  const errors = []
  const page = await browser.newPage({ viewport: { width: 1400, height: 900 } })
  page.on('pageerror', e => errors.push(String(e)))
  page.on('console', msg => {
    if (msg.type() === 'error') errors.push(msg.text())
  })

  await page.addInitScript(loc => {
    localStorage.setItem('si-locale', loc)
  }, locale)

  await page.route('**/api/agents', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(fixtureAgents),
    })
  })

  await page.goto(ready + '/', { waitUntil: 'domcontentloaded', timeout: 60000 })
  await page.waitForSelector('button[data-session-id], [data-testid="settings-agents-tab"], body', { timeout: 15000 })
  await page.waitForTimeout(500)

  // --- Settings Agents tab ---
  const settingsName = locale === 'zh-CN' ? '设置' : 'Settings'
  await page.getByRole('button', { name: settingsName }).click()
  await page.waitForSelector('[role="dialog"]', { timeout: 10000 })
  const agentsTabName = locale === 'zh-CN' ? 'Agent 能力' : 'Agent capabilities'
  await page.locator('nav button').filter({ hasText: agentsTabName }).click()
  await page.waitForSelector('[data-testid="settings-agents-tab"]', { timeout: 10000 })

  for (const a of fixtureAgents) {
    await page.waitForSelector(`[data-testid="settings-agent-${a.type}"]`, { timeout: 8000 })
  }
  const copilotText = await page.locator('[data-testid="settings-agent-copilot"]').innerText()
  if (!/Not detected|未发现/.test(copilotText)) {
    throw new Error('copilot should show not-detected copy: ' + copilotText)
  }

  await page.locator('[data-testid="settings-agent-grok"]').click()
  await page.waitForSelector('[data-testid="settings-agent-detail"]', { timeout: 8000 })
  const termRow = page.locator('[data-testid="settings-cap-terminate"]')
  await termRow.scrollIntoViewIfNeeded()
  await termRow.waitFor({ state: 'visible', timeout: 10000 })
  const termText = await termRow.innerText()
  if (/[○◯]/.test(termText)) throw new Error('not_applicable used circle')
  if (!termText.includes('—') && !/Not applicable|不适用/.test(termText)) {
    throw new Error('terminate should present not_applicable: ' + termText)
  }
  const sub = await page.locator('[data-testid="settings-cap-subtasks"]').innerText()
  if (!/Unsupported|不支持|×/.test(sub)) {
    throw new Error('subtasks should present unsupported: ' + sub)
  }

  await page.locator('[data-testid="settings-agents-compare"]').click()
  await page.waitForSelector('[data-testid="agent-capability-compare"]', { timeout: 10000 })
  const table = page.locator('[data-testid="agent-capability-compare"] table')
  if (!(await table.count())) throw new Error('compare table missing')
  const bodyRows = page.locator('[data-testid="agent-capability-compare"] tbody tr')
  const rowCount = await bodyRows.count()
  if (rowCount !== 10) {
    throw new Error(`compare table rows=${rowCount} want 10`)
  }
  const tableText = await table.innerText()
  if (tableText.includes('○') || tableText.includes('◯')) {
    throw new Error('not_applicable must not use circle icons')
  }
  if (!tableText.includes('—') && !/Not applicable|不适用/.test(tableText)) {
    throw new Error('expected not_applicable presentation in compare table')
  }
  const box = await page.locator('[data-testid="agent-capability-compare"] > div').boundingBox()
  const vp = page.viewportSize()
  if (box && vp && box.width > vp.width + 2) {
    throw new Error(`compare dialog wider than viewport: ${box.width} > ${vp.width}`)
  }
  await page.screenshot({ path: join(outDir, `compare-${locale}.png`), fullPage: false })
  await page.keyboard.press('Escape')
  await page.waitForTimeout(150)
  // Close settings
  await page.keyboard.press('Escape')
  await page.waitForTimeout(200)
  await page.close()

  // --- Session path with synthetic agent_capabilities ---
  const fixtureDetail = buildFixtureDetail()
  const sessPage = await browser.newPage({ viewport: { width: 1400, height: 900 } })
  sessPage.on('pageerror', e => errors.push(String(e)))
  sessPage.on('console', msg => {
    if (msg.type() === 'error') errors.push(msg.text())
  })
  await sessPage.addInitScript(loc => {
    localStorage.setItem('si-locale', loc)
  }, locale)

  await installSessionFixtures(sessPage, fixtureDetail)

  const sessionsResp = sessPage.waitForResponse(
    r => isSessionsList(r.url()) && r.status() === 200,
    { timeout: 30000 },
  )
  await sessPage.goto(ready + '/', { waitUntil: 'domcontentloaded', timeout: 60000 })
  await sessionsResp

  const row = sessPage.locator('button[data-session-id="fixture-cap-session"]')
  try {
    await row.waitFor({ state: 'visible', timeout: 20000 })
  } catch (e) {
    const dump = await sessPage.locator('body').innerText().catch(() => '')
    const ids = await sessPage.locator('button[data-session-id]').evaluateAll(
      els => els.map(el => el.getAttribute('data-session-id')),
    ).catch(() => [])
    throw new Error(
      `fixture session row missing. ids=${JSON.stringify(ids)} body=${dump.slice(0, 500)}`,
    )
  }
  await row.click()

  const capBtn = sessPage.locator('[data-testid="session-agent-capability-button"]')
  await capBtn.waitFor({ state: 'visible', timeout: 20000 })

  // Token header must not show trustworthy "0 tokens" when capability is missing
  const tokenHeader = sessPage.locator('[data-testid="session-token-header"]')
  await tokenHeader.waitFor({ state: 'visible', timeout: 10000 })
  const headerText = await tokenHeader.innerText()
  if (/\b0\s+tokens\b/i.test(headerText) || /0\s+用量/.test(headerText)) {
    throw new Error('header must not show 0 tokens when missing: ' + headerText)
  }
  if (!/Tokens not recorded|用量未记录/.test(headerText)) {
    throw new Error('header should show tokens-not-recorded copy: ' + headerText)
  }

  await capBtn.click()
  const panel = sessPage.locator('[data-testid="session-capability-panel"]')
  await panel.waitFor({ state: 'visible', timeout: 15000 })

  const tokensRow = sessPage.locator('[data-testid="session-cap-row-tokens"]')
  await tokensRow.scrollIntoViewIfNeeded()
  await tokensRow.waitFor({ state: 'visible', timeout: 10000 })
  const tokensText = await tokensRow.innerText()
  if (!/Missing|缺失|!/.test(tokensText)) {
    throw new Error('tokens row should show missing: ' + tokensText)
  }

  const subRow = sessPage.locator('[data-testid="session-cap-row-subtasks"]')
  await subRow.scrollIntoViewIfNeeded()
  const subText = await subRow.innerText()
  if (!/Unsupported|不支持|×/.test(subText)) {
    throw new Error('subtasks should show unsupported: ' + subText)
  }

  await sessPage.locator('[data-testid="session-action-terminate"]').scrollIntoViewIfNeeded()
  const panelText = await panel.innerText()
  if (!/Liveness|活跃状态|活跃/.test(panelText)) {
    throw new Error('session panel missing liveness section: ' + panelText.slice(0, 300))
  }
  if (!/Checked when used|使用时检查/.test(panelText)) {
    throw new Error('expected runtime_check_required label: ' + panelText.slice(0, 400))
  }
  if (!/Estimated|估算/.test(panelText)) {
    throw new Error('expected estimated liveness quality in panel: ' + panelText.slice(0, 400))
  }

  await sessPage.screenshot({ path: join(outDir, `session-panel-${locale}.png`) })
  const rows = await sessPage.locator('[data-testid^="session-cap-row-"]').count()
  if (rows < 10) {
    throw new Error(`expected 10 capability rows, got ${rows}`)
  }
  await sessPage.keyboard.press('Escape')
  await sessPage.close()

  // --- Unmocked smoke against real backend ---
  const smoke = await browser.newPage({ viewport: { width: 1400, height: 900 } })
  smoke.on('pageerror', e => errors.push(String(e)))
  await smoke.addInitScript(loc => {
    localStorage.setItem('si-locale', loc)
  }, locale)
  await smoke.goto(ready + '/', { waitUntil: 'domcontentloaded', timeout: 60000 })
  await smoke.waitForTimeout(800)
  const first = smoke.locator('button[data-session-id]').first()
  if (await first.count()) {
    await first.click()
    const btn = smoke.locator('[data-testid="session-agent-capability-button"]')
    if (await btn.count()) {
      await btn.waitFor({ state: 'visible', timeout: 20000 })
      await btn.click()
      await smoke.locator('[data-testid="session-capability-panel"]').waitFor({ timeout: 10000 })
      await smoke.keyboard.press('Escape')
    }
  }
  await smoke.close()

  if (errors.length) {
    console.warn('console errors', locale, errors.slice(0, 8))
  }
  return { locale, errors }
}

const browser = await chromium.launch({ headless: true })
const results = []
try {
  for (const loc of ['en', 'zh-CN']) {
    results.push(await runLocale(browser, loc))
    console.log('locale ok', loc)
  }
} finally {
  await browser.close()
}

writeFileSync(join(outDir, 'report.json'), JSON.stringify({ ready, results, outDir }, null, 2))
console.log('capability UI validation passed', outDir)
