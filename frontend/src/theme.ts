// App theme preference: light / dark / follow OS. Applied via the `.dark`
// class on <html> (tailwind darkMode: 'class'). Default is light.

export type ThemePreference = 'light' | 'dark' | 'system'

const THEME_KEY = 'recap-theme'
const LEGACY_THEME_KEY = 'session-insight-theme'

export function getThemePreference(): ThemePreference {
  try {
    const stored = localStorage.getItem(THEME_KEY) || localStorage.getItem(LEGACY_THEME_KEY)
    if (stored === 'light' || stored === 'dark' || stored === 'system') return stored
  } catch {
    // localStorage unavailable
  }
  return 'light'
}

export function setThemePreference(pref: ThemePreference): void {
  try {
    localStorage.setItem(THEME_KEY, pref)
  } catch {
    // ignore
  }
  applyThemeClass(pref)
  notifyThemeListeners()
}

export function resolveIsDark(pref: ThemePreference = getThemePreference()): boolean {
  if (pref === 'dark') return true
  if (pref === 'light') return false
  if (typeof window !== 'undefined' && typeof window.matchMedia === 'function') {
    return window.matchMedia('(prefers-color-scheme: dark)').matches
  }
  return false
}

export function applyThemeClass(pref: ThemePreference = getThemePreference()): void {
  if (typeof document === 'undefined') return
  document.documentElement.classList.toggle('dark', resolveIsDark(pref))
}

type ThemeListener = () => void
const listeners = new Set<ThemeListener>()

function notifyThemeListeners(): void {
  for (const l of listeners) l()
}

/** Subscribe to preference changes (and re-apply on system scheme when pref is system). */
export function subscribeTheme(listener: ThemeListener): () => void {
  listeners.add(listener)
  return () => { listeners.delete(listener) }
}

let mediaBound = false

/** Call once at app boot: apply class and watch OS scheme when preference is system. */
export function initTheme(): void {
  applyThemeClass()
  if (mediaBound || typeof window === 'undefined' || typeof window.matchMedia !== 'function') return
  mediaBound = true
  const mq = window.matchMedia('(prefers-color-scheme: dark)')
  const onChange = () => {
    if (getThemePreference() === 'system') {
      applyThemeClass('system')
      notifyThemeListeners()
    }
  }
  if (typeof mq.addEventListener === 'function') mq.addEventListener('change', onChange)
  else if (typeof mq.addListener === 'function') mq.addListener(onChange)
}
