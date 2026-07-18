import { useEffect, useState } from 'react'
import {
  getThemePreference,
  setThemePreference,
  subscribeTheme,
  type ThemePreference,
} from '../theme'

const OPTIONS: { value: ThemePreference; label: string }[] = [
  { value: 'light', label: '浅色' },
  { value: 'dark', label: '深色' },
  { value: 'system', label: '跟随系统' },
]

/** Theme preference control for the settings panel (light / dark / system). */
export default function ThemeToggle() {
  const [pref, setPref] = useState<ThemePreference>(getThemePreference)

  useEffect(() => subscribeTheme(() => setPref(getThemePreference())), [])

  return (
    <label className="flex items-center justify-between gap-2 text-helper text-[var(--text-primary)]">
      主题
      <select
        value={pref}
        onChange={e => {
          const next = e.target.value as ThemePreference
          setThemePreference(next)
          setPref(next)
        }}
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
