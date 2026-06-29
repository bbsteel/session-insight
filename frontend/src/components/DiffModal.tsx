import { useEffect, useRef, useState } from 'react'
import { fetchSessionEdits } from '../api'
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

function InlineDiff({ diff }: { diff: DiffOp[] }) {
  const lines = numberOps(diff)
  return (
    <table className="w-full border-collapse" style={{ fontFamily: '"JetBrains Mono", "Menlo", monospace', fontSize: 12 }}>
      <tbody>
        {lines.map((row, i) => {
          const bg =
            row.kind === 'remove' ? '#3d1020' :
            row.kind === 'add'    ? '#0d3320' :
            'transparent'
          const textColor =
            row.kind === 'remove' ? '#fca5a5' :
            row.kind === 'add'    ? '#86efac' :
            '#c0caf5'
          const sigColor =
            row.kind === 'remove' ? '#f87171' :
            row.kind === 'add'    ? '#4ade80' :
            'transparent'
          const sig = row.kind === 'remove' ? '-' : row.kind === 'add' ? '+' : ' '
          return (
            <tr key={i} style={{ background: bg }}>
              <td style={{ width: 44, minWidth: 44, textAlign: 'right', paddingRight: 8, paddingLeft: 4, color: '#6b7280', userSelect: 'none', background: 'rgba(0,0,0,0.25)', borderRight: '1px solid rgba(255,255,255,0.06)' }}>
                {row.oldNum ?? ''}
              </td>
              <td style={{ width: 44, minWidth: 44, textAlign: 'right', paddingRight: 8, paddingLeft: 4, color: '#6b7280', userSelect: 'none', background: 'rgba(0,0,0,0.25)', borderRight: '1px solid rgba(255,255,255,0.06)' }}>
                {row.newNum ?? ''}
              </td>
              <td style={{ width: 20, minWidth: 20, paddingLeft: 6, color: sigColor, userSelect: 'none', fontWeight: 600 }}>{sig}</td>
              <td style={{ paddingLeft: 4, paddingRight: 12, whiteSpace: 'pre', color: textColor, width: '100%' }}>{row.text || ' '}</td>
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

function SplitCell({ side }: { side?: SplitRow['left'] | SplitRow['right'] }) {
  if (!side) return (
    <td colSpan={3} style={{ background: 'rgba(0,0,0,0.15)', width: '50%' }}>&nbsp;</td>
  )
  const isRemove = side.kind === 'remove'
  const isAdd    = (side as { kind: string }).kind === 'add'
  const bg        = isRemove ? '#3d1020' : isAdd ? '#0d3320' : 'transparent'
  const textColor = isRemove ? '#fca5a5' : isAdd ? '#86efac' : '#c0caf5'
  const sigColor  = isRemove ? '#f87171' : isAdd ? '#4ade80' : 'transparent'
  const sig = isRemove ? '-' : isAdd ? '+' : ' '
  return (
    <>
      <td style={{ width: 40, minWidth: 40, textAlign: 'right', paddingRight: 6, paddingLeft: 4, color: '#6b7280', userSelect: 'none', background: bg === 'transparent' ? 'rgba(0,0,0,0.25)' : bg, borderRight: '1px solid rgba(255,255,255,0.06)' }}>
        {side.num}
      </td>
      <td style={{ width: 18, minWidth: 18, paddingLeft: 5, color: sigColor, userSelect: 'none', fontWeight: 600, background: bg }}>{sig}</td>
      <td style={{ paddingLeft: 4, paddingRight: 8, whiteSpace: 'pre', color: textColor, background: bg, width: 'calc(50% - 58px)' }}>{side.text || ' '}</td>
    </>
  )
}

function SplitDiff({ diff }: { diff: DiffOp[] }) {
  const rows = buildSplitRows(diff)
  return (
    <table className="w-full border-collapse" style={{ fontFamily: '"JetBrains Mono", "Menlo", monospace', fontSize: 12, tableLayout: 'fixed' }}>
      <colgroup>
        <col style={{ width: 40 }} /><col style={{ width: 18 }} /><col />
        <td style={{ width: 1, background: 'rgba(255,255,255,0.08)' }} />
        <col style={{ width: 40 }} /><col style={{ width: 18 }} /><col />
      </colgroup>
      <tbody>
        {rows.map((row, i) => (
          <tr key={i}>
            <SplitCell side={row.left} />
            <td style={{ width: 1, background: 'rgba(255,255,255,0.08)', padding: 0 }} />
            <SplitCell side={row.right} />
          </tr>
        ))}
      </tbody>
    </table>
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
  const [viewMode, setViewMode] = useState<'inline' | 'split'>('inline')
  const scrollRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    fetchSessionEdits(sessionId)
      .then(data => { setEdits(data); setLoading(false) })
      .catch(err => { setError(err.message); setLoading(false) })
  }, [sessionId])

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
      else if (e.key === 'ArrowRight' || e.key === 'l') setIdx(i => Math.min(i + 1, edits.length - 1))
      else if (e.key === 'ArrowLeft'  || e.key === 'h') setIdx(i => Math.max(i - 1, 0))
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

  return (
    <div
      style={{ position: 'fixed', inset: 0, zIndex: 50, display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'rgba(0,0,0,0.65)', backdropFilter: 'blur(2px)' }}
      onClick={onClose}
    >
      <div
        onClick={e => e.stopPropagation()}
        style={{ width: '92vw', maxWidth: 1200, height: '88vh', display: 'flex', flexDirection: 'column', background: '#1a1b26', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 10, overflow: 'hidden', boxShadow: '0 24px 80px rgba(0,0,0,0.6)' }}
      >
        {/* header */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '0 14px', height: 44, flexShrink: 0, borderBottom: '1px solid rgba(255,255,255,0.08)', background: '#1e2030' }}>
          {/* file path */}
          <div style={{ flex: 1, minWidth: 0, display: 'flex', alignItems: 'center', gap: 8, overflow: 'hidden' }}>
            <span style={{ color: '#7c3aed', fontSize: 13 }}>✎</span>
            <span style={{ color: '#c0caf5', fontSize: 13, fontFamily: '"JetBrains Mono", monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {edit?.file_path ?? '—'}
            </span>
            {edit && (
              <span style={{ fontSize: 11, color: '#6b7280', whiteSpace: 'nowrap' }}>
                <span style={{ color: '#f87171' }}>-{removeCount}</span>
                {' '}
                <span style={{ color: '#4ade80' }}>+{addCount}</span>
              </span>
            )}
          </div>

          {/* navigation */}
          {edits.length > 1 && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 4, flexShrink: 0 }}>
              <button
                onClick={() => setIdx(i => Math.max(i - 1, 0))}
                disabled={idx === 0}
                style={{ width: 26, height: 26, borderRadius: 5, border: '1px solid rgba(255,255,255,0.1)', background: 'transparent', color: idx === 0 ? '#374151' : '#9ca3af', cursor: idx === 0 ? 'default' : 'pointer', fontSize: 13 }}
              >←</button>
              <span style={{ fontSize: 12, color: '#6b7280', minWidth: 52, textAlign: 'center' }}>{idx + 1} / {edits.length}</span>
              <button
                onClick={() => setIdx(i => Math.min(i + 1, edits.length - 1))}
                disabled={idx === edits.length - 1}
                style={{ width: 26, height: 26, borderRadius: 5, border: '1px solid rgba(255,255,255,0.1)', background: 'transparent', color: idx === edits.length - 1 ? '#374151' : '#9ca3af', cursor: idx === edits.length - 1 ? 'default' : 'pointer', fontSize: 13 }}
              >→</button>
            </div>
          )}

          {/* view toggle */}
          <div style={{ display: 'flex', borderRadius: 6, border: '1px solid rgba(255,255,255,0.1)', overflow: 'hidden', flexShrink: 0 }}>
            {(['inline', 'split'] as const).map(mode => (
              <button
                key={mode}
                onClick={() => setViewMode(mode)}
                style={{ padding: '3px 10px', fontSize: 12, border: 'none', cursor: 'pointer', background: viewMode === mode ? 'rgba(124,58,237,0.3)' : 'transparent', color: viewMode === mode ? '#a78bfa' : '#6b7280', borderRight: mode === 'inline' ? '1px solid rgba(255,255,255,0.1)' : 'none' }}
              >
                {mode === 'inline' ? '合并' : '并列'}
              </button>
            ))}
          </div>

          {/* close */}
          <button
            onClick={onClose}
            style={{ width: 26, height: 26, borderRadius: 5, border: '1px solid rgba(255,255,255,0.1)', background: 'transparent', color: '#6b7280', cursor: 'pointer', fontSize: 16, flexShrink: 0 }}
            title="关闭 (Esc)"
          >×</button>
        </div>

        {/* turn badge */}
        {edit && (
          <div style={{ padding: '4px 14px', background: '#1e2030', borderBottom: '1px solid rgba(255,255,255,0.05)', fontSize: 11, color: '#6b7280', flexShrink: 0 }}>
            Turn {edit.turn_index}{edit.replace_all ? ' · replace_all' : ''}
          </div>
        )}

        {/* body */}
        <div ref={scrollRef} style={{ flex: 1, overflow: 'auto', background: '#1a1b26' }}>
          {loading && (
            <div style={{ padding: 32, textAlign: 'center', color: '#6b7280', fontSize: 13 }}>加载中…</div>
          )}
          {error && (
            <div style={{ padding: 32, textAlign: 'center', color: '#f87171', fontSize: 13 }}>{error}</div>
          )}
          {!loading && !error && edits.length === 0 && (
            <div style={{ padding: 32, textAlign: 'center', color: '#6b7280', fontSize: 13 }}>本次会话没有 Edit 操作。</div>
          )}
          {!loading && edit && (
            viewMode === 'inline'
              ? <InlineDiff diff={diff} />
              : <SplitDiff diff={diff} />
          )}
        </div>
      </div>
    </div>
  )
}
