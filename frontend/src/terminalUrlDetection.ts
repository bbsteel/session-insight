// URL extraction for clickable terminal rows. Only http(s) URLs are accepted:
// terminal transcripts are untrusted content, so custom schemes must not gain
// a click-to-launch affordance.
const HTTP_URL_START = /https?:\/\//gi

// RFC 3986 unreserved + reserved characters that belong in an http(s) URL.
// Brackets, quotes, and angle brackets stay delimiters — terminal prose uses
// them as wrappers. Parentheses are tracked separately so a_(b) stays intact.
const ALLOWED_TERMINAL_URL_ASCII = /[A-Za-z0-9\-._~:/?#@!$&*+,;=%]/
const UNICODE_LETTER_OR_NUMBER = /[\p{L}\p{N}]/u

export interface TerminalUrlMatch {
  value: string
  start: number
  end: number
}

function isAllowedTerminalUrlChar(ch: string): boolean {
  if (ALLOWED_TERMINAL_URL_ASCII.test(ch)) return true
  // IRIs may include letters/numbers outside ASCII (Wikipedia paths, etc.).
  // Unicode punctuation such as fullwidth （） must stop the match — those
  // characters percent-encode into 404s when glued onto an otherwise valid URL.
  return ch > '\u007f' && UNICODE_LETTER_OR_NUMBER.test(ch)
}

function terminalUrls(lineText: string): TerminalUrlMatch[] {
  const urls: TerminalUrlMatch[] = []
  HTTP_URL_START.lastIndex = 0
  for (let startMatch = HTTP_URL_START.exec(lineText); startMatch; startMatch = HTTP_URL_START.exec(lineText)) {
    const start = startMatch.index
    let end = start + startMatch[0].length
    let parenDepth = 0
    for (; end < lineText.length; end++) {
      const ch = lineText[end]
      if (ch === '(') {
        parenDepth++
        continue
      }
      if (ch === ')') {
        if (parenDepth === 0) break
        parenDepth--
        continue
      }
      if (!isAllowedTerminalUrlChar(ch)) break
    }
    // Sentence delimiters are commonly attached to a URL in prose. Keep ! and
    // ? intact: both are meaningful URL characters in paths and query values.
    const value = lineText.slice(start, end).replace(/[.,;:]+$/, '')
    if (value) urls.push({ value, start, end: start + value.length })
    HTTP_URL_START.lastIndex = Math.max(end, start + 1)
  }
  HTTP_URL_START.lastIndex = 0
  return urls
}

/** Returns every http(s) URL and its exact text range in the terminal row. */
export function extractTerminalUrlMatches(lineText: string): TerminalUrlMatch[] {
  return terminalUrls(lineText)
}

/** Returns the preferred URL and its text range, favoring Markdown destinations over labels. */
export function extractTerminalUrlMatch(lineText: string): TerminalUrlMatch | null {
  const urls = extractTerminalUrlMatches(lineText)
  // Markdown links render as `label (destination)`. Prefer their destination
  // when a URL-shaped label precedes it, while retaining parentheses that are
  // actually part of the URL itself.
  for (let i = urls.length - 1; i >= 0; i--) {
    const url = urls[i]
    if (lineText[url.start - 1] === '(' && lineText[url.end] === ')') return url
  }
  return urls[0] ?? null
}

/** Returns the preferred http(s) URL, favoring Markdown destinations over labels. */
export function extractTerminalUrl(lineText: string): string | null {
  return extractTerminalUrlMatch(lineText)?.value ?? null
}
