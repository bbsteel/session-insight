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

/** Header theme toggle. One click target switches the resolved light/dark mode. */
export function ThemeSwitch() {
  const [pref, dark, setPref] = useThemePref()
  const nextTheme: ThemePreference = dark ? 'light' : 'dark'
  const currentLabel = dark ? '深色' : '浅色'
  const nextLabel = dark ? '浅色' : '深色'

  return (
    <button
      type="button"
      onClick={() => setPref(nextTheme)}
      aria-pressed={dark}
      aria-label={`切换到${nextLabel}主题`}
      title={pref === 'system'
        ? `当前跟随系统（${currentLabel}）；点击切换到${nextLabel}`
        : `当前为${currentLabel}；点击切换到${nextLabel}`}
      className="inline-flex h-7 w-8 items-center justify-center rounded-full border border-[var(--border-default)] bg-[var(--bg-inset)] text-[var(--text-primary)] shadow-sm transition-colors duration-fast hover:bg-[var(--bg-surface-hover)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
    >
      {dark ? (
        <MoonIcon className="h-3.5 w-3.5" />
      ) : (
        <SunIcon className="h-3.5 w-3.5" />
      )}
    </button>
  )
}

/** @deprecated Prefer ThemeSelect / ThemeSwitch. Kept as settings alias. */
export default function ThemeToggle() {
  return <ThemeSelect />
}
