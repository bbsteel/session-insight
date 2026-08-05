import { useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { APIError, fetchResumePlan, fetchSessionTerminal, focusSessionTerminal, resumeSession } from '../api'
import type { ResumePlan, SessionDetail, SessionTerminalStatus } from '../types'
import { useI18n } from '../i18n'

interface Props {
  session: SessionDetail
}

function terminalLabel(status: SessionTerminalStatus, t: (key: string, vars?: Record<string, string | number>) => string): string {
  if (!status.terminal_name) return ''
  return status.tab_id ? t('resume.tabLabel', { terminal: status.terminal_name, tab: status.tab_id }) : status.terminal_name
}

export default function ResumeTerminalControl({ session }: Props) {
  const { t } = useI18n()
  const [plan, setPlan] = useState<ResumePlan | null>(null)
  const [terminal, setTerminal] = useState<SessionTerminalStatus | null>(null)
  const [busy, setBusy] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  const [confirmUnsafe, setConfirmUnsafe] = useState(false)
  const [feedback, setFeedback] = useState('')
  const [error, setError] = useState('')
  const [menuPosition, setMenuPosition] = useState({ left: 0, top: 0 })
  const rootRef = useRef<HTMLDivElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)

  const positionMenu = useCallback(() => {
    const rect = rootRef.current?.getBoundingClientRect()
    if (rect) setMenuPosition({ left: rect.left, top: rect.bottom + 4 })
  }, [])

  const openMenu = useCallback(() => {
    positionMenu()
    setMenuOpen(true)
  }, [positionMenu])

  const refresh = useCallback(async () => {
    const next = await fetchResumePlan(session.id, session.agent_type)
    setPlan(next)
    setTerminal(next.terminal)
    return next
  }, [session.agent_type, session.id])

  useEffect(() => {
    let cancelled = false
    setPlan(null)
    setTerminal(null)
    setFeedback('')
    setError('')
    void fetchResumePlan(session.id, session.agent_type)
      .then(next => {
        if (cancelled) return
        setPlan(next)
        setTerminal(next.terminal)
      })
      .catch(err => {
        if (!cancelled) setError(errorMessage(err, t))
      })
    return () => { cancelled = true }
  }, [session.agent_type, session.id, t])

  useEffect(() => {
    if (terminal?.state !== 'launching') return
    const timer = window.setInterval(() => {
      void fetchSessionTerminal(session.id, session.agent_type)
        .then(next => {
          setTerminal(next)
          if (next.state === 'active') setFeedback(t('resume.verified'))
          if (next.state === 'stopped') setError(t('resume.notVerified'))
        })
        .catch(() => {})
    }, 1200)
    return () => window.clearInterval(timer)
  }, [session.agent_type, session.id, t, terminal?.state])

  useEffect(() => {
    if (!menuOpen) return
    const close = (event: MouseEvent) => {
      const target = event.target as Node
      if (!rootRef.current?.contains(target) && !menuRef.current?.contains(target)) {
        setMenuOpen(false)
        setConfirmUnsafe(false)
      }
    }
    const reposition = () => positionMenu()
    document.addEventListener('mousedown', close)
    window.addEventListener('resize', reposition)
    window.addEventListener('scroll', reposition, true)
    return () => {
      document.removeEventListener('mousedown', close)
      window.removeEventListener('resize', reposition)
      window.removeEventListener('scroll', reposition, true)
    }
  }, [menuOpen, positionMenu])

  const launch = async (unsafe: boolean) => {
    setBusy(true)
    setError('')
    setFeedback(t('resume.starting'))
    setMenuOpen(false)
    setConfirmUnsafe(false)
    try {
      const result = await resumeSession(session.id, session.agent_type, unsafe)
      setTerminal(result.terminal)
      setFeedback(t('resume.terminalOpened', { terminal: result.terminal.terminal_name || t('resume.terminal') }))
      await refresh().catch(() => {})
    } catch (err) {
      setFeedback('')
      setError(errorMessage(err, t))
    } finally {
      setBusy(false)
    }
  }

  const focus = async () => {
    setBusy(true)
    setError('')
    try {
      const result = await focusSessionTerminal(session.id, session.agent_type)
      setFeedback(result.foreground ? t('resume.focused') : t('resume.tabSelected'))
    } catch (err) {
      setError(errorMessage(err, t))
    } finally {
      setBusy(false)
    }
  }

  const copyCommand = async () => {
    try {
      const current = plan ?? await refresh()
      if (!current.command) throw new Error('command unavailable')
      await navigator.clipboard.writeText(current.command)
      setFeedback(t('resume.commandCopied'))
      setMenuOpen(false)
    } catch (err) {
      setError(errorMessage(err, t))
    }
  }

  const state = terminal?.state ?? (plan?.status === 'session_running' ? 'active_unknown' : 'none')
  const active = state === 'active' || state === 'active_unknown'
  const canFocus = state === 'active' && terminal?.focusable
  const ready = plan?.status === 'ready'
  const primaryLabel = busy
    ? t('resume.working')
    : state === 'launching'
      ? t('resume.starting')
      : canFocus
        ? terminalLabel(terminal, t) || t('resume.returnTerminal')
        : active
          ? terminal?.terminal_name
            ? t('resume.runningIn', { terminal: terminal.terminal_name })
            : t('resume.runningUnknown')
          : ready
            ? t('resume.continue')
            : plan?.status === 'cwd_unavailable'
              ? t('resume.workspaceMissing')
              : t('resume.unavailable')

  const primary = () => {
    if (canFocus) void focus()
    else if (ready && !active) void launch(false)
    else openMenu()
  }

  return (
    <div ref={rootRef} className="relative inline-flex items-center" data-testid="resume-terminal-control">
      <button
        type="button"
        onClick={primary}
        disabled={busy || !plan}
        className={`h-7 rounded-l-md border border-r-0 px-2 inline-flex max-w-[13rem] items-center gap-1.5 text-nav focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] disabled:opacity-50 ${
          active
            ? 'border-[color-mix(in_srgb,var(--accent-green)_45%,var(--border-default))] bg-[color-mix(in_srgb,var(--accent-green)_12%,transparent)] text-[var(--accent-green)]'
            : 'border-[var(--border-default)] text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
        }`}
        title={error || feedback || primaryLabel}
        data-testid="resume-primary-button"
      >
        <span className={`inline-block h-1.5 w-1.5 shrink-0 rounded-full ${state === 'launching' ? 'animate-pulse bg-[var(--accent-blue)]' : active ? 'bg-[var(--accent-green)]' : ready ? 'bg-[var(--text-muted)]' : 'bg-[var(--warning)]'}`} />
        <span className="truncate">{primaryLabel}</span>
      </button>
      <button
        type="button"
        onClick={() => {
          if (menuOpen) setMenuOpen(false)
          else openMenu()
          setConfirmUnsafe(false)
        }}
        disabled={!plan}
        className="h-7 w-6 rounded-r-md border border-[var(--border-default)] text-meta text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] disabled:opacity-50"
        aria-label={t('resume.actions')}
        aria-expanded={menuOpen}
        data-testid="resume-menu-button"
      >
        ▾
      </button>
      {menuOpen && plan && createPortal(
        <div
          ref={menuRef}
          className="fixed z-[10000] w-80 rounded-lg border border-[var(--border-default)] bg-[var(--bg-surface)] p-2 shadow-xl"
          style={menuPosition}
          role="menu"
          data-testid="resume-menu"
        >
          <div className="px-2 py-1.5">
            <div className="text-nav font-medium text-[var(--text-primary)]">
              {terminal?.terminal_name ? terminalLabel(terminal, t) : t('resume.terminalUnknown')}
            </div>
            <div className="mt-0.5 truncate text-meta text-[var(--text-muted)]" title={plan.cwd}>{plan.cwd || t('resume.noWorkspace')}</div>
            <div className="mt-1 text-meta text-[var(--text-secondary)]">
              {terminal?.confidence === 'exact'
                ? t('resume.bindingExact')
                : terminal?.confidence === 'instance'
                  ? t('resume.bindingInstance')
                  : t('resume.bindingUnknown')}
            </div>
          </div>
          {(error || feedback) && (
            <div className={`mx-2 mb-1 rounded px-2 py-1 text-meta ${error ? 'bg-[color-mix(in_srgb,var(--error)_12%,transparent)] text-[var(--error)]' : 'bg-[color-mix(in_srgb,var(--accent-green)_12%,transparent)] text-[var(--accent-green)]'}`} role="status">
              {error || feedback}
            </div>
          )}
          {canFocus && (
            <button type="button" onClick={() => void focus()} className="w-full rounded px-2 py-1.5 text-left text-nav text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)]" role="menuitem">
              {t('resume.returnTerminal')}
            </button>
          )}
          {ready && !active && (
            <button type="button" onClick={() => void launch(false)} className="w-full rounded px-2 py-1.5 text-left text-nav text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)]" role="menuitem">
              {t('resume.continue')}
            </button>
          )}
          {plan.command && (
            <button type="button" onClick={() => void copyCommand()} className="w-full rounded px-2 py-1.5 text-left text-nav text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)]" role="menuitem">
              {t('resume.copyCommand')}
            </button>
          )}
          {plan.supports_unsafe && ready && !active && !confirmUnsafe && (
            <button type="button" onClick={() => setConfirmUnsafe(true)} className="w-full rounded px-2 py-1.5 text-left text-nav text-[var(--warning)] hover:bg-[var(--bg-surface-hover)]" role="menuitem">
              {t('resume.continueUnsafe')}
            </button>
          )}
          {confirmUnsafe && (
            <div className="mt-1 rounded border border-[color-mix(in_srgb,var(--warning)_45%,var(--border-default))] bg-[color-mix(in_srgb,var(--warning)_10%,transparent)] p-2">
              <p className="text-meta text-[var(--warning)]">{t('resume.unsafeWarning')}</p>
              <div className="mt-2 flex justify-end gap-2">
                <button type="button" onClick={() => setConfirmUnsafe(false)} className="rounded px-2 py-1 text-meta text-[var(--text-secondary)]">{t('common.cancel')}</button>
                <button type="button" onClick={() => void launch(true)} className="rounded bg-[var(--warning)] px-2 py-1 text-meta text-black">{t('resume.confirmUnsafe')}</button>
              </div>
            </div>
          )}
        </div>,
        document.body,
      )}
    </div>
  )
}

function errorMessage(error: unknown, t: (key: string, vars?: Record<string, string | number>) => string): string {
  if (error instanceof APIError) {
    const localized = t(`error.${error.code}`)
    return localized === `error.${error.code}` ? t('resume.failed') : localized
  }
  return t('resume.failed')
}
