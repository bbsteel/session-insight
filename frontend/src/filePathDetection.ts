// Extracting a file path candidate from a terminal row for the context menu.
// Pure text heuristics only — existence is verified server-side (resolve-file)
// before the menu offers to open anything.

export interface PathCandidate {
  path: string
  /** 1-based line from a `path:123` suffix, if present. */
  line: number | null
}

export interface PathMatch extends PathCandidate {
  /** Half-open JavaScript string range occupied by the path token. */
  start: number
  end: number
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
 * with the token under `textOffset` first (when it hits one). Empty when
 * nothing qualifies — no affordance for such rows.
 */
export function extractPathsAt(lineText: string, textOffset: number | null, exts: Set<string> | null): PathCandidate[] {
  const matches = extractPathMatches(lineText, exts)
  if (textOffset !== null) {
    const hit = matches.findIndex(match => textOffset >= match.start && textOffset < match.end)
    if (hit > 0) {
      const [hitMatch] = matches.splice(hit, 1)
      matches.unshift(hitMatch)
    }
  }
  return matches.map(({ path, line }) => ({ path, line }))
}

/** Returns allowlisted path tokens together with their text ranges. */
export function extractPathMatches(lineText: string, exts: Set<string> | null): PathMatch[] {
  const matches: PathMatch[] = []
  PATH_TOKEN.lastIndex = 0
  for (let m = PATH_TOKEN.exec(lineText); m; m = PATH_TOKEN.exec(lineText)) {
    if (isUrlContext(lineText, m.index)) continue
    if (PSEUDO_FS.test(m[0])) continue
    const candidate = parseToken(m[0])
    if (!candidateAllowed(candidate.path, exts)) continue
    matches.push({
      ...candidate,
      start: m.index,
      end: m.index + m[0].length,
    })
  }
  return matches
}

/**
 * Returns the path-like token spanning `textOffset` in `lineText`, falling
 * back to the first path-like token when the click missed every token (or the
 * offset is unknown). null when the row contains nothing path-like.
 */
export function extractPathAt(lineText: string, textOffset: number | null, exts: Set<string> | null = null): PathCandidate | null {
  return extractPathsAt(lineText, textOffset, exts)[0] ?? null
}

/**
 * Path token that strictly spans `textOffset` (no fallback to "first on row").
 * Used when a fold header also owns the click: only a hit on the path itself
 * should open the file; the rest of the row toggles the fold.
 *
 * `textOffset` is a JavaScript string index. Callers that start with an xterm
 * cell column must convert it through the xterm buffer line first.
 */
export function pathAtTextOffset(lineText: string, textOffset: number, exts: Set<string> | null = null): PathCandidate | null {
  const SLACK = 2
  for (const match of extractPathMatches(lineText, exts)) {
    if (textOffset >= match.start - SLACK && textOffset < match.end + SLACK) {
      return { path: match.path, line: match.line }
    }
  }
  return null
}

/** @deprecated Use pathAtTextOffset; the argument is a JavaScript text offset. */
export function pathAtColumn(lineText: string, textOffset: number, exts: Set<string> | null = null): PathCandidate | null {
  return pathAtTextOffset(lineText, textOffset, exts)
}
