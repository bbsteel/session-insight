// URL extraction for clickable terminal rows. Only http(s) URLs are accepted:
// terminal transcripts are untrusted content, so custom schemes must not gain
// a click-to-launch affordance.
const HTTP_URL = /https?:\/\/[^\s<>(){}"']+/gi

/** Returns the first http(s) URL on a terminal row, excluding trailing prose punctuation. */
export function extractTerminalUrl(lineText: string): string | null {
  const match = HTTP_URL.exec(lineText)
  HTTP_URL.lastIndex = 0
  if (!match) return null
  return match[0].replace(/[.,;:!?]+$/, '') || null
}
