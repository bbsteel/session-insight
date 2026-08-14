// Human-readable token counts for toolbar density.
// en compact: K / M / B · zh-CN compact: wan / yi unit strings · full: locale grouping.

export type TokenCountLocale = 'en' | 'zh-CN'
export type TokenCountMode = 'compact' | 'full'

/** Locale-native compact suffixes. Pass zh units from i18n (token.unit.wan / .yi). */
export interface TokenCompactUnits {
  k?: string
  m?: string
  b?: string
  wan?: string
  yi?: string
}

const DEFAULT_UNITS: Required<TokenCompactUnits> = {
  k: 'K',
  m: 'M',
  b: 'B',
  wan: 'wan',
  yi: 'yi',
}

function trimTrailingZeros(s: string): string {
  if (!s.includes('.')) return s
  return s.replace(/\.?0+$/, '')
}

/** Round to maxFrac fraction digits the same way formatScaled displays. */
function roundToFrac(n: number, maxFrac: number): number {
  const f = 10 ** maxFrac
  return Math.round(n * f) / f
}

function formatScaled(locale: TokenCountLocale, n: number, maxFrac: number): string {
  // Avoid scientific notation; cap fraction digits for short toolbar labels.
  const fixed = n.toFixed(maxFrac)
  const trimmed = trimTrailingZeros(fixed)
  // Re-locale decimal separator via Intl on the integer+frac parts.
  const num = Number(trimmed)
  if (!Number.isFinite(num)) return trimmed
  return new Intl.NumberFormat(locale, {
    maximumFractionDigits: maxFrac,
    minimumFractionDigits: 0,
  }).format(num)
}

/**
 * Format a token total for display.
 * - full: always locale-grouped integer
 * - compact en: 999 → as-is; 1.2K; 3.4M; 1.1B
 * - compact zh-CN: below 1e4 full digits; else scaled + wan/yi units
 *
 * Unit selection uses the *rounded* scaled magnitude so values near a
 * boundary promote (999,950 → 1M, not 1,000K; 99,999,500 → 1亿, not 10,000万).
 */
export function formatTokenCount(
  locale: TokenCountLocale,
  value: number,
  mode: TokenCountMode = 'compact',
  units: TokenCompactUnits = {},
): string {
  if (!Number.isFinite(value)) return String(value)
  const abs = Math.abs(value)
  const sign = value < 0 ? '-' : ''
  const u = { ...DEFAULT_UNITS, ...units }

  if (mode === 'full' || abs < 1_000) {
    return new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(Math.round(value))
  }

  if (locale === 'zh-CN') {
    // yi = 1e8, wan = 1e4. Below 1e4 keep full digits (e.g. 9,999).
    // wan uses 1 fraction digit → promote when rounded wan magnitude reaches 1e4.
    const wanRounded = roundToFrac(abs / 10_000, 1)
    if (abs >= 100_000_000 || wanRounded >= 10_000) {
      return `${sign}${formatScaled(locale, abs / 100_000_000, 2)}${u.yi}`
    }
    if (abs >= 10_000) {
      return `${sign}${formatScaled(locale, abs / 10_000, 1)}${u.wan}`
    }
    return new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(Math.round(value))
  }

  // English SI-ish short scale. K/M use 1 fraction digit; B uses 2.
  // Promote when rounded lower-unit magnitude hits 1000 (avoids 1,000K / 1,000M).
  const kRounded = roundToFrac(abs / 1_000, 1)
  const mRounded = roundToFrac(abs / 1_000_000, 1)
  if (abs >= 1_000_000_000 || mRounded >= 1_000) {
    return `${sign}${formatScaled(locale, abs / 1_000_000_000, 2)}${u.b}`
  }
  if (abs >= 1_000_000 || kRounded >= 1_000) {
    return `${sign}${formatScaled(locale, abs / 1_000_000, 1)}${u.m}`
  }
  return `${sign}${formatScaled(locale, abs / 1_000, 1)}${u.k}`
}
