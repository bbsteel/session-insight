/**
 * Live Playwright check: project filter sort (name / sessions / recent + dir).
 * Not part of PR CI. Run from frontend/ against a started instance:
 *   BASE_URL=http://127.0.0.1:PORT node scripts/validate-project-sort.mjs
 */
import { chromium } from 'playwright'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const ROOT = path.resolve(__dirname, '..', '..')

const BASE_URL = (() => {
  if (process.env.BASE_URL) return process.env.BASE_URL.replace(/\/?$/, '/')
  const urlPath = path.join(ROOT, '.runtime', 'session-insight.url')
  try {
    return fs.readFileSync(urlPath, 'utf8').trim().replace(/\/?$/, '/')
  } catch {
    return 'http://127.0.0.1:8080/'
  }
})()

const OUT_DIR = path.join(ROOT, '.runtime', 'ui-project-sort')
fs.mkdirSync(OUT_DIR, { recursive: true })

function assert(cond, msg) {
  if (!cond) throw new Error(msg)
}

async function setLocale(page, locale) {
  await page.evaluate((loc) => {
    localStorage.setItem('si-locale', loc)
    localStorage.removeItem('si-project-sort')
  }, locale)
  await page.reload({ waitUntil: 'domcontentloaded' })
  await page.waitForSelector('header', { timeout: 15000 })
}

async function openProjectDropdown(page, allProjectsLabel, projectsLabel) {
  const trigger = page.locator('button[aria-haspopup="listbox"]').filter({ hasText: allProjectsLabel }).first()
  await trigger.waitFor({ state: 'visible', timeout: 30000 })
  await trigger.click()
  const listbox = page.getByRole('listbox', { name: projectsLabel })
  await listbox.waitFor({ state: 'visible', timeout: 5000 })
  return listbox
}

async function optionRows(listbox, allProjectsLabel) {
  const options = listbox.locator('[role="option"]')
  const n = await options.count()
  const rows = []
  for (let i = 0; i < n; i++) {
    const opt = options.nth(i)
    const spans = opt.locator('span')
    const name = (await spans.nth(1).innerText()).trim()
    const countText = (await spans.nth(2).innerText()).trim()
    const count = Number(countText)
    rows.push({ name, count })
  }
  // Drop the leading "All projects" row when present
  if (rows.length && rows[0].name === allProjectsLabel) return rows.slice(1)
  return rows
}

async function pageWaitStable() {
  await new Promise(r => setTimeout(r, 80))
}

async function runLocale(page, locale, labels) {
  console.log(`\n=== locale ${locale} ===`)
  await setLocale(page, locale)
  let listbox = await openProjectDropdown(page, labels.allProjects, labels.projectsLabel)

  const group = listbox.getByRole('group', { name: labels.sortLabel })
  await group.waitFor({ state: 'visible' })
  for (const lab of [labels.name, labels.sessions, labels.recent]) {
    assert(await group.getByRole('button', { name: lab, exact: true }).count() >= 1, `missing sort key: ${lab}`)
  }
  console.log('sort keys visible')

  let rows = await optionRows(listbox, labels.allProjects)
  assert(rows.length >= 2, `need ≥2 projects to verify sort, got ${rows.length}`)
  for (let i = 1; i < rows.length; i++) {
    const ok =
      rows[i - 1].count > rows[i].count ||
      (rows[i - 1].count === rows[i].count &&
        rows[i - 1].name.localeCompare(rows[i].name, undefined, { sensitivity: 'base' }) <= 0)
    assert(ok, `sessions desc violated at ${i}: ${JSON.stringify(rows.slice(0, 8))}`)
  }
  console.log(`default sessions desc ok (${rows.length} projects)`)

  const searchInput = listbox.getByPlaceholder(labels.searchProjects, { exact: true })
  const searchTerm = rows[0].name.trim()
  assert(searchTerm.length > 0, 'first project must have a searchable name')
  await searchInput.fill(searchTerm)
  await pageWaitStable()
  const filteredRows = await optionRows(listbox, labels.allProjects)
  const normalizedSearchTerm = searchTerm.toLowerCase()
  assert(filteredRows.length > 0, `project search returned no results for ${JSON.stringify(searchTerm)}`)
  assert(
    filteredRows.every(row => row.name.toLowerCase().includes(normalizedSearchTerm)),
    `project search returned a non-matching row: ${JSON.stringify(filteredRows)}`,
  )
  console.log(`project search ok (${filteredRows.length} matching project${filteredRows.length === 1 ? '' : 's'})`)
  await searchInput.fill('')
  await pageWaitStable()
  rows = await optionRows(listbox, labels.allProjects)
  assert(rows.length >= 2, 'clearing project search should restore the project list')

  const originalViewport = page.viewportSize()
  assert(originalViewport, 'expected a fixed viewport for resize validation')
  const resizedViewport = {
    width: Math.max(760, Math.min(originalViewport.width - 160, 1024)),
    height: Math.max(520, Math.min(originalViewport.height - 160, 720)),
  }
  await page.setViewportSize(resizedViewport)
  await pageWaitStable()
  const resizedPanelBox = await listbox.boundingBox()
  assert(resizedPanelBox, 'project panel should remain visible after viewport resize')
  assert(resizedPanelBox.x >= 0 && resizedPanelBox.y >= 0, `resized panel starts outside viewport: ${JSON.stringify(resizedPanelBox)}`)
  assert(
    resizedPanelBox.x + resizedPanelBox.width <= resizedViewport.width + 1 &&
      resizedPanelBox.y + resizedPanelBox.height <= resizedViewport.height + 1,
    `resized panel overflows viewport: ${JSON.stringify(resizedPanelBox)}`,
  )
  assert(resizedPanelBox.width <= 720 + 1, `resized panel exceeds its max width: ${JSON.stringify(resizedPanelBox)}`)
  const resizedTriggerBox = page.locator('button[aria-haspopup="listbox"]').filter({ hasText: labels.allProjects }).first()
  const resizedTriggerBounds = await resizedTriggerBox.boundingBox()
  assert(resizedTriggerBounds, 'project trigger should remain visible after viewport resize')
  assert(
    resizedPanelBox.x >= resizedTriggerBounds.x + resizedTriggerBounds.width - 1,
    `resized panel should stay beside its trigger: panel=${JSON.stringify(resizedPanelBox)} trigger=${JSON.stringify(resizedTriggerBounds)}`,
  )
  console.log(`viewport resize ok (${resizedViewport.width}×${resizedViewport.height})`)
  await page.setViewportSize(originalViewport)
  await pageWaitStable()

  await page.mouse.click(5, 5)
  await listbox.waitFor({ state: 'hidden' })
  assert(await page.getByRole('listbox', { name: labels.projectsLabel }).count() === 0, 'outside click should close the project panel')
  console.log('outside click closes panel')
  listbox = await openProjectDropdown(page, labels.allProjects, labels.projectsLabel)

  await group.getByRole('button', { name: labels.name, exact: true }).click()
  await pageWaitStable()
  rows = await optionRows(listbox, labels.allProjects)
  const nameAsc = rows.map(r => r.name)
  const expectedAsc = [...nameAsc].sort((a, b) => a.localeCompare(b, undefined, { sensitivity: 'base' }))
  assert(JSON.stringify(nameAsc) === JSON.stringify(expectedAsc), `name asc: got ${nameAsc.slice(0, 10)}`)
  console.log('name asc ok')

  // Direction toggle is the last control in the sort group (arrow button)
  const dirBtn = group.locator('button').last()
  await dirBtn.click()
  await pageWaitStable()
  rows = await optionRows(listbox, labels.allProjects)
  const nameDesc = rows.map(r => r.name)
  const expectedDesc = [...expectedAsc].reverse()
  assert(JSON.stringify(nameDesc) === JSON.stringify(expectedDesc), `name desc: got ${nameDesc.slice(0, 10)}`)
  console.log('name desc ok')

  await group.getByRole('button', { name: labels.recent, exact: true }).click()
  await pageWaitStable()
  rows = await optionRows(listbox, labels.allProjects)
  assert(rows.length >= 2, 'recent: still have projects')
  assert(
    await group.getByRole('button', { name: labels.recent, exact: true }).getAttribute('aria-pressed') === 'true',
    'recent should be pressed',
  )
  console.log('recent sort selected')

  await group.getByRole('button', { name: labels.sessions, exact: true }).click()
  await pageWaitStable()
  rows = await optionRows(listbox, labels.allProjects)
  for (let i = 1; i < rows.length; i++) {
    const ok =
      rows[i - 1].count > rows[i].count ||
      (rows[i - 1].count === rows[i].count &&
        rows[i - 1].name.localeCompare(rows[i].name, undefined, { sensitivity: 'base' }) <= 0)
    assert(ok, `sessions desc after reselect violated at ${i}`)
  }
  console.log('sessions desc after reselect ok')

  await page.keyboard.press('Escape')
  await page.waitForTimeout(150)
  const listbox2 = await openProjectDropdown(page, labels.allProjects, labels.projectsLabel)
  const sessionsBtn = listbox2.getByRole('group', { name: labels.sortLabel }).getByRole('button', { name: labels.sessions, exact: true })
  assert(await sessionsBtn.getAttribute('aria-pressed') === 'true', 'sessions pref should persist')
  console.log('sort pref persisted across reopen')

  const shot = path.join(OUT_DIR, `project-sort-${locale}.png`)
  await page.screenshot({ path: shot })
  console.log(`screenshot: ${shot}`)
}

async function main() {
  const browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } })
  const page = await context.newPage()
  page.setDefaultTimeout(15000)

  console.log(`Navigating to ${BASE_URL}`)
  await page.goto(BASE_URL, { waitUntil: 'domcontentloaded' })
  await page.waitForSelector('header', { timeout: 20000 })
  console.log('app loaded')

  // Wait until the project filter trigger appears (needs sessions with project field)
  await page.waitForFunction(() => {
    return [...document.querySelectorAll('button[aria-haspopup="listbox"]')]
      .some(b => /All projects|全部项目/.test(b.textContent || ''))
  }, { timeout: 90000 })

  await runLocale(page, 'en', {
    allProjects: 'All projects',
    searchProjects: 'Search projects…',
    projectsLabel: 'Filter sessions by project',
    sortLabel: 'Sort',
    name: 'Name',
    sessions: 'Sessions',
    recent: 'Recent',
  })

  await runLocale(page, 'zh-CN', {
    allProjects: '全部项目',
    searchProjects: '搜索项目…',
    projectsLabel: '按项目筛选会话',
    sortLabel: '排序',
    name: '名称',
    sessions: '会话数',
    recent: '最近活跃',
  })

  await browser.close()
  console.log('\nvalidate-project-sort: PASS')
}

main().catch((err) => {
  console.error('validate-project-sort: FAIL', err)
  process.exit(1)
})
