import { useEffect, useState } from 'react'
import {
  getThemePreference,
  resolveIsDark,
  setThemePreference,
  subscribeTheme,
  type ThemePreference,
} from '../theme'
import { MoonIcon, SunIcon } from './icons'

const OPTIONS: { value: ThemePreference; label: string }[] = [
  { value: 'light', label: '浅色' },
  { value: 'dark', label: '深色' },
  { value: 'system', label: '跟随系统' },
]

function useThemePref(): [ThemePreference, boolean, (p: ThemePreference) => void] {
  const [pref, setPref] = useState<ThemePreference>(getThemePreference)
  // Track resolved dark separately so "system" flips re-render even when
  // preference string stays "system".
  const [dark, setDark] = useState(() => resolveIsDark(getThemePreference()))
  useEffect(() => subscribeTheme(() => {
    const next = getThemePreference()
    setPref(next)
    setDark(resolveIsDark(next))
  }), [])
  const set = (p: ThemePreference) => {
    setThemePreference(p)
    setPref(p)
    setDark(resolveIsDark(p))
  }
  return [pref, dark, set]
}

/** Settings-panel select: light / dark / system. */
export function ThemeSelect() {
  const [pref, , setPref] = useThemePref()
  return (
    <label className="flex items-center justify-between gap-2 text-helper text-[var(--text-primary)]">
      主题
      <select
        value={pref}
        onChange={e => setPref(e.target.value as ThemePreference)}
        className="h-7 max-w-[9rem] rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] px-1.5 text-helper text-[var(--text-primary)] focus:border-[var(--accent-blue)] focus:outline-none"
        aria-label="主题"
      >
        {OPTIONS.map(o => (
          <option key={o.value} value={o.value}>{o.label}</option>
        ))}
      </select>
    </label>
  )
}

/**
 * Header sun/moon segmented control (explicit light ↔ dark).
 * When preference is "system", the resolved scheme is shown as selected;
 * clicking a side commits to that explicit preference.
 */
export function ThemeSwitch() {
  const [pref, dark, setPref] = useThemePref()

  return (
    <div
      role="group"
      aria-label="主题"
      title={pref === 'system' ? '当前跟随系统；点击可固定为浅色或深色' : dark ? '深色' : '浅色'}
      className="inline-flex h-7 items-center rounded-full border border-[var(--border-default)] bg-[var(--bg-inset)] p-0.5 shadow-sm"
    >
      <button
        type="button"
        onClick={() => setPref('light')}
        aria-pressed={!dark}
        aria-label="浅色主题"
        className={`flex h-6 w-7 items-center justify-center rounded-full transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
          !dark
            ? 'bg-[var(--bg-surface)] text-[var(--text-primary)] shadow-sm'
            : 'text-[var(--text-muted)] hover:text-[var(--text-secondary)]'
        }`}
      >
        <SunIcon className="h-3.5 w-3.5" />
      </button>
      <button
        type="button"
        onClick={() => setPref('dark')}
        aria-pressed={dark}
        aria-label="深色主题"
        className={`flex h-6 w-7 items-center justify-center rounded-full transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
          dark
            ? 'bg-[var(--bg-surface)] text-[var(--text-primary)] shadow-sm'
            : 'text-[var(--text-muted)] hover:text-[var(--text-secondary)]'
        }`}
      >
        <MoonIcon className="h-3.5 w-3.5" />
      </button>
    </div>
  )
}

/** @deprecated Prefer ThemeSelect / ThemeSwitch. Kept as settings alias. */
export default function ThemeToggle() {
  return <ThemeSelect />
}
