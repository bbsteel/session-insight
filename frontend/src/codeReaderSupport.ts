// Capability matrix for Code Reader: which extensions get syntax highlight
// and/or structure outline. Keep in sync with languageForPath in CodeReader.tsx.

export type LanguageSupportLevel = 'full' | 'partial' | 'highlight_only' | 'none'

export type OutlineSupport = 'full' | 'approximate' | 'none'

export interface LanguageSupportInfo {
  level: LanguageSupportLevel
  /** Human language name, or null when the extension is unknown. */
  label: string | null
  highlight: boolean
  outline: OutlineSupport
  /** One-line Chinese status for the incomplete-support banner. */
  summary: string
}

type ExtEntry = {
  label: string
  highlight: true
  outline: OutlineSupport
  /** Optional extra note appended after the default summary. */
  note?: string
}

const FULL: Omit<ExtEntry, 'label'> = { highlight: true, outline: 'full' }
const HIGHLIGHT: Omit<ExtEntry, 'label'> = { highlight: true, outline: 'none' }
const PARTIAL_CS: Omit<ExtEntry, 'label'> = {
  highlight: true,
  outline: 'approximate',
  note: '结构大纲为近似提取，可能遗漏构造函数或嵌套不准确。',
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

function summaryFor(label: string | null, ext: string, outline: OutlineSupport, note?: string): string {
  if (!label) {
    return ext
      ? `未识别的文件类型（.${ext}）：无语法高亮与结构大纲，按纯文本显示。`
      : '未识别的文件类型：无语法高亮与结构大纲，按纯文本显示。'
  }
  if (outline === 'full') return `${label}：语法高亮与结构大纲均已支持。`
  if (outline === 'approximate') {
    return note
      ? `${label}：语法高亮可用；${note}`
      : `${label}：语法高亮可用；结构大纲为近似提取，可能不完整。`
  }
  return `${label}：已支持语法高亮，暂无结构大纲。`
}

export function languageSupportForPath(path: string): LanguageSupportInfo {
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
      summary: summaryFor(null, ext, 'none'),
    }
  }
  return {
    level: levelFor(entry.outline, true),
    label: entry.label,
    highlight: entry.highlight,
    outline: entry.outline,
    summary: summaryFor(entry.label, ext, entry.outline, entry.note),
  }
}

/** True when the banner should appear (anything short of full support). */
export function shouldShowLanguageSupportBanner(info: LanguageSupportInfo): boolean {
  return info.level !== 'full'
}
