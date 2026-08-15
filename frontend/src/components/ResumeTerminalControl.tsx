import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { APIError, fetchResumePlan, fetchSessionTerminal, focusSessionTerminal, resumeSession } from '../api'
import type { ResumePlan, SessionDetail, SessionTerminalStatus } from '../types'
import { useI18n } from '../i18n'
import {
  isSessionWriting,
  presentResumeControl,
  presentResumeMenu,
  resumeBindingLabelKey,
  type ResumeMenuActionKind,
} from '../resumePresentation'
import { isSessionLive } from '../sidebarRows'

interface Props {
  session: SessionDetail
}

function terminalLabel(status: SessionTerminalStatus, t: (key: string, vars?: Record<string, string | number>) => string): string {
  if (!status.terminal_name) return ''
  return status.tab_id ? t('resume.tabLabel', { terminal: status.terminal_name, tab: status.tab_id }) : status.terminal_name
}

const actionTestId: Record<ResumeMenuActionKind, string> = {
  focus: 'resume-menu-focus',
  continue: 'resume-menu-continue',
  copy: 'resume-menu-copy',
  unsafe: 'resume-menu-unsafe',
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
  const [now, setNow] = useState(() => Date.now())
  const rootRef = useRef<HTMLDivElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const menuButtonRef = useRef<HTMLButtonElement>(null)

  const positionMenu = useCallback(() => {
    const rect = rootRef.current?.getBoundingClientRect()
    if (rect) setMenuPosition({ left: rect.left, top: rect.bottom + 4 })
  }, [])

  const closeMenu = useCallback((restoreFocus: boolean) => {
    setMenuOpen(false)
    setConfirmUnsafe(false)
    if (restoreFocus) window.requestAnimationFrame(() => menuButtonRef.current?.focus())
  }, [])

  const refresh = useCallback(async () => {
    const next = await fetchResumePlan(session.id, session.agent_type)
    setPlan(next)
    setTerminal(next.terminal)
    return next
  }, [session.agent_type, session.id])

  const openMenu = useCallback(() => {
    positionMenu()
    setMenuOpen(true)
    void refresh().catch(err => setError(errorMessage(err, t)))
  }, [positionMenu, refresh, t])

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
  }, [session.agent_type, session.id, session.is_live, t])

  useEffect(() => {
    const shouldTick = () => isSessionWriting(session.updated_at, Date.now()) || session.is_live
    if (!shouldTick()) return
    setNow(Date.now())
    const timer = window.setInterval(() => {
      const current = Date.now()
      setNow(current)
      if (!isSessionWriting(session.updated_at, current) && !session.is_live) {
        window.clearInterval(timer)
      }
    }, 1000)
    return () => window.clearInterval(timer)
  }, [session.is_live, session.updated_at])

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
    const closeOutside = (event: MouseEvent) => {
      const target = event.target as Node
      if (!rootRef.current?.contains(target) && !menuRef.current?.contains(target)) {
        closeMenu(true)
      }
    }
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      event.preventDefault()
      event.stopPropagation()
      closeMenu(true)
    }
    const reposition = () => positionMenu()
    document.addEventListener('mousedown', closeOutside)
    document.addEventListener('keydown', closeOnEscape)
    window.addEventListener('resize', reposition)
    window.addEventListener('scroll', reposition, true)
    return () => {
      document.removeEventListener('mousedown', closeOutside)
      document.removeEventListener('keydown', closeOnEscape)
      window.removeEventListener('resize', reposition)
      window.removeEventListener('scroll', reposition, true)
    }
  }, [closeMenu, menuOpen, positionMenu])

  const presentation = presentResumeControl(plan, terminal, busy, {
    sessionLive: isSessionLive(session, now),
    emitting: isSessionWriting(session.updated_at, now),
  })
  const { state, active, canFocus, canLaunch, emitting } = presentation
  const menuActions = presentResumeMenu(plan, presentation)
  const primaryLabel = presentation.preferTerminalLabel && terminal
    ? terminalLabel(terminal, t) || t(presentation.primaryLabelKey, presentation.primaryLabelVars)
    : t(presentation.primaryLabelKey, presentation.primaryLabelVars)

  const launch = async (unsafe: boolean) => {
    if (!canLaunch) return
    setBusy(true)
    setError('')
    setFeedback(t('resume.starting'))
    closeMenu(false)
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
      closeMenu(false)
    } catch (err) {
      setError(errorMessage(err, t))
    }
  }

  const runAction = (kind: ResumeMenuActionKind) => {
    if (kind === 'focus') void focus()
    else if (kind === 'continue') void launch(false)
    else if (kind === 'copy') void copyCommand()
    else setConfirmUnsafe(true)
  }

  const actionLabel = (kind: ResumeMenuActionKind): ReactNode => {
    if (kind === 'focus') return t('resume.returnTerminal')
    if (kind === 'continue') return t('resume.continue')
    if (kind === 'copy') return t('resume.copyCommand')
    return (
      <>
        {t('resume.continueUnsafe')}
        <span className="ml-1.5 text-meta text-[var(--warning)]">{t('sidebar.unsafe')}</span>
      </>
    )
  }

  const primary = () => {
    if (canFocus) void focus()
    else if (canLaunch) void launch(false)
    else openMenu()
  }

  const statusDot = state === 'launching' || (emitting && !active)
    ? 'animate-pulse bg-[var(--accent-blue)]'
    : active
      ? 'bg-[var(--accent-green)]'
      : canLaunch
        ? 'bg-[var(--text-muted)]'
        : 'bg-[var(--warning)]'

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
        <span className={`inline-block h-1.5 w-1.5 shrink-0 rounded-full ${statusDot}`} />
        <span className="truncate">{primaryLabel}</span>
      </button>
      <button
        ref={menuButtonRef}
        type="button"
        onClick={() => {
          if (menuOpen) closeMenu(false)
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
          className="fixed z-[10000] w-80 rounded-lg border border-[var(--border-default)] bg-[var(--bg-surface)] py-1.5 shadow-xl"
          style={menuPosition}
          role="menu"
          data-testid="resume-menu"
        >
          <div className="px-2.5 pb-2 pt-1 select-none" data-testid="resume-menu-status">
            <div className="text-meta text-[var(--text-muted)]">{t('resume.sectionStatus')}</div>
            <div className="mt-1 text-nav text-[var(--text-secondary)]" data-testid="resume-menu-status-title">
              {terminal?.terminal_name ? terminalLabel(terminal, t) : t('resume.terminalUnknown')}
            </div>
            <div className="mt-0.5 truncate text-meta text-[var(--text-muted)]" title={plan.cwd}>{plan.cwd || t('resume.noWorkspace')}</div>
            <div className="mt-1 text-meta text-[var(--text-muted)]">{t(resumeBindingLabelKey(terminal))}</div>
          </div>
          <div className="mx-2 border-t border-[var(--border-muted)]" />
          {(error || feedback) && (
            <div className={`mx-2 mt-1.5 rounded px-2 py-1 text-meta ${error ? 'bg-[color-mix(in_srgb,var(--error)_12%,transparent)] text-[var(--error)]' : 'bg-[color-mix(in_srgb,var(--accent-green)_12%,transparent)] text-[var(--accent-green)]'}`} role="status">
              {error || feedback}
            </div>
          )}
          {!canLaunch && presentation.continueBlockedReasonKey && (
            <div
              className="mx-2 mt-1.5 rounded bg-[var(--bg-inset)] px-2 py-1 text-meta text-[var(--text-muted)]"
              role="note"
              data-testid="resume-menu-blocked"
            >
              {t(presentation.continueBlockedReasonKey)}
            </div>
          )}
          <div className="px-2.5 pb-0.5 pt-1.5 text-meta text-[var(--text-muted)] select-none" data-testid="resume-menu-actions-label">
            {t('resume.sectionActions')}
          </div>
          {menuActions.map(action => {
            if (action.kind === 'unsafe' && confirmUnsafe) return null
            return (
              <button
                key={action.kind}
                type="button"
                role="menuitem"
                disabled={!action.enabled}
                onClick={() => { if (action.enabled) runAction(action.kind) }}
                data-testid={actionTestId[action.kind]}
                aria-disabled={!action.enabled}
                className={`mx-1 w-[calc(100%-0.5rem)] rounded px-2 py-1.5 text-left text-nav ${
                  action.enabled
                    ? 'text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)]'
                    : 'cursor-not-allowed text-[var(--text-muted)]'
                }`}
              >
                {actionLabel(action.kind)}
              </button>
            )
          })}
          {confirmUnsafe && (
            <div className="mx-2 mt-1 rounded border border-[color-mix(in_srgb,var(--warning)_45%,var(--border-default))] bg-[color-mix(in_srgb,var(--warning)_10%,transparent)] p-2">
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
