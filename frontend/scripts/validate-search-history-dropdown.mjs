// Live check: global search history dropdown stays above the session toolbar.
// Run after ./run.sh all from the worktree root:
//   node frontend/scripts/validate-search-history-dropdown.mjs
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

const HISTORY = [
  { query: '--dangerously-bypass-approvals-and-sandbox', pinned: false, ts: Date.now() },
  { query: 'sandbox', pinned: false, ts: Date.now() - 1000 },
  { query: 'apple', pinned: false, ts: Date.now() - 2000 },
]

let failures = 0
function check(name, condition, detail = '') {
  if (condition) console.log(`PASS: ${name}`)
  else {
    failures++
    console.error(`FAIL: ${name}${detail ? ` — ${detail}` : ''}`)
  }
}

function overlapArea(a, b) {
  const x = Math.max(0, Math.min(a.right, b.right) - Math.max(a.left, b.left))
  const y = Math.max(0, Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top))
  return x * y
}

async function chooseSession(page) {
  const fromEnv = typeof process.env.SI_SESSION_ID === 'string' ? process.env.SI_SESSION_ID.trim() : ''
  const fromEnvAgent = typeof process.env.SI_AGENT_TYPE === 'string' ? process.env.SI_AGENT_TYPE.trim() : ''
  const deadline = Date.now() + 90_000
  let lastCount = 0
  while (Date.now() < deadline) {
    const summaries = await (await page.request.get(new URL('/api/sessions', BASE_URL).toString())).json()
    lastCount = Array.isArray(summaries) ? summaries.length : 0
    const ranked = [...(summaries ?? [])]
      .filter(s => !s.is_live && (s.turn_count ?? s.message_count ?? 0) > 0)
      .sort((a, b) => (b.message_count ?? b.turn_count ?? 0) - (a.message_count ?? a.turn_count ?? 0))
    if (fromEnv) {
      const match = (summaries ?? []).find(s => s.id === fromEnv)
      if (match?.agent_type) return { id: match.id, agentType: match.agent_type }
      if (fromEnvAgent) return { id: fromEnv, agentType: fromEnvAgent }
    } else if (ranked.length && ranked[0].agent_type) {
      return { id: ranked[0].id, agentType: ranked[0].agent_type }
    }
    await page.waitForTimeout(2_000)
  }
  throw new Error(`No recorded sessions available after wait (last count=${lastCount})`)
}

async function openSession(page, session) {
  const hash = `#/session/${encodeURIComponent(session.agentType)}/${encodeURIComponent(session.id)}`
  await page.goto(new URL(hash, BASE_URL).toString(), { waitUntil: 'domcontentloaded' })
  await page.locator('[data-testid="session-toolbar"]').waitFor({ state: 'visible', timeout: 60_000 })
}

async function seedAndReload(page, locale) {
  await page.addInitScript(({ history, locale }) => {
    localStorage.setItem('search-history', JSON.stringify(history))
    localStorage.setItem('si-locale', locale)
  }, { history: HISTORY, locale })
}

async function assertDropdownAboveToolbar(page, locale) {
  const input = page.locator('[data-testid="global-search-input"]')
  await input.waitFor({ state: 'visible', timeout: 10_000 })
  await input.click()
  const dropdown = page.locator('[data-testid="global-search-dropdown"]')
  await dropdown.waitFor({ state: 'visible', timeout: 5_000 })

  const recent = page.locator('[data-testid="global-search-recent-label"]')
  await recent.waitFor({ state: 'visible', timeout: 5_000 })
  const recentText = (await recent.innerText()).trim()
  const expectedRecent = locale === 'zh-CN' ? '最近搜索' : 'Recent searches'
  check(`${locale} recent label`, recentText.toLocaleLowerCase() === expectedRecent.toLocaleLowerCase(), `got "${recentText}"`)

  const firstItem = dropdown.locator('button', { hasText: HISTORY[0].query })
  await firstItem.waitFor({ state: 'visible', timeout: 5_000 })
  check(`${locale} first history item visible`, await firstItem.isVisible())

  const [inputBox, dropdownBox, recentBox, itemBox, toolbarBox] = await Promise.all([
    input.boundingBox(),
    dropdown.boundingBox(),
    recent.boundingBox(),
    firstItem.boundingBox(),
    page.locator('[data-testid="session-toolbar"]').boundingBox(),
  ])

  check(`${locale} boxes present`, !!(inputBox && dropdownBox && recentBox && itemBox && toolbarBox),
    JSON.stringify({ inputBox, dropdownBox, recentBox, itemBox, toolbarBox }))
  if (!inputBox || !dropdownBox || !recentBox || !itemBox || !toolbarBox) return

  check(`${locale} dropdown below input`, dropdownBox.y >= inputBox.y + inputBox.height - 2,
    `dropdown.y=${dropdownBox.y} input.bottom=${inputBox.y + inputBox.height}`)
  check(`${locale} dropdown has size`, dropdownBox.width > 100 && dropdownBox.height > 40,
    JSON.stringify(dropdownBox))

  const dropdownVsToolbar = overlapArea(
    { left: dropdownBox.x, right: dropdownBox.x + dropdownBox.width, top: dropdownBox.y, bottom: dropdownBox.y + dropdownBox.height },
    { left: toolbarBox.x, right: toolbarBox.x + toolbarBox.width, top: toolbarBox.y, bottom: toolbarBox.y + toolbarBox.height },
  )
  check(`${locale} dropdown overlaps toolbar (expected overlay)`, dropdownVsToolbar > 0,
    `overlap=${dropdownVsToolbar} — dropdown should paint over the toolbar, not stop below it`)

  const hitAt = async (locator) => locator.evaluate(el => {
    const r = el.getBoundingClientRect()
    const x = r.left + Math.min(40, r.width / 2)
    const y = r.top + r.height / 2
    const hit = document.elementFromPoint(x, y)
    const toolbar = document.querySelector('[data-testid="session-toolbar"]')
    const dropdown = document.querySelector('[data-testid="global-search-dropdown"]')
    return {
      tag: hit?.tagName ?? null,
      testid: hit instanceof HTMLElement ? hit.dataset.testid ?? '' : '',
      inDropdown: !!(hit && dropdown && dropdown.contains(hit)),
      inToolbar: !!(hit && toolbar && toolbar.contains(hit)),
    }
  })
  const recentHit = await hitAt(recent)
  const itemHit = await hitAt(firstItem)
  check(`${locale} recent label hit-testable`, recentHit.inDropdown && !recentHit.inToolbar, JSON.stringify(recentHit))
  check(`${locale} first item hit-testable`, itemHit.inDropdown && !itemHit.inToolbar, JSON.stringify(itemHit))

  const shot = path.join(SHOT_DIR, `search-history-dropdown-${locale}.png`)
  await page.screenshot({ path: shot })
  console.log(`Screenshot saved: ${shot}`)
}

const browser = await chromium.launch({ headless: true })
fs.mkdirSync(SHOT_DIR, { recursive: true })

try {
  for (const locale of ['en', 'zh-CN']) {
    const context = await browser.newContext({ viewport: { width: 1400, height: 900 } })
    const page = await context.newPage()
    const errors = []
    page.on('pageerror', err => errors.push(String(err)))
    await seedAndReload(page, locale)
    const session = await chooseSession(page)
    console.log(`locale=${locale} sessionSelected=true`)
    await openSession(page, session)
    await assertDropdownAboveToolbar(page, locale)
    check(`${locale} no page errors`, errors.length === 0, errors.join('; '))
    await context.close()
  }
} catch (err) {
  failures++
  console.error('FAIL: script', err)
} finally {
  await browser.close()
}

if (failures > 0) {
  console.error(`\n${failures} check(s) failed`)
  process.exit(1)
}
console.log('\nAll search-history dropdown checks passed')
