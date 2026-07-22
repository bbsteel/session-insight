import { useEffect, useState } from 'react'
import {
  getThemePreference,
  resolveIsDark,
  setThemePreference,
  subscribeTheme,
  type ThemePreference,
} from '../theme'
import { MoonIcon, SunIcon } from './icons'
import { useI18n } from '../i18n'

const OPTIONS: { value: ThemePreference; labelKey: string }[] = [
  { value: 'light', labelKey: 'theme.light' },
  { value: 'dark', labelKey: 'theme.dark' },
  { value: 'system', labelKey: 'theme.system' },
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
  const { t } = useI18n()
  return (
    <label className="flex items-center justify-between gap-2 text-helper text-[var(--text-primary)]">
      {t('theme.label')}
      <select
        value={pref}
        onChange={e => setPref(e.target.value as ThemePreference)}
        className="h-7 max-w-[9rem] rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] px-1.5 text-helper text-[var(--text-primary)] focus:border-[var(--accent-blue)] focus:outline-none"
        aria-label={t('theme.label')}
      >
        {OPTIONS.map(o => (
          <option key={o.value} value={o.value}>{t(o.labelKey)}</option>
        ))}
      </select>
    </label>
  )
}

/** Header theme toggle. Shows both sun and moon icons; the active one is framed. */
export function ThemeSwitch() {
  const [pref, dark, setPref] = useThemePref()
  const { t } = useI18n()
  const nextTheme: ThemePreference = dark ? 'light' : 'dark'
  const currentLabel = t(dark ? 'theme.dark' : 'theme.light')
  const nextLabel = t(dark ? 'theme.light' : 'theme.dark')

  const activeIconBox =
    'flex h-5 w-6 items-center justify-center rounded-full border border-[var(--border-default)] bg-[var(--bg-surface)] text-[var(--text-primary)] shadow-sm'
  const inactiveIcon = 'flex h-5 w-6 items-center justify-center rounded-full text-[var(--text-muted)]'

  return (
    <button
      type="button"
      onClick={() => setPref(nextTheme)}
      aria-pressed={dark}
      aria-label={t('theme.switchTo', { theme: nextLabel })}
      title={pref === 'system'
        ? t('theme.systemTitle', { current: currentLabel, next: nextLabel })
        : t('theme.currentTitle', { current: currentLabel, next: nextLabel })}
      className="inline-flex h-7 w-14 items-center justify-center rounded-full border border-[var(--border-default)] bg-[var(--bg-inset)] text-[var(--text-primary)] shadow-sm transition-colors duration-fast hover:bg-[var(--bg-surface-hover)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
    >
      <span className="flex w-full items-center justify-between px-1">
        <span className={dark ? inactiveIcon : activeIconBox}>
          <SunIcon className="h-3.5 w-3.5" />
        </span>
        <span className={dark ? activeIconBox : inactiveIcon}>
          <MoonIcon className="h-3.5 w-3.5" />
        </span>
      </span>
    </button>
  )
}

/** @deprecated Prefer ThemeSelect / ThemeSwitch. Kept as settings alias. */
export default function ThemeToggle() {
  return <ThemeSelect />
}
