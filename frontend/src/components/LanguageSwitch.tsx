import { useEffect, useRef, useState } from 'react'
import { useI18n, type Locale } from '../i18n'
import { GlobeIcon } from './icons'

const OPTIONS: { value: Locale | 'system'; labelKey: string }[] = [
  { value: 'system', labelKey: 'settings.languageSystem' },
  { value: 'en', labelKey: 'settings.languageEnglish' },
  { value: 'zh-CN', labelKey: 'settings.languageChinese' },
]

const SHORT_LABEL: Record<string, string> = {
  system: '',
  en: 'EN',
  'zh-CN': '中文',
}

/** Header language switch button — opens a dropdown on click, with keyboard navigation. */
export function LanguageSwitch() {
  const { preference, setPreference, t } = useI18n()
  const [open, setOpen] = useState(false)
  const [activeIndex, setActiveIndex] = useState(0)
  const containerRef = useRef<HTMLDivElement>(null)
  const optionRefs = useRef<(HTMLButtonElement | null)[]>([])

  const currentValue = preference ?? 'system'
  const currentLabel = SHORT_LABEL[currentValue] || t(OPTIONS.find(o => o.value === currentValue)!.labelKey)

  // Reset active index to current value when opening.
  useEffect(() => {
    if (open) {
      const idx = OPTIONS.findIndex(o => o.value === currentValue)
      setActiveIndex(idx >= 0 ? idx : 0)
      // Focus the first option after render.
      requestAnimationFrame(() => optionRefs.current[idx >= 0 ? idx : 0]?.focus())
    }
  }, [open, currentValue])

  useEffect(() => {
    if (!open) return
    const onClickOutside = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    const onKeyDown = (e: KeyboardEvent) => {
      switch (e.key) {
        case 'Escape':
          setOpen(false)
          break
        case 'ArrowDown':
          e.preventDefault()
          setActiveIndex(prev => {
            const next = Math.min(prev + 1, OPTIONS.length - 1)
            optionRefs.current[next]?.focus()
            return next
          })
          break
        case 'ArrowUp':
          e.preventDefault()
          setActiveIndex(prev => {
            const next = Math.max(prev - 1, 0)
            optionRefs.current[next]?.focus()
            return next
          })
          break
        case 'Home':
          e.preventDefault()
          setActiveIndex(0)
          optionRefs.current[0]?.focus()
          break
        case 'End':
          e.preventDefault()
          setActiveIndex(OPTIONS.length - 1)
          optionRefs.current[OPTIONS.length - 1]?.focus()
          break
      }
    }
    document.addEventListener('mousedown', onClickOutside)
    window.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('mousedown', onClickOutside)
      window.removeEventListener('keydown', onKeyDown)
    }
  }, [open])

  const select = (value: Locale | 'system') => {
    setPreference(value === 'system' ? null : value as Locale)
    setOpen(false)
  }

  return (
    <div ref={containerRef} className="relative">
      <button
        type="button"
        onClick={() => setOpen(o => !o)}
        aria-expanded={open}
        aria-haspopup="listbox"
        aria-label={t('settings.language')}
        title={t('settings.language')}
        className={`inline-flex h-7 w-auto items-center gap-1 rounded-md px-1.5 text-nav focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
          open
            ? 'bg-[var(--bg-surface-hover)] text-[var(--text-primary)]'
            : 'text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
        }`}
      >
        <GlobeIcon className="h-3.5 w-3.5 shrink-0" />
        <span>{currentLabel}</span>
      </button>
      {open && (
        <div
          role="listbox"
          aria-label={t('settings.language')}
          className="absolute right-0 top-full mt-1 z-[var(--z-dropdown)] min-w-[8rem] rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] py-1 shadow-lg"
        >
          {OPTIONS.map((o, i) => {
            const selected = currentValue === o.value
            return (
              <button
                key={o.value}
                ref={el => { optionRefs.current[i] = el }}
                role="option"
                aria-selected={selected}
                tabIndex={i === activeIndex ? 0 : -1}
                onClick={() => select(o.value)}
                onKeyDown={e => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault()
                    select(o.value)
                  }
                }}
                className={`flex w-full items-center gap-2 px-3 py-1.5 text-nav transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[var(--accent-blue)] ${
                  selected
                    ? 'text-[var(--accent-blue)] bg-[var(--bg-surface-hover)]'
                    : 'text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)]'
                }`}
              >
                <span className="flex-1 text-left">{t(o.labelKey)}</span>
                {selected && (
                  <svg className="h-3.5 w-3.5 shrink-0" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M3 8l3.5 3.5L13 5" />
                  </svg>
                )}
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}
