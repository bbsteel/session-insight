import type { ResumePlan, SessionTerminalStatus, TerminalBindingState } from './types'

// Tight window for "the transcript is still being written". This is not the
// 5-minute live badge: a just-finished turn should become continuable again
// after a short pause, but a session that is actively appending messages
// must not offer Continue.
export const SESSION_WRITING_WINDOW_MS = 20_000

export function isSessionWriting(updatedAt: string | undefined, now: number): boolean {
  if (!updatedAt) return false
  const timestamp = Date.parse(updatedAt)
  return Number.isFinite(timestamp) && now - timestamp < SESSION_WRITING_WINDOW_MS
}

export interface ResumeControlFlags {
  sessionLive?: boolean
  emitting?: boolean
}

export interface ResumeControlPresentation {
  state: TerminalBindingState
  active: boolean
  canFocus: boolean
  ready: boolean
  canLaunch: boolean
  running: boolean
  emitting: boolean
  continueBlockedReasonKey?: string
  primaryLabelKey: string
  primaryLabelVars?: Record<string, string | number>
  preferTerminalLabel: boolean
}

function deriveBindingState(
  terminal: SessionTerminalStatus | null,
  running: boolean,
): TerminalBindingState {
  const raw = terminal?.state
  if (raw === 'launching' || raw === 'active' || raw === 'active_unknown') return raw
  if (running) return 'active_unknown'
  return raw ?? 'none'
}

function continueBlockedReasonKey(
  canLaunch: boolean,
  running: boolean,
  emitting: boolean,
  plan: ResumePlan | null,
): string | undefined {
  if (canLaunch) return undefined
  if (running) return 'resume.continueBlockedRunning'
  if (emitting) return 'resume.continueBlockedWriting'
  if (plan?.status === 'cwd_unavailable') return 'resume.workspaceMissing'
  return 'resume.unavailable'
}

export function presentResumeControl(
  plan: ResumePlan | null,
  terminal: SessionTerminalStatus | null,
  busy: boolean,
  flags: ResumeControlFlags = {},
): ResumeControlPresentation {
  const running = plan?.status === 'session_running'
    || plan?.liveness?.is_live === true
    || flags.sessionLive === true
  const emitting = flags.emitting === true
  const state = deriveBindingState(terminal, running)
  const active = state === 'active' || state === 'active_unknown'
  const canFocus = state === 'active' && terminal?.focusable === true
  const ready = plan?.status === 'ready'
  const canLaunch = ready && !running && !emitting
  const blockedReason = continueBlockedReasonKey(canLaunch, running, emitting, plan)
  const base = {
    state, active, canFocus, ready, canLaunch, running, emitting,
    continueBlockedReasonKey: blockedReason,
    preferTerminalLabel: false as boolean,
  }

  if (busy) return { ...base, primaryLabelKey: 'resume.working' }
  if (state === 'launching') return { ...base, primaryLabelKey: 'resume.starting' }
  if (canFocus) return { ...base, primaryLabelKey: 'resume.returnTerminal', preferTerminalLabel: true }
  if (active && terminal?.terminal_name) {
    return {
      ...base,
      primaryLabelKey: 'resume.runningIn',
      primaryLabelVars: { terminal: terminal.terminal_name },
    }
  }
  if (active) return { ...base, primaryLabelKey: 'resume.runningUnknown' }
  if (emitting) return { ...base, primaryLabelKey: 'resume.writing' }
  if (canLaunch) return { ...base, primaryLabelKey: 'resume.continue' }
  if (plan?.status === 'cwd_unavailable') {
    return { ...base, primaryLabelKey: 'resume.workspaceMissing' }
  }
  return { ...base, primaryLabelKey: 'resume.unavailable' }
}

export type ResumeMenuActionKind = 'focus' | 'continue' | 'copy' | 'unsafe'

export interface ResumeMenuAction {
  kind: ResumeMenuActionKind
  enabled: boolean
  disabledReasonKey?: string
}

export function resumeBindingLabelKey(terminal: SessionTerminalStatus | null): string {
  if (terminal?.confidence === 'exact') return 'resume.bindingExact'
  if (terminal?.confidence === 'instance') return 'resume.bindingInstance'
  return 'resume.bindingUnknown'
}

export function presentResumeMenu(
  plan: ResumePlan | null,
  presentation: ResumeControlPresentation,
): ResumeMenuAction[] {
  const actions: ResumeMenuAction[] = []
  if (presentation.canFocus) {
    actions.push({ kind: 'focus', enabled: true })
  }
  actions.push({
    kind: 'continue',
    enabled: presentation.canLaunch,
    disabledReasonKey: presentation.canLaunch ? undefined : presentation.continueBlockedReasonKey,
  })
  if (plan?.command) {
    actions.push({ kind: 'copy', enabled: true })
  }
  if (plan?.supports_unsafe) {
    actions.push({
      kind: 'unsafe',
      enabled: presentation.canLaunch,
      disabledReasonKey: presentation.canLaunch ? undefined : presentation.continueBlockedReasonKey,
    })
  }
  return actions
}

export interface SidebarResumePresentation {
  command: string | null
  isLive: boolean
  supportsUnsafe: boolean
}

export function presentSidebarResume(plan: ResumePlan | null, fallbackLive: boolean): SidebarResumePresentation {
  return {
    command: plan?.command ?? null,
    isLive: plan?.liveness.is_live ?? fallbackLive,
    supportsUnsafe: plan?.supports_unsafe ?? false,
  }
}

// The preloaded menu plan contains the standard command. Unsafe mode has a
// different argv contract and must still request its own plan.
export function preloadedResumePlanForCopy(plan: ResumePlan | null, unsafe: boolean): ResumePlan | null {
  return unsafe ? null : plan
}
