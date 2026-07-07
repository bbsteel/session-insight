// Extracting a file path candidate from a terminal row for the context menu.
// Pure text heuristics only — existence is verified server-side (resolve-file)
// before the menu offers to open anything.

export interface PathCandidate {
  path: string
  /** 1-based line from a `path:123` suffix, if present. */
  line: number | null
}

// Tokens containing at least one '/', optionally ~ or ./ prefixed, with an
// optional :line suffix. Trailing punctuation that commonly wraps paths in
// prose (quotes, brackets, commas) is excluded from the character class.
const PATH_TOKEN = /(?:~|\.{1,2})?\/?[\w@+.-]+(?:\/[\w@+.-]+)+(?::\d+)?/g

function isUrlContext(text: string, start: number): boolean {
  return /[a-zA-Z][\w+-]*:\/\/?$/.test(text.slice(Math.max(0, start - 12), start))
}

function parseToken(token: string): PathCandidate {
  const m = token.match(/^(.*?):(\d+)$/)
  if (m) return { path: m[1], line: parseInt(m[2], 10) }
  return { path: token, line: null }
}

/**
 * Returns the path-like token spanning `column` in `lineText`, falling back
 * to the first path-like token when the click missed every token (or column
 * is unknown). null when the row contains nothing path-like.
 */
export function extractPathAt(lineText: string, column: number | null): PathCandidate | null {
  const matches: { start: number; end: number; token: string }[] = []
  PATH_TOKEN.lastIndex = 0
  for (let m = PATH_TOKEN.exec(lineText); m; m = PATH_TOKEN.exec(lineText)) {
    if (isUrlContext(lineText, m.index)) continue
    matches.push({ start: m.index, end: m.index + m[0].length, token: m[0] })
  }
  if (matches.length === 0) return null
  if (column !== null) {
    const hit = matches.find(m => column >= m.start && column < m.end)
    if (hit) return parseToken(hit.token)
  }
  return parseToken(matches[0].token)
}
