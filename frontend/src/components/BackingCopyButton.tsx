// Copy button for a backed invocation's CLI-resumable agent ID, using the
// same shared logic as the session list (resume_id || id). The collaboration
// payload carries only the backing session id, so the resume id is fetched
// lazily from the session detail and cached per session; on fetch failure it
// falls back to the session id. Rendered as a compact toolbar icon button.

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

  const copy = async () => {
    const key = backing.session_id
    let resumeId = resumeCache.current.get(key)
    if (resumeId === undefined) {
      try {
        const detail = await fetchSession(key)
        resumeId = detail.resume_id ?? null
      } catch {
        resumeId = null
      }
      resumeCache.current.set(key, resumeId)
    }
    const ok = await copySessionIdToClipboard({ id: key, resume_id: resumeId })
    setState(ok ? 'copied' : 'failed')
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(() => setState('idle'), 2000)
  }

  const label = state === 'copied'
    ? t('sidebar.copiedSessionId')
    : state === 'failed'
      ? t('sidebar.copyFailed')
      : t('collaboration.backing.copyAgentId')

  return (
    <button
      type="button"
      onClick={(e) => { e.stopPropagation(); void copy() }}
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
