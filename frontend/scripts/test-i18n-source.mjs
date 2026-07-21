import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { existsSync, readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs'
import { dirname, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'

const frontendRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const sourceRoot = join(frontendRoot, 'src')
const baselinePath = join(frontendRoot, 'scripts', 'i18n-source-baseline.json')
const catalogPath = 'src/i18n.tsx'
const hanPattern = /\p{Script=Han}/u

function sourceFiles(dir) {
  return readdirSync(dir)
    .flatMap(name => {
      const path = join(dir, name)
      return statSync(path).isDirectory() ? sourceFiles(path) : [path]
    })
    .filter(path => /\.tsx?$/.test(path))
}

function literalText(node) {
  if (ts.isStringLiteralLike(node) || ts.isJsxText(node)) return node.text
  if (node.kind === ts.SyntaxKind.TemplateHead
      || node.kind === ts.SyntaxKind.TemplateMiddle
      || node.kind === ts.SyntaxKind.TemplateTail) return node.text
  return null
}

function collect() {
  const result = {}
  for (const path of sourceFiles(sourceRoot)) {
    const file = relative(frontendRoot, path).replaceAll('\\', '/')
    if (file === catalogPath) continue
    const source = ts.createSourceFile(path, readFileSync(path, 'utf8'), ts.ScriptTarget.Latest, true)
    const occurrences = new Map()
    const visit = node => {
      const text = literalText(node)
      if (text && hanPattern.test(text)) {
        const key = `${ts.SyntaxKind[node.kind]}\u0000${text}`
        occurrences.set(key, (occurrences.get(key) ?? 0) + 1)
      }
      ts.forEachChild(node, visit)
    }
    visit(source)
    if (occurrences.size > 0) {
      result[file] = [...occurrences]
        .map(([key, count]) => {
          const [kind, text] = key.split('\u0000')
          return { kind, text, count }
        })
        .sort((a, b) => `${a.kind}\u0000${a.text}`.localeCompare(`${b.kind}\u0000${b.text}`))
    }
  }
  return result
}

const current = collect()
const snapshot = Object.fromEntries(Object.entries(current).map(([file, items]) => [file, {
  count: items.reduce((sum, item) => sum + item.count, 0),
  fingerprint: createHash('sha256').update(JSON.stringify(items)).digest('hex').slice(0, 16),
}]))
if (process.argv.includes('--update')) {
  writeFileSync(baselinePath, `${JSON.stringify(snapshot, null, 2)}\n`)
  console.log(`Updated ${relative(frontendRoot, baselinePath)}`)
  process.exit(0)
}

assert.ok(existsSync(baselinePath), 'Missing i18n source baseline; run npm run test:i18n-source:update')
const baseline = JSON.parse(readFileSync(baselinePath, 'utf8'))
const stable = value => JSON.stringify(value)

if (stable(snapshot) !== stable(baseline)) {
  const allFiles = [...new Set([...Object.keys(baseline), ...Object.keys(snapshot)])].sort()
  const changed = allFiles.filter(file => stable(snapshot[file] ?? null) !== stable(baseline[file] ?? null))
  console.error('Uncatalogued Chinese text changed outside src/i18n.tsx:')
  for (const file of changed) {
    console.error(`  ${file}: baseline=${stable(baseline[file] ?? null)} current=${stable(snapshot[file] ?? null)}`)
  }
  console.error('Move user-facing copy into i18n.tsx and call t(...).')
  console.error('When removing legacy literals, refresh the ratchet with npm run test:i18n-source:update.')
  process.exit(1)
}

const occurrenceCount = Object.values(snapshot).reduce((sum, item) => sum + item.count, 0)
const fingerprint = createHash('sha256').update(stable(snapshot)).digest('hex').slice(0, 12)
console.log(`i18n source ratchet passed (${occurrenceCount} legacy literals, ${fingerprint})`)
