// Extracting a file path candidate from a terminal row for the context menu.
// Pure text heuristics only — existence is verified server-side (resolve-file)
// before the menu offers to open anything.

export interface PathCandidate {
  path: string
  /** 1-based line from a `path:123` suffix, if present. */
  line: number | null
}

// Tokens containing at least one path separator — `/` (Unix) or `\` and `/`
// (Windows) — optionally `~`/`./`/`../` prefixed, with an optional `:line`
// suffix. Windows drive-absolute (`C:\…`, `C:/…`) and UNC (`\\server\share\…`)
// forms get dedicated branches so the drive letter is preserved (the generic
// branch would otherwise match a lone `C` and bail at the drive colon) and
// backslash separators are recognised — without this, chrys/opencode sessions
// recorded on Windows render file paths that the row-affordance matcher never
// recognises, so plain rows mentioning files aren't clickable. Trailing
// punctuation that commonly wraps paths in prose (quotes, brackets, commas) is
// excluded from the character classes.
const PATH_TOKEN = /(?:[A-Za-z]:[\\/][\w@+.-]+(?:[\\/][\w@+.-]+)*|\\\\[\w@+.-]+(?:[\\/][\w@+.-]+)+|(?:~|\.{1,2})?[\\/]?[\w@+.-]+(?:[\\/][\w@+.-]+)+)(?::\d+)?/g

// Pseudo-filesystem paths (shell redirections like 2>/dev/null, /proc/…)
// are never files the user wants to open.
const PSEUDO_FS = /^\/(dev|proc|sys)(\/|$)/

function isUrlContext(text: string, start: number): boolean {
  return /[a-zA-Z][\w+-]*:\/\/?$/.test(text.slice(Math.max(0, start - 12), start))
}

function parseToken(token: string): PathCandidate {
  const m = token.match(/^(.*?):(\d+)$/)
  if (m) return { path: m[1], line: parseInt(m[2], 10) }
  return { path: token, line: null }
}

// Default "session-relevant file" extensions for the open-file affordance.
// Entries without a dot double as exact basename matches (makefile, dockerfile).
export const DEFAULT_FILE_OPEN_EXTS = [
  'ts', 'tsx', 'js', 'jsx', 'mjs', 'cjs', 'go', 'py', 'rs', 'java', 'kt', 'rb', 'php',
  'c', 'h', 'cpp', 'cc', 'hpp', 'cs', 'swift', 'css', 'scss', 'less', 'html', 'htm',
  'xml', 'vue', 'svelte', 'json', 'yaml', 'yml', 'toml', 'ini', 'conf', 'md', 'markdown',
  'sh', 'bash', 'zsh', 'fish', 'sql', 'txt', 'log', 'csv', 'proto', 'graphql', 'gradle',
  'properties', 'env', 'makefile', 'dockerfile',
]

/** Parses the settings value: '' → default list, '*' → no restriction (null). */
export function parseExtList(raw: string): Set<string> | null {
  const s = raw.trim()
  if (!s) return new Set(DEFAULT_FILE_OPEN_EXTS)
  if (s === '*') return null
  return new Set(s.split(/[,\s]+/).map(x => x.replace(/^\./, '').toLowerCase()).filter(Boolean))
}

function candidateAllowed(path: string, exts: Set<string> | null): boolean {
  if (!exts) return true
  // Split on both separators so Windows paths (`C:\Users\foo\bar.ts`) yield the
  // basename rather than the whole string.
  const base = (path.split(/[\\/]/).pop() ?? '').toLowerCase()
  const dot = base.lastIndexOf('.')
  return dot > 0 ? exts.has(base.slice(dot + 1)) : exts.has(base)
}

/**
 * All path-like tokens in the row that pass the extension allowlist, ordered
 * with the token under `column` first (when it hits one). Empty when nothing
 * qualifies — no affordance for such rows.
 */
export function extractPathsAt(lineText: string, column: number | null, exts: Set<string> | null): PathCandidate[] {
  const matches: { start: number; end: number; token: string }[] = []
  PATH_TOKEN.lastIndex = 0
  for (let m = PATH_TOKEN.exec(lineText); m; m = PATH_TOKEN.exec(lineText)) {
    if (isUrlContext(lineText, m.index)) continue
    if (PSEUDO_FS.test(m[0])) continue
    if (!candidateAllowed(parseToken(m[0]).path, exts)) continue
    matches.push({ start: m.index, end: m.index + m[0].length, token: m[0] })
  }
  if (column !== null) {
    const hit = matches.findIndex(m => column >= m.start && column < m.end)
    if (hit > 0) {
      const [h] = matches.splice(hit, 1)
      matches.unshift(h)
    }
  }
  return matches.map(m => parseToken(m.token))
}

/**
 * Returns the path-like token spanning `column` in `lineText`, falling back
 * to the first path-like token when the click missed every token (or column
 * is unknown). null when the row contains nothing path-like.
 */
export function extractPathAt(lineText: string, column: number | null, exts: Set<string> | null = null): PathCandidate | null {
  return extractPathsAt(lineText, column, exts)[0] ?? null
}
