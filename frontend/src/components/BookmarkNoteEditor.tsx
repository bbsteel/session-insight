import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'

export interface BookmarkNoteTarget {
  id: string
  agent_type: string
  bookmark_note?: string
}

export interface BookmarkNoteAnchor {
  x: number
  y: number
  /** Open above the pointer when there is not enough room below it. */
  placement: 'below' | 'above'
}

interface BookmarkNoteEditorProps {
  session: BookmarkNoteTarget
  onSave: (session: BookmarkNoteTarget, note: string) => Promise<void>
  onClose: () => void
  /** Optional title override shown above the textarea. */
  title?: string
  /** Renders a compact, non-modal editor beside the pointer. */
  anchor?: BookmarkNoteAnchor
}

/** Modal editor for a session's bookmark note (收藏原因). */
export default function BookmarkNoteEditor({
  session,
  onSave,
  onClose,
  title = '收藏备注',
  anchor,
}: BookmarkNoteEditorProps) {
  const note = session.bookmark_note?.trim() ?? ''
  const [draft, setDraft] = useState(note)
  const [saving, setSaving] = useState(false)
  const anchoredRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    setDraft(session.bookmark_note?.trim() ?? '')
  }, [session.id, session.agent_type, session.bookmark_note])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  useEffect(() => {
    if (!anchor) return
    const onPointerDown = (event: PointerEvent) => {
      if (!anchoredRef.current?.contains(event.target as Node)) onClose()
    }
    document.addEventListener('pointerdown', onPointerDown)
    return () => document.removeEventListener('pointerdown', onPointerDown)
  }, [anchor, onClose])

  const save = async () => {
    const next = draft.trim()
    setSaving(true)
    try {
      await onSave(session, next)
      onClose()
    } finally {
      setSaving(false)
    }
  }

  const editor = (
    <div
      role="dialog"
      ref={anchor ? anchoredRef : undefined}
      aria-modal={anchor ? undefined : true}
      aria-label={title}
      className={anchor
        ? 'fixed z-[calc(var(--z-modal)+1)] w-[min(280px,calc(100vw-2rem))] rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] p-3 shadow-lg'
        : 'fixed left-1/2 top-1/3 z-[calc(var(--z-modal)+1)] w-[min(420px,calc(100vw-2rem))] -translate-x-1/2 rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] p-4 shadow-lg'}
      style={anchor
        ? anchor.placement === 'below'
          ? { left: `min(${anchor.x + 8}px, calc(100vw - 18rem))`, top: anchor.y + 8 }
          : { left: `min(${anchor.x + 8}px, calc(100vw - 18rem))`, bottom: window.innerHeight - anchor.y + 8 }
        : undefined}
    >
      <h3 className="text-nav font-semibold text-[var(--text-primary)]">{title}</h3>
      <p className="mt-1 text-helper text-[var(--text-muted)]">记录为什么收藏这条会话（仅本机保存）。</p>
      <textarea
        value={draft}
        onChange={e => setDraft(e.target.value)}
        maxLength={2000}
        rows={anchor ? 3 : 4}
        autoFocus
        className="mt-3 w-full resize-none rounded border border-[var(--border-muted)] bg-[var(--bg-inset)] px-2 py-1.5 text-helper text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none focus:ring-1 focus:ring-[var(--accent-blue)]/30"
        placeholder="记录收藏原因..."
      />
      <div className="mt-3 flex items-center justify-end gap-2">
        <button
          type="button"
          onClick={onClose}
          disabled={saving}
          className="h-7 px-3 rounded-md border border-[var(--border-default)] text-nav text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] disabled:opacity-60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
        >
          {anchor ? '跳过直接收藏' : '取消'}
        </button>
        <button
          type="button"
          onClick={() => void save()}
          disabled={saving}
          className="h-7 px-3 rounded-md bg-[var(--accent-blue)] text-nav text-white disabled:opacity-60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
        >
          {saving ? '保存中…' : '保存'}
        </button>
      </div>
    </div>
  )

  return createPortal(
    anchor ? editor : <>
      <div
        className="fixed inset-0 z-[calc(var(--z-modal))] bg-[rgba(0,0,0,var(--opacity-overlay,0.4))]"
        onClick={onClose}
      />
      {editor}
    </>,
    document.body,
  )
}
