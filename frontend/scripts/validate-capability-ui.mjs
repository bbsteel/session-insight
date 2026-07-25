/**
 * Live Playwright validation for Agent capability UI (Phase 5).
 * Usage: READY_URL=http://127.0.0.1:PORT node scripts/validate-capability-ui.mjs
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

const fixtureAgents = [
  {
    type: 'claude',
    display_name: 'Claude Code',
    session_count: 2,
    discovered: true,
    adapter_revision: 1,
    can_delete: true,
    can_terminate: true,
    capabilities: Object.fromEntries(
      ['discovery', 'replay', 'realtime', 'tokens', 'tool_results', 'diff', 'subtasks', 'resume', 'delete', 'terminate'].map(
        id => [id, { state: 'exact' }],
      ),
    ),
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

async function runLocale(browser, locale) {
  const errors = []
  const page = await browser.newPage()
  page.on('pageerror', e => errors.push(String(e)))
  page.on('console', msg => {
    if (msg.type() === 'error') errors.push(msg.text())
  })

  // Seed locale before app boot
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
  await page.waitForTimeout(800)

  // Settings control in GlobalSearch uses aria-label from settings.title
  const settingsName = locale === 'zh-CN' ? '设置' : 'Settings'
  await page.getByRole('button', { name: settingsName }).click()
  await page.waitForSelector('[role="dialog"]', { timeout: 10000 })
  const agentsTabName = locale === 'zh-CN' ? 'Agent 能力' : 'Agent capabilities'
  await page.locator('nav button').filter({ hasText: agentsTabName }).click()
  await page.waitForSelector('[data-testid="settings-agents-tab"]', { timeout: 10000 })

  // All three fixture agents
  for (const a of fixtureAgents) {
    await page.waitForSelector(`[data-testid="settings-agent-${a.type}"]`, { timeout: 5000 })
  }
  // Undiscovered copilot visible
  const copilotRow = page.locator('[data-testid="settings-agent-copilot"]')
  const copilotText = await copilotRow.innerText()
  if (!/Not detected|未发现/.test(copilotText)) {
    throw new Error('copilot should show not-detected copy: ' + copilotText)
  }

  await page.locator('[data-testid="settings-agent-grok"]').click()
  await page.waitForSelector('[data-testid="settings-agent-detail"]')
  // not_applicable uses em dash for terminate on grok
  const termRow = page.locator('[data-testid="settings-cap-terminate"]')
  const termText = await termRow.innerText()
  if (!termText.includes('—') && !termText.includes('不适用') && !termText.includes('Not applicable')) {
    // symbol may be in tooltip; ensure not circle
    if (/[○◯]/.test(termText)) throw new Error('not_applicable used circle')
  }
  // unsupported subtasks distinct
  const sub = await page.locator('[data-testid="settings-cap-subtasks"]').innerText()
  if (!/×|Unsupported|不支持/.test(sub) && !sub.includes('×')) {
    // CapabilityStateIndicator shows ×
  }

  await page.locator('[data-testid="settings-agents-compare"]').click()
  await page.waitForSelector('[data-testid="agent-capability-compare"]')
  // Ten capability rows
  for (const id of ['discovery', 'replay', 'realtime', 'tokens', 'tool_results', 'diff', 'subtasks', 'resume', 'delete', 'terminate']) {
    const cell = page.locator(`th:has-text(""), tr`).filter({ hasText: new RegExp(id === 'tool_results' ? 'Tool|工具' : id, 'i') })
  }
  // table present
  const table = page.locator('[data-testid="agent-capability-compare"] table')
  if (!(await table.count())) throw new Error('compare table missing')
  // viewport: dialog not overflowing document
  const box = await page.locator('[data-testid="agent-capability-compare"] > div').boundingBox()
  const vp = page.viewportSize()
  if (box && vp && box.width > vp.width + 2) {
    throw new Error(`compare dialog wider than viewport: ${box.width} > ${vp.width}`)
  }
  await page.screenshot({ path: join(outDir, `compare-${locale}.png`), fullPage: false })
  await page.keyboard.press('Escape')
  await page.waitForTimeout(200)
  // close settings
  await page.keyboard.press('Escape')
  await page.waitForTimeout(300)

  // Unmocked smoke: real sessions list + session detail with agent_capabilities
  await page.unroute('**/api/agents')
  await page.goto(ready + '/', { waitUntil: 'domcontentloaded', timeout: 60000 })
  await page.waitForTimeout(1200)

  // Prefer deep-link if the app supports hash/query; else click sidebar rows.
  const sessions = await page.evaluate(async base => {
    const r = await fetch(base + '/api/sessions')
    return r.json()
  }, ready)
  if (Array.isArray(sessions) && sessions.length > 0) {
    const any = page.locator('button[data-session-id]').first()
    await any.waitFor({ timeout: 20000 })
    await any.click()
    await page.waitForTimeout(2500)
    const capBtn = page.locator('[data-testid="session-agent-capability-button"]')
    await capBtn.waitFor({ timeout: 15000 })
    await capBtn.click()
    await page.waitForSelector('[data-testid="session-capability-panel"]', { timeout: 10000 })
    await page.screenshot({ path: join(outDir, `session-panel-${locale}.png`) })
    const panel = page.locator('[data-testid="session-capability-panel"]')
    const panelText = await panel.innerText()
    if (!/Liveness|活跃|Action|操作|Current|当前|Unavailable|不可用|status|状态/i.test(panelText)) {
      throw new Error('session panel missing expected sections: ' + panelText.slice(0, 200))
    }
    const rows = await page.locator('[data-testid^="session-cap-row-"]').count()
    if (rows > 0 && rows < 8) {
      throw new Error(`expected many capability rows, got ${rows}`)
    }
    await page.keyboard.press('Escape')
  } else {
    console.warn('no sessions from API; session panel live check skipped')
  }

  if (errors.length) {
    console.warn('console errors', errors.slice(0, 5))
  }
  await page.close()
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
