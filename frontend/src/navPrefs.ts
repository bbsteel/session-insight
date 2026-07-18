// Session-open navigation panel defaults (user messages / tools).
// Stored in localStorage so settings UI and ReplayView stay in sync.

export type NavOpenPref = 'user' | 'tool' | 'off'

const NAV_OPEN_KEY = 'si-nav-open-on-session'

export function getNavOpenPref(): NavOpenPref {
  try {
    const v = localStorage.getItem(NAV_OPEN_KEY)
    if (v === 'user' || v === 'tool' || v === 'off') return v
  } catch {
    // ignore
  }
  // Default: open user-message nav with pin when a session is selected.
  return 'user'
}

export function setNavOpenPref(pref: NavOpenPref): void {
  try {
    localStorage.setItem(NAV_OPEN_KEY, pref)
  } catch {
    // ignore
  }
}
