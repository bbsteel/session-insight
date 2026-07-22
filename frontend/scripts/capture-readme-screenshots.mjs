import { execFileSync } from 'node:child_process'
import { mkdirSync } from 'node:fs'
import { homedir } from 'node:os'
import { basename, dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright'

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../..')

function readOptions(argv) {
  const options = new Map()
  for (let i = 0; i < argv.length; i++) {
    const key = argv[i]
    if (!key.startsWith('--')) throw new Error(`Unexpected argument: ${key}`)
    if (key === '--help') {
      options.set(key, 'true')
      continue
    }
    const value = argv[++i]
    if (!value || value.startsWith('--')) throw new Error(`Missing value for ${key}`)
    options.set(key, value)
  }
  return options
}

const options = readOptions(process.argv.slice(2))
if (options.has('--help')) {
  console.log(`Usage:
  npm --prefix frontend run capture:screenshots -- --session-title "<title>"

Options:
  --session-title  Exact title of the local session to capture (required)
  --base-url       Running Session Insight URL (default: http://127.0.0.1:8080)
  --locale         Interface locale: en or zh-CN (default: en)
  --public-root    Path displayed instead of the real repository path
                   (default: /workspace/session-insight)`)
  process.exit(0)
}

const sessionTitle = options.get('--session-title')
if (!sessionTitle) throw new Error('--session-title is required; run with --help for usage')

const baseURL = options.get('--base-url') ?? 'http://127.0.0.1:8080'
const locale = options.get('--locale') ?? 'en'
if (locale !== 'en' && locale !== 'zh-CN') throw new Error('--locale must be en or zh-CN')
const publicRoot = options.get('--public-root') ?? '/workspace/session-insight'
const privateHome = homedir()
const privateUsername = basename(privateHome)
const publicHome = '/home/user'
const outputDir = resolve(repoRoot, 'assets/screenshots', locale)
mkdirSync(outputDir, { recursive: true })

const copy = locale === 'en' ? {
  filter: /Filter sessions/,
  analytics: 'Analytics',
  interactionClose: 'Close messages panel',
  interactionTitle: 'Messages',
  jumpPrefix: 'Jump to terminal line',
  settings: 'Settings',
  fonts: 'Fonts',
  uiFont: 'Interface font',
  find: 'Find',
} : {
  filter: /过滤会话/,
  analytics: '分析',
  interactionClose: '关闭交互消息面板',
  interactionTitle: '交互消息',
  jumpPrefix: '跳转到终端第',
  settings: '设置',
  fonts: '字体',
  uiFont: '界面字体',
  find: '查找',
}

let trackedRootEntries = null
try {
  trackedRootEntries = new Set(
    execFileSync('git', ['ls-files'], { cwd: repoRoot, encoding: 'utf8' })
      .split('\n')
      .filter(Boolean)
      .map(path => path.split('/', 1)[0]),
  )
} catch {
  // A source archive has no .git directory. The screenshots still work; the
  // file tree simply is not restricted to tracked repository entries.
}

function escapedPath(path) {
  return path.replaceAll('/', '\\/')
}

function sanitized(text) {
  return text
    .replaceAll(escapedPath(repoRoot), escapedPath(publicRoot))
    .replaceAll(escapedPath(privateHome), escapedPath(publicHome))
    .replaceAll(repoRoot, publicRoot)
    .replaceAll(privateHome, publicHome)
    // ANSI styling can split a path around escape sequences before it reaches
    // the browser. Replacing the username itself closes that remaining gap.
    .replaceAll(privateUsername, 'user')
    .replace(/[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}/gi, 'developer@example.com')
}

async function installSanitizer(page) {
  await page.route('**/api/**', async route => {
    const request = route.request()
    if (request.resourceType() === 'eventsource') {
      await route.abort()
      return
    }

    const upstreamURL = request.url().replaceAll(
      encodeURIComponent(publicRoot),
      encodeURIComponent(repoRoot),
    )
    const response = await route.fetch({ url: upstreamURL })
    let body = sanitized(await response.text())
    const upstream = new URL(upstreamURL)
    if (
      trackedRootEntries
      && upstream.pathname === '/api/fs/list'
      && upstream.searchParams.get('dir') === repoRoot
    ) {
      body = JSON.stringify(
        JSON.parse(body).filter(entry => trackedRootEntries.has(entry.name)),
      )
    }
    await route.fulfill({ response, body })
  })
}

const browser = await chromium.launch({ headless: true })
try {
  const context = await browser.newContext({
    viewport: { width: 1600, height: 1000 },
    deviceScaleFactor: 1,
    colorScheme: 'light',
  })
  await context.addInitScript(selectedLocale => {
    localStorage.setItem('sidebar-width', '300')
    localStorage.setItem('recap-theme', 'light')
    localStorage.setItem('si-locale', selectedLocale)
    // Screenshots use a stable session snapshot, so keep the UI in the
    // connected state without opening the live SSE stream.
    class ScreenshotEventSource {
      onopen = null
      onerror = null

      constructor() {
        queueMicrotask(() => this.onopen?.())
      }

      addEventListener() {}
      close() {}
    }
    Object.defineProperty(window, 'EventSource', { value: ScreenshotEventSource })
  }, locale)

  const replay = await context.newPage()
  await installSanitizer(replay)
  await replay.goto(baseURL, { waitUntil: 'domcontentloaded' })
  await replay.getByPlaceholder(copy.filter).fill(sessionTitle)
  await replay.getByText(sessionTitle, { exact: true }).first().click()
  await replay.getByRole('button', { name: copy.analytics, exact: true }).waitFor()
  await replay.waitForTimeout(1800)
  const interactionPanel = replay.locator(`aside:has(button[aria-label="${copy.interactionClose}"])`)
  if (await interactionPanel.isVisible()) {
    await interactionPanel.getByRole('button', { name: copy.interactionClose }).click()
  }
  await replay.screenshot({
    path: resolve(outputDir, 'replay.png'),
    clip: { x: 0, y: 0, width: 1600, height: 640 },
  })

  await replay.locator(`button[title="${copy.interactionTitle}"]`).click()
  await interactionPanel.waitFor({ state: 'visible' })
  await interactionPanel.locator(`div[title^="${copy.jumpPrefix}"]`).first().waitFor()
  await replay.screenshot({ path: resolve(outputDir, 'interaction.png') })
  await interactionPanel.getByRole('button', { name: copy.interactionClose }).click()

  await replay.getByRole('button', { name: copy.analytics, exact: true }).click()
  await replay.waitForTimeout(1800)
  await replay.screenshot({ path: resolve(outputDir, 'analytics.png') })

  const settings = await context.newPage()
  await installSanitizer(settings)
  await settings.goto(baseURL, { waitUntil: 'domcontentloaded' })
  await settings.getByPlaceholder(copy.filter).fill(sessionTitle)
  await settings.getByRole('button', { name: copy.settings, exact: true }).click()
  const settingsDialog = settings.getByRole('dialog')
  await settingsDialog.getByRole('button', { name: copy.fonts, exact: true }).click()
  await settingsDialog.getByText(copy.uiFont, { exact: true }).waitFor()
  await settings.screenshot({ path: resolve(outputDir, 'settings.png') })

  const reader = await context.newPage()
  await installSanitizer(reader)
  const file = `${publicRoot}/README.md`
  const fileRoute = `#/file?path=${encodeURIComponent(file)}&cwd=${encodeURIComponent(publicRoot)}&line=20`
  await reader.goto(`${baseURL}/${fileRoute}`, { waitUntil: 'domcontentloaded' })
  await reader.getByRole('button', { name: copy.find, exact: true }).waitFor()
  await reader.waitForTimeout(2200)
  await reader.screenshot({ path: resolve(outputDir, 'code-reader.png') })

  await Promise.all([
    replay.unrouteAll({ behavior: 'wait' }),
    settings.unrouteAll({ behavior: 'wait' }),
    reader.unrouteAll({ behavior: 'wait' }),
  ])
} finally {
  await browser.close()
}

console.log(`Wrote screenshots to ${outputDir}`)
