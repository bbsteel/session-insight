import { useEffect, useRef, useState, type ReactNode } from 'react'
import { refractor } from 'refractor'
import { fetchSessionEdits } from '../api'
import { langForPath } from '../langMap'
import { useIsDark } from '../terminalTheme'
import type { EditCall } from '../types'

// ---------- diff algorithm ----------

type DiffKind = 'equal' | 'remove' | 'add'
interface DiffOp { kind: DiffKind; text: string }

function computeDiff(oldText: string, newText: string): DiffOp[] {
  const a = oldText.split('\n')
  const b = newText.split('\n')
  const m = a.length, n = b.length
  const dp: number[][] = Array.from({ length: m + 1 }, () => new Array(n + 1).fill(0))
  for (let i = 1; i <= m; i++)
    for (let j = 1; j <= n; j++)
      dp[i][j] = a[i - 1] === b[j - 1] ? dp[i - 1][j - 1] + 1 : Math.max(dp[i - 1][j], dp[i][j - 1])

  const ops: DiffOp[] = []
  let i = m, j = n
  while (i > 0 || j > 0) {
    if (i > 0 && j > 0 && a[i - 1] === b[j - 1]) {
      ops.unshift({ kind: 'equal', text: a[i - 1] }); i--; j--
    } else if (j > 0 && (i === 0 || dp[i][j - 1] >= dp[i - 1][j])) {
      ops.unshift({ kind: 'add', text: b[j - 1] }); j--
    } else {
      ops.unshift({ kind: 'remove', text: a[i - 1] }); i--
    }
  }
  return ops
}

// ---------- theme palettes ----------

interface Palette {
  overlay: string
  surface: string
  header: string
  border: string
  borderMuted: string
  text: string
  muted: string
  dim: string
  gutterBg: string
  emptyBg: string
  removeBg: string
  addBg: string
  removeText: string
  addText: string
  removeSig: string
  addSig: string
  accent: string
  accentText: string
  accentBg: string
}

const DARK: Palette = {
  overlay: 'rgba(0,0,0,0.65)', surface: '#1a1b26', header: '#1e2030',
  border: 'rgba(255,255,255,0.1)', borderMuted: 'rgba(255,255,255,0.06)',
  text: '#c0caf5', muted: '#6b7280', dim: '#374151',
  gutterBg: 'rgba(0,0,0,0.25)', emptyBg: 'rgba(0,0,0,0.15)',
  removeBg: '#3d1020', addBg: '#0d3320',
  removeText: '#fca5a5', addText: '#86efac',
  removeSig: '#f87171', addSig: '#4ade80',
  accent: '#7c3aed', accentText: '#a78bfa', accentBg: 'rgba(124,58,237,0.3)',
}

const LIGHT: Palette = {
  overlay: 'rgba(15,18,25,0.35)', surface: '#ffffff', header: '#f5f6f8',
  border: 'rgba(15,18,25,0.14)', borderMuted: 'rgba(15,18,25,0.07)',
  text: '#1f2430', muted: '#6b7280', dim: '#c3c8d4',
  gutterBg: 'rgba(15,18,25,0.04)', emptyBg: 'rgba(15,18,25,0.05)',
  removeBg: '#ffe3e8', addBg: '#dcf5e4',
  removeText: '#9f1239', addText: '#14652f',
  removeSig: '#dc2626', addSig: '#16a34a',
  accent: '#7c3aed', accentText: '#6d28d9', accentBg: 'rgba(124,58,237,0.12)',
}

// ---------- per-line syntax highlighting (refractor → spans) ----------

const TOKENS_DARK: Record<string, string> = {
  keyword: '#c678dd', 'class-name': '#e5c07b', builtin: '#e5c07b',
  function: '#61afef', string: '#98c379', char: '#98c379', regex: '#98c379',
  comment: '#7f848e', number: '#d19a66', boolean: '#d19a66', constant: '#d19a66',
  operator: '#56b6c2', punctuation: '#889096', property: '#e06c75', tag: '#e06c75',
  'attr-name': '#d19a66', 'attr-value': '#98c379', selector: '#e06c75', variable: '#e06c75',
  important: '#c678dd', atrule: '#c678dd', url: '#56b6c2', symbol: '#56b6c2',
}

const TOKENS_LIGHT: Record<string, string> = {
  keyword: '#a626a4', 'class-name': '#c18401', builtin: '#c18401',
  function: '#4078f2', string: '#50a14f', char: '#50a14f', regex: '#50a14f',
  comment: '#a0a1a7', number: '#986801', boolean: '#986801', constant: '#986801',
  operator: '#0184bc', punctuation: '#5c6370', property: '#e45649', tag: '#e45649',
  'attr-name': '#986801', 'attr-value': '#50a14f', selector: '#e45649', variable: '#e45649',
  important: '#a626a4', atrule: '#a626a4', url: '#0184bc', symbol: '#0184bc',
}

interface HastNode {
  type: string
  value?: string
  tagName?: string
  properties?: { className?: string[] }
  children?: HastNode[]
}

function hastToNodes(nodes: HastNode[], colors: Record<string, string>, keyBase: string): ReactNode[] {
  return nodes.map((n, i) => {
    if (n.type === 'text') return n.value ?? ''
    if (n.type === 'element') {
      const cls = n.properties?.className ?? []
      const color = cls.map(c => colors[c]).find(Boolean)
      return (
        <span key={keyBase + i} style={color ? { color } : undefined}>
          {hastToNodes(n.children ?? [], colors, `${keyBase}${i}-`)}
        </span>
      )
    }
    return null
  })
}

function resolveLang(lang: string): string | null {
  if (refractor.registered(lang)) return lang
  if (lang === 'tsx' && refractor.registered('typescript')) return 'typescript'
  return null
}

// One diff line, syntax-colored when the language is known; falls back to the
// kind-based flat color otherwise. Tokenizing per line loses multi-line
// constructs (block comments), an accepted trade-off for row-level diffs.
function TokenLine({ text, lang, colors, fallback }: { text: string; lang: string | null; colors: Record<string, string>; fallback: string }) {
  if (!text) return <>{' '}</>
  if (!lang) return <span style={{ color: fallback }}>{text}</span>
  try {
    const root = refractor.highlight(text, lang) as unknown as { children: HastNode[] }
    return <>{hastToNodes(root.children, colors, 'h')}</>
  } catch {
    return <span style={{ color: fallback }}>{text}</span>
  }
}

// ---------- inline view ----------

interface NumberedOp { kind: DiffKind; text: string; oldNum?: number; newNum?: number }

function numberOps(diff: DiffOp[]): NumberedOp[] {
  let o = 1, n = 1
  return diff.map(op => {
    const row: NumberedOp = { ...op }
    if (op.kind !== 'add')    { row.oldNum = o++ }
    if (op.kind !== 'remove') { row.newNum = n++ }
    return row
  })
}

interface ViewProps { diff: DiffOp[]; pal: Palette; lang: string | null; tokens: Record<string, string> }

function InlineDiff({ diff, pal, lang, tokens }: ViewProps) {
  const lines = numberOps(diff)
  return (
    <table className="w-full border-collapse" style={{ fontFamily: '"JetBrains Mono", "Consolas", "Menlo", monospace', fontSize: 12 }}>
      <tbody>
        {lines.map((row, i) => {
          const bg = row.kind === 'remove' ? pal.removeBg : row.kind === 'add' ? pal.addBg : 'transparent'
          const fallback = row.kind === 'remove' ? pal.removeText : row.kind === 'add' ? pal.addText : pal.text
          const sigColor = row.kind === 'remove' ? pal.removeSig : row.kind === 'add' ? pal.addSig : 'transparent'
          const sig = row.kind === 'remove' ? '-' : row.kind === 'add' ? '+' : ' '
          return (
            <tr key={i} style={{ background: bg }}>
              <td style={{ width: 44, minWidth: 44, textAlign: 'right', paddingRight: 8, paddingLeft: 4, color: pal.muted, userSelect: 'none', background: pal.gutterBg, borderRight: `1px solid ${pal.borderMuted}` }}>
                {row.oldNum ?? ''}
              </td>
              <td style={{ width: 44, minWidth: 44, textAlign: 'right', paddingRight: 8, paddingLeft: 4, color: pal.muted, userSelect: 'none', background: pal.gutterBg, borderRight: `1px solid ${pal.borderMuted}` }}>
                {row.newNum ?? ''}
              </td>
              <td style={{ width: 20, minWidth: 20, paddingLeft: 6, color: sigColor, userSelect: 'none', fontWeight: 600 }}>{sig}</td>
              <td style={{ paddingLeft: 4, paddingRight: 12, whiteSpace: 'pre', color: fallback, width: '100%' }}>
                <TokenLine text={row.text} lang={lang} colors={tokens} fallback={fallback} />
              </td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}

// ---------- split view ----------

interface SplitRow {
  left?: { kind: 'remove' | 'equal'; text: string; num: number }
  right?: { kind: 'add' | 'equal'; text: string; num: number }
}

const SPLIT_GUTTER_WIDTH = 44
const SPLIT_CONTENT_PADDING_CH = 4

function visualLineLength(text: string): number {
  return text.replace(/\t/g, '    ').length
}

function splitPaneWidthCh(rows: SplitRow[]): number {
  let max = 0
  for (const row of rows) {
    if (row.left) max = Math.max(max, visualLineLength(row.left.text))
    if (row.right) max = Math.max(max, visualLineLength(row.right.text))
  }
  return Math.max(1, max + SPLIT_CONTENT_PADDING_CH)
}

function buildSplitRows(diff: DiffOp[]): SplitRow[] {
  const rows: SplitRow[] = []
  let i = 0, o = 1, n = 1
  while (i < diff.length) {
    const op = diff[i]
    if (op.kind === 'equal') {
      rows.push({ left: { kind: 'equal', text: op.text, num: o++ }, right: { kind: 'equal', text: op.text, num: n++ } })
      i++
    } else {
      const removes: string[] = [], adds: string[] = []
      while (i < diff.length && diff[i].kind === 'remove') { removes.push(diff[i].text); i++ }
      while (i < diff.length && diff[i].kind === 'add')    { adds.push(diff[i].text); i++ }
      const count = Math.max(removes.length, adds.length)
      for (let j = 0; j < count; j++) {
        const row: SplitRow = {}
        if (j < removes.length) row.left  = { kind: 'remove', text: removes[j], num: o++ }
        if (j < adds.length)    row.right = { kind: 'add',    text: adds[j],    num: n++ }
        rows.push(row)
      }
    }
  }
  return rows
}

function SplitPane({
  side,
  pal,
  lang,
  tokens,
  softWrap,
  paneWidthCh,
}: {
  side: SplitRow['left'] | SplitRow['right'] | undefined
  softWrap: boolean
  paneWidthCh: number
} & Omit<ViewProps, 'diff'>) {
  if (!side) {
    return (
      <div style={{ display: 'grid', gridTemplateColumns: `${SPLIT_GUTTER_WIDTH}px minmax(0, 1fr)`, minWidth: 0, background: pal.emptyBg }}>
        <div style={{ borderRight: `1px solid ${pal.borderMuted}` }} />
        <div>&nbsp;</div>
      </div>
    )
  }
  const isRemove = side.kind === 'remove'
  const isAdd    = side.kind === 'add'
  const fallback = isRemove ? pal.removeText : isAdd ? pal.addText : pal.text
  const sigColor = isRemove ? pal.removeSig : isAdd ? pal.addSig : 'transparent'
  const sig = isRemove ? '-' : isAdd ? '+' : ' '
  const bg = isRemove ? pal.removeBg : isAdd ? pal.addBg : 'transparent'
  const gutterBg = bg === 'transparent' ? pal.gutterBg : bg
  return (
    <div style={{ display: 'grid', gridTemplateColumns: `${SPLIT_GUTTER_WIDTH}px minmax(0, 1fr)`, minWidth: 0, background: bg }}>
      <div style={{ textAlign: 'right', paddingRight: 6, paddingLeft: 4, color: pal.muted, userSelect: 'none', background: gutterBg, borderRight: `1px solid ${pal.borderMuted}` }}>
        {side.num}
      </div>
      <div style={{ overflow: 'hidden', minWidth: 0 }}>
        <div style={{ display: 'flex', width: softWrap ? '100%' : `max(100%, ${paneWidthCh}ch)`, transform: softWrap ? undefined : 'translateX(calc(-1 * var(--diff-scroll-x, 0px)))' }}>
          <span style={{ width: 18, minWidth: 18, paddingLeft: 5, color: sigColor, userSelect: 'none', fontWeight: 600, flexShrink: 0 }}>{sig}</span>
          <span style={{ flex: 1, paddingLeft: 4, paddingRight: 8, whiteSpace: softWrap ? 'pre-wrap' : 'pre', wordBreak: softWrap ? 'break-all' : 'normal', color: fallback, minWidth: 0 }}>
            <TokenLine text={side.text} lang={lang} colors={tokens} fallback={fallback} />
          </span>
        </div>
      </div>
    </div>
  )
}

function SplitDiff({ diff, pal, lang, tokens, softWrap, resetKey }: ViewProps & { softWrap: boolean; resetKey: number }) {
  const rows = buildSplitRows(diff)
  const paneWidthCh = splitPaneWidthCh(rows)
  const verticalRef = useRef<HTMLDivElement>(null)
  const horizontalRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    verticalRef.current?.style.setProperty('--diff-scroll-x', '0px')
    verticalRef.current?.scrollTo(0, 0)
    horizontalRef.current?.scrollTo(0, 0)
  }, [resetKey, softWrap])

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', minHeight: 0, background: pal.surface, fontFamily: '"JetBrains Mono", "Consolas", "Menlo", monospace', fontSize: 12 }}>
      <div ref={verticalRef} style={{ flex: 1, minHeight: 0, overflowY: 'auto', overflowX: 'hidden' }}>
        {rows.map((row, i) => {
          return (
            <div key={i} style={{ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 1fr)', minWidth: 0 }}>
              <SplitPane side={row.left} pal={pal} lang={lang} tokens={tokens} softWrap={softWrap} paneWidthCh={paneWidthCh} />
              <div style={{ minWidth: 0, borderLeft: `1px solid ${pal.border}` }}>
                <SplitPane side={row.right} pal={pal} lang={lang} tokens={tokens} softWrap={softWrap} paneWidthCh={paneWidthCh} />
              </div>
            </div>
          )
        })}
      </div>
      {!softWrap && (
        <div
          ref={horizontalRef}
          onScroll={e => verticalRef.current?.style.setProperty('--diff-scroll-x', `${e.currentTarget.scrollLeft}px`)}
          style={{ flexShrink: 0, height: 16, overflowX: 'auto', overflowY: 'hidden', borderTop: `1px solid ${pal.borderMuted}`, background: pal.header }}
        >
          <div style={{ height: 1, width: `max(100%, calc(${paneWidthCh}ch + 50% + ${SPLIT_GUTTER_WIDTH}px))` }} />
        </div>
      )}
    </div>
  )
}

// ---------- modal ----------

interface Props {
  sessionId: string
  onClose: () => void
  initialIdx?: number
}

export default function DiffModal({ sessionId, onClose, initialIdx = 0 }: Props) {
  const [edits, setEdits] = useState<EditCall[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [idx, setIdx] = useState(initialIdx)
  const [viewMode, setViewMode] = useState<'inline' | 'split'>('split')
  const [maximized, setMaximized] = useState(false)
  const [softWrap, setSoftWrap] = useState(false)
  const scrollRef = useRef<HTMLDivElement>(null)
  const isDark = useIsDark()
  const pal = isDark ? DARK : LIGHT
  const tokens = isDark ? TOKENS_DARK : TOKENS_LIGHT

  useEffect(() => {
    fetchSessionEdits(sessionId)
      .then(data => { setEdits(data); setLoading(false) })
      .catch(err => { setError(err.message); setLoading(false) })
  }, [sessionId])

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const key = e.key.toLowerCase()
      if (e.key === 'Escape') onClose()
      else if (e.key === 'ArrowRight' || key === 'l') setIdx(i => Math.min(i + 1, edits.length - 1))
      else if (e.key === 'ArrowLeft'  || key === 'h') setIdx(i => Math.max(i - 1, 0))
      else if (key === 'm') setMaximized(m => !m)
      else if (key === 'w') setSoftWrap(w => !w)
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [onClose, edits.length])

  // Reset scroll when switching edit
  useEffect(() => { scrollRef.current?.scrollTo(0, 0) }, [idx])

  const edit = edits[idx]
  const diff = edit ? computeDiff(edit.old_string, edit.new_string) : []
  const removeCount = diff.filter(d => d.kind === 'remove').length
  const addCount    = diff.filter(d => d.kind === 'add').length
  const lang = edit ? resolveLang(langForPath(edit.file_path)) : null
  const shortcutHint = '快捷键：Esc 关闭 · ←/→ 或 H/L 切换编辑 · M 最大化 · W 换行'

  const modalStyle: React.CSSProperties = maximized
    ? { width: '100vw', height: '100vh', display: 'flex', flexDirection: 'column', background: pal.surface, border: 'none', borderRadius: 0, overflow: 'hidden' }
    : { width: '92vw', maxWidth: 1200, height: '88vh', display: 'flex', flexDirection: 'column', background: pal.surface, border: `1px solid ${pal.border}`, borderRadius: 10, overflow: 'hidden', boxShadow: '0 24px 80px rgba(0,0,0,0.35)' }

  return (
    <div
      style={{ position: 'fixed', inset: 0, zIndex: 300, display: 'flex', alignItems: 'center', justifyContent: 'center', background: pal.overlay, backdropFilter: 'blur(2px)' }}
      onClick={onClose}
    >
      <div
        onClick={e => e.stopPropagation()}
        style={modalStyle}
      >
        {/* header */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '0 14px', height: 44, flexShrink: 0, borderBottom: `1px solid ${pal.border}`, background: pal.header }}>
          {/* file path */}
          <div style={{ flex: 1, minWidth: 0, display: 'flex', alignItems: 'center', gap: 8, overflow: 'hidden' }}>
            <span style={{ color: pal.accent, fontSize: 13 }}>✎</span>
            <span style={{ color: pal.text, fontSize: 13, fontFamily: '"JetBrains Mono", "Consolas", "Menlo", monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {edit?.file_path ?? '—'}
            </span>
            {edit && (
              <span style={{ fontSize: 11, color: pal.muted, whiteSpace: 'nowrap' }}>
                <span style={{ color: pal.removeSig }}>-{removeCount}</span>
                {' '}
                <span style={{ color: pal.addSig }}>+{addCount}</span>
              </span>
            )}
          </div>

          {/* shortcut hint */}
          <div
            title={shortcutHint}
            style={{
              flex: '0 1 520px',
              minWidth: 240,
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
              textAlign: 'right',
              color: pal.muted,
              fontSize: 12,
            }}
          >
            {shortcutHint}
          </div>

          {/* navigation */}
          {edits.length > 1 && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 4, flexShrink: 0 }}>
              <button
                onClick={() => setIdx(i => Math.max(i - 1, 0))}
                disabled={idx === 0}
                style={{ width: 26, height: 26, borderRadius: 5, border: `1px solid ${pal.border}`, background: 'transparent', color: idx === 0 ? pal.dim : pal.muted, cursor: idx === 0 ? 'default' : 'pointer', fontSize: 13 }}
              >←</button>
              <span style={{ fontSize: 12, color: pal.muted, minWidth: 52, textAlign: 'center' }}>{idx + 1} / {edits.length}</span>
              <button
                onClick={() => setIdx(i => Math.min(i + 1, edits.length - 1))}
                disabled={idx === edits.length - 1}
                style={{ width: 26, height: 26, borderRadius: 5, border: `1px solid ${pal.border}`, background: 'transparent', color: idx === edits.length - 1 ? pal.dim : pal.muted, cursor: idx === edits.length - 1 ? 'default' : 'pointer', fontSize: 13 }}
              >→</button>
            </div>
          )}

          {/* view toggle */}
          <div style={{ display: 'flex', borderRadius: 6, border: `1px solid ${pal.border}`, overflow: 'hidden', flexShrink: 0 }}>
            {(['inline', 'split'] as const).map(mode => (
              <button
                key={mode}
                onClick={() => setViewMode(mode)}
                style={{ padding: '3px 10px', fontSize: 12, border: 'none', cursor: 'pointer', background: viewMode === mode ? pal.accentBg : 'transparent', color: viewMode === mode ? pal.accentText : pal.muted, borderRight: mode === 'inline' ? `1px solid ${pal.border}` : 'none' }}
              >
                {mode === 'inline' ? '合并' : '并列'}
              </button>
            ))}
          </div>

          {/* soft wrap */}
          <button
            onClick={() => setSoftWrap(w => !w)}
            style={{ padding: '3px 10px', fontSize: 12, borderRadius: 6, border: `1px solid ${pal.border}`, background: softWrap ? pal.accentBg : 'transparent', color: softWrap ? pal.accentText : pal.muted, cursor: 'pointer', flexShrink: 0 }}
            title={softWrap ? '关闭软换行 (W)' : '开启软换行 (W)'}
          >
            换行
          </button>

          {/* maximize */}
          <button
            onClick={() => setMaximized(m => !m)}
            style={{ width: 26, height: 26, borderRadius: 5, border: `1px solid ${pal.border}`, background: 'transparent', color: pal.muted, cursor: 'pointer', fontSize: 14, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}
            title={maximized ? '还原 (M)' : '最大化 (M)'}
          >
            {maximized ? '❐' : '⛶'}
          </button>

          {/* close */}
          <button
            onClick={onClose}
            style={{ width: 26, height: 26, borderRadius: 5, border: `1px solid ${pal.border}`, background: 'transparent', color: pal.muted, cursor: 'pointer', fontSize: 16, flexShrink: 0 }}
            title="关闭 (Esc)"
          >×</button>
        </div>

        {/* turn badge */}
        {edit && (
          <div style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '4px 14px', background: pal.header, borderBottom: `1px solid ${pal.borderMuted}`, fontSize: 11, color: pal.muted, flexShrink: 0 }}>
            <span>Turn {edit.turn_index}{edit.replace_all ? ' · replace_all' : ''}</span>
            <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {shortcutHint}
            </span>
          </div>
        )}

        {/* body */}
        <div ref={scrollRef} style={{ flex: 1, minHeight: 0, overflow: viewMode === 'split' ? 'hidden' : 'auto', background: pal.surface }}>
          {loading && (
            <div style={{ padding: 32, textAlign: 'center', color: pal.muted, fontSize: 13 }}>加载中…</div>
          )}
          {error && (
            <div style={{ padding: 32, textAlign: 'center', color: pal.removeSig, fontSize: 13 }}>{error}</div>
          )}
          {!loading && !error && edits.length === 0 && (
            <div style={{ padding: 32, textAlign: 'center', color: pal.muted, fontSize: 13 }}>本次会话没有 Edit 操作。</div>
          )}
          {!loading && edit && (
            viewMode === 'inline'
              ? <InlineDiff diff={diff} pal={pal} lang={lang} tokens={tokens} />
              : <SplitDiff diff={diff} pal={pal} lang={lang} tokens={tokens} softWrap={softWrap} resetKey={idx} />
          )}
        </div>
      </div>
    </div>
  )
}
