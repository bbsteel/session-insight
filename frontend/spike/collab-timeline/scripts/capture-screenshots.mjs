/**
 * Screenshot matrix for the spike. Screenshots are disposable review artifacts
 * written outside the repository (default /tmp/session-insight-ui/collab-spike).
 *
 * Usage:
 *   node frontend/spike/collab-timeline/scripts/capture-screenshots.mjs
 */

import { chromium } from 'playwright'
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { buildHarness, SCREENSHOT_DIR, serveSpike } from './lib.mjs'

const SHOTS = [
  { name: 'collapsed-dock-1440x900', q: 'renderer=svg&dataset=typical&dock=collapsed', w: 1440, h: 900 },
  { name: 'expanded-typical-svg-dark-en-1440x900', q: 'renderer=svg&dataset=typical', w: 1440, h: 900 },
  { name: 'expanded-typical-svg-light-en-1440x900', q: 'renderer=svg&dataset=typical&theme=light', w: 1440, h: 900 },
  { name: 'expanded-typical-svg-dark-zh-1440x900', q: 'renderer=svg&dataset=typical&lang=zh', w: 1440, h: 900 },
  { name: 'expanded-typical-canvas-dark-en-1440x900', q: 'renderer=canvas&dataset=typical', w: 1440, h: 900 },
  { name: 'stress-200lane-svg-dark-1440x900', q: 'renderer=svg&dataset=stress', w: 1440, h: 900 },
  { name: 'stress-200lane-canvas-dark-1440x900', q: 'renderer=canvas&dataset=stress', w: 1440, h: 900 },
  { name: 'typical-svg-dark-en-1280x720', q: 'renderer=svg&dataset=typical', w: 1280, h: 720 },
  { name: 'typical-canvas-dark-en-1280x720', q: 'renderer=canvas&dataset=typical', w: 1280, h: 720 },
]

async function main() {
  buildHarness()
  const { server, url } = await serveSpike()
  mkdirSync(SCREENSHOT_DIR, { recursive: true })
  const browser = await chromium.launch()
  const paths = []
  try {
    for (const shot of SHOTS) {
      const page = await browser.newPage({ viewport: { width: shot.w, height: shot.h } })
      await page.goto(`${url}?${shot.q}`)
      await page.waitForFunction(() => window.__spike?.ready)
      await page.waitForTimeout(150)
      const path = join(SCREENSHOT_DIR, `${shot.name}.png`)
      await page.screenshot({ path })
      paths.push(path)
      console.log(path)
      await page.close()
    }

    // Selected causal path (deep chain) with running + failure states, zoomed.
    for (const renderer of ['svg', 'canvas']) {
      const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
      await page.goto(`${url}?renderer=${renderer}&dataset=typical`)
      await page.waitForFunction(() => window.__spike?.ready)
      await page.evaluate(() => {
        const id = window.__spike.selectDeep()
        window.__spike.zoomToLane(id)
      })
      await page.waitForTimeout(150)
      const path = join(SCREENSHOT_DIR, `selected-causal-path-${renderer}-1440x900.png`)
      await page.screenshot({ path })
      paths.push(path)
      console.log(path)
      await page.close()
    }

    // Collapsed parent branch.
    {
      const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
      await page.goto(`${url}?renderer=svg&dataset=large`)
      await page.waitForFunction(() => window.__spike?.ready)
      await page.evaluate(() => window.__spike.collapseFirstBranch())
      await page.waitForTimeout(150)
      const path = join(SCREENSHOT_DIR, `collapsed-branch-large-svg-1440x900.png`)
      await page.screenshot({ path })
      paths.push(path)
      console.log(path)
      await page.close()
    }
  } finally {
    await browser.close()
    server.close()
  }
  console.log(`\n${paths.length} screenshots written under ${SCREENSHOT_DIR}`)
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
