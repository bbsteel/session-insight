import { extractPathAt, pathAtColumn } from '/tmp/session-insight-file-path/filePathDetection.js'

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
assertEq(extractPathAt('uv venv --help 2>/dev/null | head -3', null), null, 'shell redirection /dev/null is not a file')
assertEq(extractPathAt('cat /proc/self/status /sys/class/net', null), null, 'pseudo filesystems excluded')
assertEq(extractPathAt('log to /dev/null but edit src/main.go', 30), { path: 'src/main.go', line: null }, 'real path survives next to /dev/null')

// Windows paths: chrys/opencode sessions recorded on Windows store backslash
// (C:\…) and sometimes forward-slash (C:/…) forms. Both must keep the drive
// letter and recognise backslash separators, otherwise plain rows mentioning
// files get no click affordance.
assertEq(extractPathAt('读取 C:\\Users\\foo\\main.go 完成', 6), { path: 'C:\\Users\\foo\\main.go', line: null }, 'windows backslash path under cursor')
assertEq(extractPathAt('读取 C:/Users/foo/main.go 完成', 6), { path: 'C:/Users/foo/main.go', line: null }, 'windows forward-slash path keeps drive letter')
assertEq(extractPathAt('edit C:\\Users\\foo\\bar.ts:123 done', 6), { path: 'C:\\Users\\foo\\bar.ts', line: 123 }, 'windows backslash path with :line')
assertEq(extractPathAt('cd C:\\Users\\foo && vim a.vue', 4), { path: 'C:\\Users\\foo', line: null }, 'windows drive dir')
assertEq(extractPathAt('cat src\\components\\App.tsx', 5), { path: 'src\\components\\App.tsx', line: null }, 'windows relative backslash path')
assertEq(extractPathAt('share \\\\server\\share\\foo\\bar.go', 7), { path: '\\\\server\\share\\foo\\bar.go', line: null }, 'UNC path')

// pathAtColumn: strict column hit only (used by fold-header dual-action).
const writeHeader = '◆ write (275 行)  /home/deck/projects/session-insight/docs/version-roadmap.md · 3.0s'
const pathStart = writeHeader.indexOf('/home/deck')
assertEq(pathAtColumn(writeHeader, pathStart + 5), { path: '/home/deck/projects/session-insight/docs/version-roadmap.md', line: null }, 'pathAtColumn hits path token')
assertEq(pathAtColumn(writeHeader, 2), null, 'pathAtColumn misses tool name → null (fold keeps click)')
assertEq(pathAtColumn(writeHeader, writeHeader.indexOf('275')), null, 'pathAtColumn misses badge digits → null')

if (failures > 0) {
  console.error(`${failures} failure(s)`)
  process.exit(1)
}
console.log('all file-path-detection tests passed')
