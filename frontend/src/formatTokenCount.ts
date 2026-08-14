// Human-readable token counts for toolbar density.
// en compact: K / M / B · zh-CN compact: 10_000 / 100_000_000 unit strings · full: locale grouping.

export type TokenCountLocale = 'en' | 'zh-CN'
export type TokenCountMode = 'compact' | 'full'

/** Compact unit thresholds (powers of ten used for scaling). */
const THOUSAND = 1_000
const MILLION = 1_000_000
const BILLION = 1_000_000_000
const TEN_THOUSAND = 10_000
const HUNDRED_MILLION = 100_000_000

/** Locale-native compact suffixes. Pass zh units from i18n (token.unit.tenThousand / .hundredMillion). */
export interface TokenCompactUnits {
  thousand?: string
  million?: string
  billion?: string
  /** zh-CN suffix for ×10_000 (汉字「万」via i18n). */
  tenThousand?: string
  /** zh-CN suffix for ×100_000_000 (汉字「亿」via i18n). */
  hundredMillion?: string
}

const DEFAULT_UNITS: Required<TokenCompactUnits> = {
  thousand: 'K',
  million: 'M',
  billion: 'B',
  tenThousand: '×10k',
  hundredMillion: '×100M',
}

function trimTrailingZeros(text: string): string {
  if (!text.includes('.')) return text
  return text.replace(/\.?0+$/, '')
}

/** Round to maxFrac fraction digits the same way formatScaled displays. */
function roundToFrac(magnitude: number, maxFrac: number): number {
  const scale = 10 ** maxFrac
  return Math.round(magnitude * scale) / scale
}

function formatScaled(locale: TokenCountLocale, magnitude: number, maxFrac: number): string {
  // Avoid scientific notation; cap fraction digits for short toolbar labels.
  const fixed = magnitude.toFixed(maxFrac)
  const trimmed = trimTrailingZeros(fixed)
  // Re-locale decimal separator via Intl on the integer+frac parts.
  const parsed = Number(trimmed)
  if (!Number.isFinite(parsed)) return trimmed
  return new Intl.NumberFormat(locale, {
    maximumFractionDigits: maxFrac,
    minimumFractionDigits: 0,
  }).format(parsed)
}

/**
 * Format a token total for display.
 * - full: always locale-grouped integer
 * - compact en: 999 → as-is; 1.2K; 3.4M; 1.1B
 * - compact zh-CN: below 10_000 full digits; else scaled + tenThousand/hundredMillion units
 *
 * Unit selection uses the *rounded* scaled magnitude so values near a
 * boundary promote (999,950 → 1M, not 1,000K; 99,999,500 → 1×100M unit, not 10,000×10k).
 */
export function formatTokenCount(
  locale: TokenCountLocale,
  value: number,
  mode: TokenCountMode = 'compact',
  units: TokenCompactUnits = {},
): string {
  if (!Number.isFinite(value)) return String(value)
  const absolute = Math.abs(value)
  const sign = value < 0 ? '-' : ''
  const unitLabels = { ...DEFAULT_UNITS, ...units }

  if (mode === 'full' || absolute < THOUSAND) {
    return new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(Math.round(value))
  }

  if (locale === 'zh-CN') {
    // hundredMillion = 1e8, tenThousand = 1e4. Below 1e4 keep full digits (e.g. 9,999).
    // tenThousand uses 1 fraction digit → promote when rounded magnitude reaches TEN_THOUSAND.
    const tenThousandRounded = roundToFrac(absolute / TEN_THOUSAND, 1)
    if (absolute >= HUNDRED_MILLION || tenThousandRounded >= TEN_THOUSAND) {
      return `${sign}${formatScaled(locale, absolute / HUNDRED_MILLION, 2)}${unitLabels.hundredMillion}`
    }
    if (absolute >= TEN_THOUSAND) {
      return `${sign}${formatScaled(locale, absolute / TEN_THOUSAND, 1)}${unitLabels.tenThousand}`
    }
    return new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(Math.round(value))
  }

  // English SI-ish short scale. K/M use 1 fraction digit; B uses 2.
  // Promote when rounded lower-unit magnitude hits THOUSAND (avoids 1,000K / 1,000M).
  const thousandRounded = roundToFrac(absolute / THOUSAND, 1)
  const millionRounded = roundToFrac(absolute / MILLION, 1)
  if (absolute >= BILLION || millionRounded >= THOUSAND) {
    return `${sign}${formatScaled(locale, absolute / BILLION, 2)}${unitLabels.billion}`
  }
  if (absolute >= MILLION || thousandRounded >= THOUSAND) {
    return `${sign}${formatScaled(locale, absolute / MILLION, 1)}${unitLabels.million}`
  }
  return `${sign}${formatScaled(locale, absolute / THOUSAND, 1)}${unitLabels.thousand}`
}
