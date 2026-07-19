// Enumerate locally installed fonts and classify them as monospaced or proportional.
// The primary source is the browser's queryLocalFonts() API; if it is unavailable
// or permission is denied, we fall back to a curated list of common system fonts.
//
// queryLocalFonts() can return fonts that are registered but not actually usable by
// the browser (e.g. sandboxed Chromium, missing files). We validate each candidate
// by rendering a sample string against two different generic fallbacks. If the font
// is really available, the same face is used regardless of the fallback and the widths
// match; if it is not available, the fallback is used and the widths differ.

export interface SystemFontInfo {
  family: string
  isMonospace: boolean
}

// Common fonts shown when the browser cannot enumerate installed fonts.
const COMMON_UI_FONTS = [
  'Inter',
  'Segoe UI',
  'Microsoft YaHei UI',
  'PingFang SC',
  'Helvetica Neue',
  'Arial',
  'Noto Sans',
  'Roboto',
  'Cantarell',
  'Ubuntu',
  'Open Sans',
]

const COMMON_MONO_FONTS = [
  'JetBrains Mono',
  'Consolas',
  'Menlo',
  'Monaco',
  'SF Mono',
  'Fira Code',
  'Source Code Pro',
  'Cascadia Code',
  'Cascadia Mono',
  'DejaVu Sans Mono',
  'Liberation Mono',
  'Ubuntu Mono',
  'Inconsolata',
]

let probeCanvas: HTMLCanvasElement | null = null
const PROBE_SIZE = 48
const PROBE_SAMPLE = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789'

function getProbeContext(): CanvasRenderingContext2D | null {
  if (!probeCanvas) probeCanvas = document.createElement('canvas')
  return probeCanvas.getContext('2d')
}

const POISON_FALLBACK = '__si_font_probe_fallback__'

function widthFor(family: string | null, fallback: string): number {
  const ctx = getProbeContext()
  if (!ctx) return 0
  const familyPart = family ? `"${family.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}", ` : ''
  ctx.font = `${PROBE_SIZE}px ${familyPart}${fallback}`
  return ctx.measureText(PROBE_SAMPLE).width
}

// A font is truly available if the browser uses it instead of the fallback.
// We compare rendering with a "poison" non-existent fallback: when the font is
// present it is used, producing a different width than the poison fallback alone.
// Using two generic fallbacks is unreliable because some environments map all
// generics (monospace, sans-serif, etc.) to the same face.
export function isFontFamilyAvailable(family: string): boolean {
  const withFont = widthFor(family, POISON_FALLBACK)
  const fallbackOnly = widthFor(null, POISON_FALLBACK)
  return withFont > 0 && fallbackOnly > 0 && Math.abs(withFont - fallbackOnly) >= 0.5
}

// For monospace detection, use the same poison fallback. When the font is
// available and monospace, the narrow 'i' and wide 'M' have the same advance
// width. Unavailable fonts are never tested because we filter by availability
// first.
export function isMonospaceFamily(family: string): boolean {
  const ctx = getProbeContext()
  if (!ctx) return false
  const escaped = family.replace(/\\/g, '\\\\').replace(/"/g, '\\"')
  ctx.font = `${PROBE_SIZE}px "${escaped}", ${POISON_FALLBACK}`
  const i = ctx.measureText('iiiiiiiiii').width
  const W = ctx.measureText('MMMMMMMMMM').width
  return i > 0 && W > 0 && Math.abs(i - W) < 0.5
}

export async function fallbackSystemFonts(): Promise<SystemFontInfo[]> {
  const merged = new Map<string, SystemFontInfo>()
  for (const family of COMMON_UI_FONTS) {
    if (isFontFamilyAvailable(family)) {
      merged.set(family.toLowerCase(), { family, isMonospace: isMonospaceFamily(family) })
    }
  }
  for (const family of COMMON_MONO_FONTS) {
    if (isFontFamilyAvailable(family)) {
      merged.set(family.toLowerCase(), { family, isMonospace: isMonospaceFamily(family) })
    }
  }
  return Array.from(merged.values()).sort((a, b) => a.family.localeCompare(b.family))
}

let cachedFonts: SystemFontInfo[] | null = null
let pendingFonts: Promise<SystemFontInfo[]> | null = null

export async function queryLocalSystemFonts(): Promise<SystemFontInfo[]> {
  if (cachedFonts) return cachedFonts
  if (pendingFonts) return pendingFonts
  pendingFonts = (async () => {
    try {
      if (typeof window !== 'undefined' && 'queryLocalFonts' in window) {
        const fontData = await (window as unknown as { queryLocalFonts: () => Promise<{ family: string }[]> }).queryLocalFonts()
        const families = Array.from(new Set<string>(fontData.map(f => f.family)))
        const list = families
          .filter(family => isFontFamilyAvailable(family))
          .map(family => ({ family, isMonospace: isMonospaceFamily(family) }))
        cachedFonts = list.sort((a, b) => a.family.localeCompare(b.family))
        return cachedFonts
      }
    } catch {
      // Permission denied or API unavailable: fall through to the static list.
    }
    cachedFonts = await fallbackSystemFonts()
    return cachedFonts
  })()
  return pendingFonts
}
