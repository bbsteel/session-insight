// Live regression check: hermes session tool results must not render the
// <untrusted_tool_result> envelope or literal \uXXXX mojibake; binary garbage
// must collapse to an omission marker. Throwaway — depends on the local
// hermes session 20260811_120636_00664e.
// Run after ./run.sh all: node .runtime/validate-hermes-mojibake.mjs
import { createRequire } from 'node:module'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const require = createRequire(path.join(repoRoot, 'frontend/package.json'))
const { chromium } = require('playwright')

const BASE_URL = process.env.SI_URL || fs.readFileSync(path.join(repoRoot, '.runtime/session-insight.url'), 'utf8').trim()
const SESSION_ID = '20260811_120636_00664e'
const SHOT_DIR = '/tmp/session-insight-ui'

let failures = 0
function check(name, condition, detail = '') {
  if (condition) console.log(`PASS: ${name}`)
  else {
    failures++
    console.error(`FAIL: ${name}${detail ? ` — ${detail}` : ''}`)
  }
}

for (const locale of ['zh-CN', 'en']) {
  const browser = await chromium.launch()
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const page = await ctx.newPage()
  const consoleErrors = []
  page.on('console', msg => { if (msg.type() === 'error') consoleErrors.push(msg.text()) })
  // The frontend probes /collaboration for every session; agents without
  // collaboration data answer 404 by design — not a regression signal.
  page.on('response', r => {
    if (r.status() >= 400 && !r.url().includes('/collaboration')) consoleErrors.push(`${r.status()} ${r.url()}`)
  })
  await page.addInitScript(l => window.localStorage.setItem('si-locale', l), locale)

  await page.goto(BASE_URL, { waitUntil: 'domcontentloaded' })
  await page.waitForTimeout(2_000)
  const filter = page.locator('input[placeholder*="过滤会话"], input[placeholder*="Filter"], input[placeholder*="Search sessions"]').first()
  await filter.waitFor({ state: 'visible', timeout: 15_000 })
  await filter.fill(SESSION_ID)
  const row = page.locator(`[data-session-id="${SESSION_ID}"]`)
  await row.waitFor({ state: 'visible', timeout: 15_000 })
  await row.click()
  await page.locator('.xterm-viewport').waitFor({ state: 'visible', timeout: 30_000 })
  await page.waitForTimeout(2_500)

  // Viewport-visible rows must never show the envelope or literal escapes.
  const visibleText = await page.locator('.xterm-screen').textContent() ?? ''
  check(`[${locale}] viewport has no envelope tag`, !visibleText.includes('<untrusted_tool_result'))
  check(`[${locale}] viewport has no literal \\u00 escapes`, !/\\u00[0-1][0-9a-f]/.test(visibleText))

  // Full-buffer check via the render API: the complete ANSI stream must be
  // clean and contain the omission marker. This avoids relying on the in-
  // terminal search bar focus, which is flaky under automation.
  const renderURL = new URL(`/api/sessions/${SESSION_ID}/render?agent=hermes&cols=200`, BASE_URL).toString()
  const ansi = await (await page.request.get(renderURL)).text()
  const text = ansi.replace(/\x1b\[[0-9;]*m/g, '')
  check(`[${locale}] render API has no envelope`, !text.includes('<untrusted_tool_result'))
  check(`[${locale}] render API has no C0 literal escapes`, !/\\u00[0-1][0-9a-f]/.test(text))
  check(`[${locale}] render API contains omission marker`, text.includes('[binary data omitted:'))

  fs.mkdirSync(SHOT_DIR, { recursive: true })
  const shot = path.join(SHOT_DIR, `hermes-mojibake-${locale}.png`)
  await page.screenshot({ path: shot, fullPage: false })
  console.log(`[${locale}] screenshot: ${shot}`)
  const realErrors = consoleErrors.filter(e => !e.includes('/collaboration') && !e.includes('404'))
  check(`[${locale}] no unexpected console/network errors`, realErrors.length === 0, realErrors.slice(0, 2).join(' | '))
  await browser.close()
}

console.log(failures === 0 ? 'ALL CHECKS PASSED' : `${failures} CHECK(S) FAILED`)
process.exit(failures === 0 ? 0 : 1)
