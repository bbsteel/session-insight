// Copy button for a backed invocation's CLI-resumable agent ID, using the
// same shared logic as the session list (resume_id || id). The collaboration
// payload carries only the backing session id, so the resume id is prefetched
// on hover/press and cached per session; the click never awaits before the
// clipboard write (an async gap can break transient user activation), so a
// cold click copies the session id while the fetch warms the cache for the
// next one. Rendered as a compact toolbar button.

import { useEffect, useRef, useState } from 'react'
import { fetchSession } from '../api'
import { useI18n } from '../i18n'
import { copySessionIdToClipboard } from '../copySessionId'
import type { BackingSessionRefDTO } from '../collaboration/types'

export default function BackingCopyButton({ backing, className }: { backing: BackingSessionRefDTO; className?: string }) {
  const { t } = useI18n()
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle')
  const resumeCache = useRef(new Map<string, string | null>())
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => () => { if (timer.current) clearTimeout(timer.current) }, [])

  // A prior "Copied!"/"Failed" label must not bleed into a different
  // invocation's button after the selection switches.
  useEffect(() => {
    setState('idle')
  }, [backing.session_id])

  const prefetch = () => {
    const key = backing.session_id
    if (resumeCache.current.has(key)) return
    resumeCache.current.set(key, null) // claim the slot; fill on resolve
    fetchSession(key)
      .then((detail) => resumeCache.current.set(key, detail.resume_id ?? null))
      .catch(() => resumeCache.current.set(key, null))
  }

  const copy = () => {
    const key = backing.session_id
    // resume_id stays undefined (copy falls back to the session id) until the
    // prefetch resolves — never await it inside the click handler.
    const resumeId = resumeCache.current.get(key) ?? undefined
    void copySessionIdToClipboard({ id: key, resume_id: resumeId }).then((ok) => {
      setState(ok ? 'copied' : 'failed')
      if (timer.current) clearTimeout(timer.current)
      timer.current = setTimeout(() => setState('idle'), 2000)
    })
  }

  const label = state === 'copied'
    ? t('sidebar.copiedSessionId')
    : state === 'failed'
      ? t('sidebar.copyFailed')
      : t('collaboration.backing.copyAgentId')

  return (
    <button
      type="button"
      onPointerEnter={prefetch}
      onPointerDown={prefetch}
      onClick={(e) => { e.stopPropagation(); copy() }}
      className={className}
      aria-label={t('collaboration.backing.copyAgentId')}
      title={label}
      data-testid="collaboration-copy-agent-id"
      data-state={state}
    >
      {state === 'copied' ? (
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round">
          <polyline points="20 6 9 17 4 12" />
        </svg>
      ) : (
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
          <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
        </svg>
      )}
      <span>{label}</span>
    </button>
  )
}
