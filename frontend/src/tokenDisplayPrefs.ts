// Token count display preference for the session header and related chrome.
// compact (default): human-readable 1.2M / 120万; full: locale-grouped digits.

export type TokenDisplayMode = 'compact' | 'full'

const TOKEN_DISPLAY_KEY = 'si-token-display-mode'

type Listener = (mode: TokenDisplayMode) => void
const listeners = new Set<Listener>()

export function getTokenDisplayMode(): TokenDisplayMode {
  try {
    const v = localStorage.getItem(TOKEN_DISPLAY_KEY)
    if (v === 'compact' || v === 'full') return v
  } catch {
    // ignore
  }
  return 'compact'
}

export function setTokenDisplayMode(mode: TokenDisplayMode): void {
  try {
    localStorage.setItem(TOKEN_DISPLAY_KEY, mode)
  } catch {
    // ignore
  }
  for (const listener of listeners) listener(mode)
}

/** Subscribe to preference changes from Settings (same-tab). */
export function onTokenDisplayModeChange(listener: Listener): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}
