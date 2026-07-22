import { useEffect, useState } from 'react'
import { queryLocalSystemFonts, fallbackSystemFonts, type SystemFontInfo } from '../systemFonts'
import { useI18n } from '../i18n'

interface FontPickerProps {
  label: string
  value: string
  onChange: (family: string) => void
  monospaceOnly?: boolean
}

export default function FontPicker({ label, value, onChange, monospaceOnly }: FontPickerProps) {
  const { t } = useI18n()
  const [fonts, setFonts] = useState<SystemFontInfo[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let cancelled = false
    setLoading(true)

    async function load() {
      let list = await queryLocalSystemFonts()
      if (cancelled) return

      // If the browser API returned nothing, fall back to our curated list.
      if (list.length === 0) {
        list = await fallbackSystemFonts()
      }

      // Ensure the currently selected value is always present, even if the local
      // font API did not list it (e.g. a bundled web font or uninstalled font).
      const merged = new Map<string, SystemFontInfo>()
      for (const f of list) merged.set(f.family.toLowerCase(), f)
      if (!merged.has(value.toLowerCase())) {
        merged.set(value.toLowerCase(), { family: value, isMonospace: !!monospaceOnly })
      }

      let arr = Array.from(merged.values()).sort((a, b) => a.family.localeCompare(b.family))
      if (monospaceOnly) arr = arr.filter(f => f.isMonospace)

      setFonts(arr)
      setLoading(false)
    }

    void load()
    return () => { cancelled = true }
  }, [value, monospaceOnly])

  return (
    <label className="block text-helper text-[var(--text-primary)]">
      {label}
      <select
        value={value}
        onChange={e => onChange(e.target.value)}
        disabled={loading}
        aria-label={label}
        className="mt-1 h-7 w-full rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] px-2 text-meta text-[var(--text-primary)] focus:border-[var(--accent-blue)] focus:outline-none"
      >
        {fonts.map(f => (
          <option key={f.family} value={f.family}>{f.family}</option>
        ))}
      </select>
      {loading && (
        <div className="mt-1 text-meta text-[var(--text-muted)]">{t('font.loadingLocal')}</div>
      )}
    </label>
  )
}
