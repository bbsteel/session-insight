import { extractPathAt } from '/tmp/session-insight-file-path/filePathDetection.js'

let failures = 0
function assertEq(actual, expected, label) {
  const a = JSON.stringify(actual)
  const e = JSON.stringify(expected)
  if (a !== e) {
    failures++
    console.error(`FAIL ${label}: got ${a}, want ${e}`)
  } else {
    console.log(`ok   ${label}`)
  }
}

assertEq(extractPathAt('✏ Edit frontend/src/api.ts', 10), { path: 'frontend/src/api.ts', line: null }, 'relative path under cursor')
assertEq(extractPathAt('cat /etc/hosts && ls', 6), { path: '/etc/hosts', line: null }, 'absolute path')
assertEq(extractPathAt('open ~/notes/todo.md now', 8), { path: '~/notes/todo.md', line: null }, 'tilde path')
assertEq(extractPathAt('at internal/db/db.go:164 in schema', 8), { path: 'internal/db/db.go', line: 164 }, 'path:line suffix')
assertEq(extractPathAt('no paths here at all', 5), null, 'no path → null')
assertEq(extractPathAt('see https://example.com/a/b docs', 14), null, 'url is not a file path')
assertEq(extractPathAt('a.go and src/main.go', 0), { path: 'src/main.go', line: null }, 'column misses → first path-like token')
assertEq(extractPathAt('left src/a.ts right src/b.ts', 22), { path: 'src/b.ts', line: null }, 'column picks the second token')
assertEq(extractPathAt('padded  ./scripts/run.sh  tail', null), { path: './scripts/run.sh', line: null }, 'null column → first token')

if (failures > 0) {
  console.error(`${failures} failure(s)`)
  process.exit(1)
}
console.log('all file-path-detection tests passed')
