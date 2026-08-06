import type { ResumePlan, SessionTerminalStatus, TerminalBindingState } from './types'

export interface ResumeControlPresentation {
  state: TerminalBindingState
  active: boolean
  canFocus: boolean
  ready: boolean
  primaryLabelKey: string
  primaryLabelVars?: Record<string, string | number>
  preferTerminalLabel: boolean
}

export function presentResumeControl(
  plan: ResumePlan | null,
  terminal: SessionTerminalStatus | null,
  busy: boolean,
): ResumeControlPresentation {
  const state = terminal?.state ?? (plan?.status === 'session_running' ? 'active_unknown' : 'none')
  const active = state === 'active' || state === 'active_unknown'
  const canFocus = state === 'active' && terminal?.focusable === true
  const ready = plan?.status === 'ready'

  if (busy) return { state, active, canFocus, ready, primaryLabelKey: 'resume.working', preferTerminalLabel: false }
  if (state === 'launching') return { state, active, canFocus, ready, primaryLabelKey: 'resume.starting', preferTerminalLabel: false }
  if (canFocus) return { state, active, canFocus, ready, primaryLabelKey: 'resume.returnTerminal', preferTerminalLabel: true }
  if (active && terminal?.terminal_name) {
    return {
      state, active, canFocus, ready,
      primaryLabelKey: 'resume.runningIn',
      primaryLabelVars: { terminal: terminal.terminal_name },
      preferTerminalLabel: false,
    }
  }
  if (active) return { state, active, canFocus, ready, primaryLabelKey: 'resume.runningUnknown', preferTerminalLabel: false }
  if (ready) return { state, active, canFocus, ready, primaryLabelKey: 'resume.continue', preferTerminalLabel: false }
  if (plan?.status === 'cwd_unavailable') {
    return { state, active, canFocus, ready, primaryLabelKey: 'resume.workspaceMissing', preferTerminalLabel: false }
  }
  return { state, active, canFocus, ready, primaryLabelKey: 'resume.unavailable', preferTerminalLabel: false }
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
