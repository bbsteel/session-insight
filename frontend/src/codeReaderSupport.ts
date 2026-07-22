// Capability matrix for Code Reader: which extensions get syntax highlight
// and/or structure outline. Keep in sync with languageForPath in CodeReader.tsx.
import { translate, type Locale } from './i18n'

export type LanguageSupportLevel = 'full' | 'partial' | 'highlight_only' | 'none'

export type OutlineSupport = 'full' | 'approximate' | 'none'

export interface LanguageSupportInfo {
  level: LanguageSupportLevel
  /** Human language name, or null when the extension is unknown. */
  label: string | null
  highlight: boolean
  outline: OutlineSupport
  /** One-line localized status for the incomplete-support banner. */
  summary: string
}

type ExtEntry = {
  label: string
  highlight: true
  outline: OutlineSupport
  /** Optional extra note appended after the default summary. */
  noteKey?: string
}

const FULL: Omit<ExtEntry, 'label'> = { highlight: true, outline: 'full' }
const HIGHLIGHT: Omit<ExtEntry, 'label'> = { highlight: true, outline: 'none' }
const PARTIAL_CS: Omit<ExtEntry, 'label'> = {
  highlight: true,
  outline: 'approximate',
  noteKey: 'reader.support.partialNote',
}

const BY_EXT: Record<string, ExtEntry> = {
  // Full: highlight + structural outline
  go: { label: 'Go', ...FULL },
  js: { label: 'JavaScript', ...FULL },
  mjs: { label: 'JavaScript', ...FULL },
  cjs: { label: 'JavaScript', ...FULL },
  jsx: { label: 'JavaScript (JSX)', ...FULL },
  ts: { label: 'TypeScript', ...FULL },
  tsx: { label: 'TypeScript (TSX)', ...FULL },
  py: { label: 'Python', ...FULL },
  pyw: { label: 'Python', ...FULL },
  java: { label: 'Java', ...FULL },
  rs: { label: 'Rust', ...FULL },
  rb: { label: 'Ruby', ...FULL },
  rake: { label: 'Ruby', ...FULL },
  gemspec: { label: 'Ruby', ...FULL },
  md: { label: 'Markdown', ...FULL },
  markdown: { label: 'Markdown', ...FULL },

  // Partial outline (community grammar with weak structure)
  cs: { label: 'C#', ...PARTIAL_CS },

  // Highlight only
  c: { label: 'C', ...HIGHLIGHT },
  h: { label: 'C/C++ Header', ...HIGHLIGHT },
  cpp: { label: 'C++', ...HIGHLIGHT },
  cc: { label: 'C++', ...HIGHLIGHT },
  cxx: { label: 'C++', ...HIGHLIGHT },
  hpp: { label: 'C++ Header', ...HIGHLIGHT },
  hh: { label: 'C++ Header', ...HIGHLIGHT },
  hxx: { label: 'C++ Header', ...HIGHLIGHT },
  php: { label: 'PHP', ...HIGHLIGHT },
  json: { label: 'JSON', ...HIGHLIGHT },
  jsonc: { label: 'JSON', ...HIGHLIGHT },
  html: { label: 'HTML', ...HIGHLIGHT },
  htm: { label: 'HTML', ...HIGHLIGHT },
  vue: { label: 'Vue', ...HIGHLIGHT },
  svelte: { label: 'Svelte', ...HIGHLIGHT },
  css: { label: 'CSS', ...HIGHLIGHT },
  scss: { label: 'SCSS', ...HIGHLIGHT },
  sass: { label: 'Sass', ...HIGHLIGHT },
  less: { label: 'Less', ...HIGHLIGHT },
  kt: { label: 'Kotlin', ...HIGHLIGHT },
  kts: { label: 'Kotlin', ...HIGHLIGHT },
  swift: { label: 'Swift', ...HIGHLIGHT },
  xml: { label: 'XML', ...HIGHLIGHT },
  svg: { label: 'SVG', ...HIGHLIGHT },
  yaml: { label: 'YAML', ...HIGHLIGHT },
  yml: { label: 'YAML', ...HIGHLIGHT },
  sql: { label: 'SQL', ...HIGHLIGHT },
  sh: { label: 'Shell', ...HIGHLIGHT },
  bash: { label: 'Shell', ...HIGHLIGHT },
  zsh: { label: 'Shell', ...HIGHLIGHT },
  ksh: { label: 'Shell', ...HIGHLIGHT },
  toml: { label: 'TOML', ...HIGHLIGHT },
}

function levelFor(outline: OutlineSupport, known: boolean): LanguageSupportLevel {
  if (!known) return 'none'
  if (outline === 'full') return 'full'
  if (outline === 'approximate') return 'partial'
  return 'highlight_only'
}

function summaryFor(locale: Locale, label: string | null, ext: string, outline: OutlineSupport, noteKey?: string): string {
  if (!label) {
    return ext
      ? translate(locale, 'reader.support.unknownExt', { ext })
      : translate(locale, 'reader.support.unknown')
  }
  if (outline === 'full') return translate(locale, 'reader.support.full', { language: label })
  if (outline === 'approximate') {
    return noteKey
      ? translate(locale, 'reader.support.partial', { language: label, note: translate(locale, noteKey) })
      : translate(locale, 'reader.support.partialDefault', { language: label })
  }
  return translate(locale, 'reader.support.highlight', { language: label })
}

export function languageSupportForPath(path: string, locale: Locale = 'zh-CN'): LanguageSupportInfo {
  const base = path.split(/[\\/]/).pop() ?? path
  const dot = base.lastIndexOf('.')
  const ext = dot > 0 ? base.slice(dot + 1).toLowerCase() : ''
  const entry = ext ? BY_EXT[ext] : undefined
  if (!entry) {
    return {
      level: 'none',
      label: null,
      highlight: false,
      outline: 'none',
      summary: summaryFor(locale, null, ext, 'none'),
    }
  }
  return {
    level: levelFor(entry.outline, true),
    label: entry.label,
    highlight: entry.highlight,
    outline: entry.outline,
    summary: summaryFor(locale, entry.label, ext, entry.outline, entry.noteKey),
  }
}

/** True when the banner should appear (anything short of full support). */
export function shouldShowLanguageSupportBanner(info: LanguageSupportInfo): boolean {
  return info.level !== 'full'
}
