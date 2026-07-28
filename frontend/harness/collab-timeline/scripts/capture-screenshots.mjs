/**
 * Screenshot matrix for the production CollaborationTimeline harness.
 * Writes disposable PNGs (default /tmp/session-insight-ui/collab-timeline).
 *
 * Usage:
 *   node frontend/harness/collab-timeline/scripts/capture-screenshots.mjs
 */

import { chromium } from 'playwright'
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { buildHarness, serveHarness, SCREENSHOT_DIR } from './lib.mjs'

const SHOTS = [
  // 1440x900: typical/stress x light/dark x en/zh coverage.
  { name: 'typical-dark-en-1440x900', dataset: 'typical', theme: 'dark', lang: 'en', width: 1440, height: 900 },
  { name: 'typical-light-en-1440x900', dataset: 'typical', theme: 'light', lang: 'en', width: 1440, height: 900 },
  { name: 'typical-dark-zh-1440x900', dataset: 'typical', theme: 'dark', lang: 'zh', width: 1440, height: 900 },
  { name: 'typical-light-zh-1440x900', dataset: 'typical', theme: 'light', lang: 'zh', width: 1440, height: 900 },
  { name: 'stress-dark-en-1440x900', dataset: 'stress', theme: 'dark', lang: 'en', width: 1440, height: 900 },
  { name: 'stress-light-zh-1440x900', dataset: 'stress', theme: 'light', lang: 'zh', width: 1440, height: 900 },
  // 1280x720 spot checks.
  { name: 'typical-dark-en-1280x720', dataset: 'typical', theme: 'dark', lang: 'en', width: 1280, height: 720 },
  { name: 'typical-light-zh-1280x720', dataset: 'typical', theme: 'light', lang: 'zh', width: 1280, height: 720 },
  { name: 'stress-dark-en-1280x720', dataset: 'stress', theme: 'dark', lang: 'en', width: 1280, height: 720 },
]

async function main() {
  buildHarness()
  const { server, url } = await serveHarness()
  mkdirSync(SCREENSHOT_DIR, { recursive: true })
  const browser = await chromium.launch()
  try {
    for (const shot of SHOTS) {
      const page = await browser.newPage({ viewport: { width: shot.width, height: shot.height } })
      await page.goto(`${url}?dataset=${shot.dataset}&theme=${shot.theme}&lang=${shot.lang}`)
      await page.waitForFunction(() => window.__collab?.ready)
      await page.waitForTimeout(120)
      const file = join(SCREENSHOT_DIR, `${shot.name}.png`)
      await page.screenshot({ path: file })
      console.log(`captured ${file}`)
      await page.close()
    }

    // Selected causal path (typical, dark, en).
    const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
    await page.goto(`${url}?dataset=typical&theme=dark&lang=en`)
    await page.waitForFunction(() => window.__collab?.ready)
    await page.evaluate(() => window.__collab.selectRow(4))
    await page.waitForTimeout(80)
    const selectedFile = join(SCREENSHOT_DIR, 'selected-causal-path-dark-en-1440x900.png')
    await page.screenshot({ path: selectedFile })
    console.log(`captured ${selectedFile}`)

    // Collapsed branch (typical, dark, zh).
    await page.evaluate(() => window.__collab.setLang('zh'))
    await page.waitForFunction(() => window.__collab?.ready)
    await page.evaluate(() => window.__collab.collapseFirstBranch())
    await page.waitForTimeout(80)
    const collapsedFile = join(SCREENSHOT_DIR, 'collapsed-branch-dark-zh-1440x900.png')
    await page.screenshot({ path: collapsedFile })
    console.log(`captured ${collapsedFile}`)
    await page.close()
  } finally {
    await browser.close()
    server.close()
  }
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
