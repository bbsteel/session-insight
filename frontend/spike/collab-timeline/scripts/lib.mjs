/** Shared helpers for the spike runner scripts (plain JS, run directly by Node). */

import { execFileSync } from 'node:child_process'
import { createServer } from 'node:http'
import { readFileSync, existsSync, mkdirSync } from 'node:fs'
import { readFile } from 'node:fs/promises'
import { dirname, extname, join, normalize } from 'node:path'
import { fileURLToPath } from 'node:url'

export const SPIKE_DIR = dirname(dirname(fileURLToPath(import.meta.url)))
export const FRONTEND_DIR = dirname(dirname(SPIKE_DIR))
export const SCREENSHOT_DIR = process.env.SPIKE_SCREENSHOT_DIR ?? '/tmp/session-insight-ui/collab-spike'

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json',
  '.png': 'image/png',
}

export function buildHarness() {
  const esbuild = join(FRONTEND_DIR, 'node_modules', '.bin', 'esbuild')
  if (!existsSync(esbuild)) throw new Error(`esbuild not found at ${esbuild}; run npm ci in frontend/`)
  mkdirSync(join(SPIKE_DIR, 'dist'), { recursive: true })
  execFileSync(
    esbuild,
    [
      join(SPIKE_DIR, 'src', 'harness.ts'),
      '--bundle',
      '--format=esm',
      '--target=es2020',
      `--outfile=${join(SPIKE_DIR, 'dist', 'harness.js')}`,
    ],
    { stdio: 'inherit' },
  )
}

export function serveSpike() {
  const server = createServer((req, res) => {
    const raw = (req.url ?? '/').split('?')[0]
    const path = normalize(raw === '/' ? '/harness.html' : raw)
    const file = join(SPIKE_DIR, path)
    if (!file.startsWith(SPIKE_DIR) || !existsSync(file)) {
      res.writeHead(404)
      res.end('not found')
      return
    }
    readFile(file)
      .then((body) => {
        res.writeHead(200, { 'content-type': MIME[extname(file)] ?? 'application/octet-stream' })
        res.end(body)
      })
      .catch(() => {
        res.writeHead(500)
        res.end('error')
      })
  })
  return new Promise((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address()
      const port = typeof addr === 'object' && addr ? addr.port : 0
      resolve({ server, url: `http://127.0.0.1:${port}/harness.html` })
    })
  })
}

export function median(values) {
  if (values.length === 0) return NaN
  const s = [...values].sort((a, b) => a - b)
  const mid = Math.floor(s.length / 2)
  return s.length % 2 ? s[mid] : (s[mid - 1] + s[mid]) / 2
}

export function p95(values) {
  if (values.length === 0) return NaN
  const s = [...values].sort((a, b) => a - b)
  return s[Math.min(s.length - 1, Math.ceil(0.95 * s.length) - 1)]
}

export function fmt(ms) {
  return Number.isFinite(ms) ? ms.toFixed(2) : 'n/a'
}

export function envInfo() {
  let cpu = 'unknown'
  try {
    const cpuinfo = readFileSync('/proc/cpuinfo', 'utf8')
    const model = cpuinfo.split('\n').find((l) => l.startsWith('model name'))
    if (model) cpu = model.split(':')[1].trim()
  } catch {
    /* non-Linux */
  }
  let os = process.platform
  try {
    os = execFileSync('uname', ['-srm']).toString().trim()
  } catch {
    /* keep platform */
  }
  let npm = 'unknown'
  try {
    npm = execFileSync('npm', ['--version']).toString().trim()
  } catch {
    /* keep unknown */
  }
  return { machine: process.arch, os, cpu, node: process.version, npm }
}
