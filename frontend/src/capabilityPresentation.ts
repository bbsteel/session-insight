/**
 * Pure Agent capability presentation helpers.
 *
 * Capability *values* always come from the API. This module only maps stable
 * capability IDs / states / reason codes to presentation metadata. It must not
 * contain Agent-type → capability value tables.
 */

import type {
  ActionAvailability,
  CapabilityDeclaration,
  CapabilityID,
  SessionActionStatus,
  SessionCapabilities,
  SessionCapabilityStatus,
  SessionLivenessStatus,
} from './types'

/** Backend baseline order (ten v0.4.0 capabilities). */
export const BASELINE_CAPABILITY_IDS: readonly CapabilityID[] = [
  'discovery',
  'replay',
  'realtime',
  'tokens',
  'tool_results',
  'diff',
  'subtasks',
  'resume',
  'delete',
  'terminate',
] as const

const KNOWN_STATES = new Set<string>([
  'exact',
  'estimated',
  'missing',
  'not_applicable',
  'unsupported',
])

/** Fixed UI symbols for the five capability states. */
export function capabilityStateSymbol(state: string | undefined): string {
  switch (state) {
    case 'exact':
      return '✓'
    case 'estimated':
      return '≈'
    case 'missing':
      return '!'
    case 'not_applicable':
      return '—' // em dash, not a circle
    case 'unsupported':
      return '×'
    default:
      return '?'
  }
}

/** i18n key for the state label (Exact / Estimated / …). */
export function capabilityStateLabelKey(state: string | undefined): string {
  if (state && KNOWN_STATES.has(state)) {
    return `capability.state.${state}`
  }
  return 'capability.state.unknown'
}

/** Tone class hint for styling (neutral for exact / not_applicable). */
export type CapabilityTone = 'neutral' | 'info' | 'warning' | 'muted' | 'danger'

export function capabilityStateTone(state: string | undefined): CapabilityTone {
  switch (state) {
    case 'exact':
      return 'neutral'
    case 'estimated':
      return 'info'
    case 'missing':
      return 'warning'
    case 'not_applicable':
      return 'muted'
    case 'unsupported':
      return 'danger'
    default:
      return 'muted'
  }
}

/**
 * Summary severity rank for ordering issues (higher = more severe).
 * not_applicable does not contribute to warnings (returns 0).
 */
export function capabilityStateSeverity(state: string | undefined): number {
  switch (state) {
    case 'missing':
      return 3
    case 'unsupported':
      return 2
    case 'estimated':
      return 1
    case 'exact':
    case 'not_applicable':
    default:
      return 0
  }
}

/** Whether this state counts as a “problem” in compact summaries. */
export function capabilityStateIsWarning(state: string | undefined): boolean {
  return capabilityStateSeverity(state) > 0
}

export function capabilityLabelKey(id: string): string {
  if ((BASELINE_CAPABILITY_IDS as readonly string[]).includes(id)) {
    return `capability.id.${id}`
  }
  return 'capability.id.unknown'
}

export function capabilityDescriptionKey(id: string): string {
  if ((BASELINE_CAPABILITY_IDS as readonly string[]).includes(id)) {
    return `capability.desc.${id}`
  }
  return 'capability.desc.unknown'
}

/** Vars for t(...) when resolving capability label/description (retains machine id for unknowns). */
export function capabilityIdI18nVars(id: string): { id: string } | undefined {
  if ((BASELINE_CAPABILITY_IDS as readonly string[]).includes(id)) return undefined
  return { id }
}

/**
 * Header token display from session bill / turns, gated by resolved tokens capability.
 * When tokens are missing/unsupported/not_applicable, never present a trustworthy numeric count.
 */
export function sessionTokenHeaderDisplay(
  tokensState: string | undefined,
  totalTokens: number,
): { kind: 'value' | 'missing' | 'unsupported' | 'not_applicable' | 'unknown'; total: number } {
  switch (tokensState) {
    case 'missing':
      return { kind: 'missing', total: totalTokens }
    case 'unsupported':
      return { kind: 'unsupported', total: totalTokens }
    case 'not_applicable':
      return { kind: 'not_applicable', total: totalTokens }
    case 'exact':
    case 'estimated':
    case undefined:
      // undefined: older payload without agent_capabilities — keep legacy numeric display
      return { kind: 'value', total: totalTokens }
    default:
      return { kind: 'unknown', total: totalTokens }
  }
}

/** Map stable reason codes to i18n keys; unknown → fallback key. */
export function reasonCodeLabelKey(code: string | undefined | null): string | null {
  if (!code || !code.trim()) return null
  const known = new Set([
    'source_not_recorded',
    'session_not_finalized',
    'resume_id_missing',
    'timestamp_heuristic',
    'name_heuristic',
    'revision_unavailable',
    'session_running',
    'session_not_live',
    'runtime_check_required',
    'exact_pid_unavailable',
    'adapter_not_implemented',
    'concept_absent',
    'platform_not_supported',
    // Frozen collaboration contract reason codes (internal/collaboration).
    'fifo_join_heuristic',
    'aggregate_window',
    'completion_not_recorded',
    'stale_graph_retained',
    'timestamp_contradiction',
  ])
  if (known.has(code)) return `capability.reason.${code}`
  return 'capability.reason.unknown'
}

export function actionAvailabilityLabelKey(availability: string | undefined): string {
  switch (availability) {
    case 'available':
      return 'capability.action.available'
    case 'unavailable':
      return 'capability.action.unavailable'
    case 'runtime_check_required':
      return 'capability.action.runtime_check_required'
    default:
      return 'capability.action.unknown'
  }
}

/** runtime_check_required is advisory, not confirmed available. */
export function actionIsConfirmedAvailable(availability: string | undefined): boolean {
  return availability === 'available'
}

export interface CapabilitySummary {
  exact: number
  estimated: number
  missing: number
  not_applicable: number
  unsupported: number
  unknown: number
  /** Counts that should surface as warnings (excludes not_applicable and exact). */
  warningCount: number
  hasWarning: boolean
  /** Highest severity among warning states (0 if calm). */
  maxSeverity: number
}

export function summarizeCapabilityStates(
  entries: Iterable<{ state?: string } | CapabilityDeclaration | SessionCapabilityStatus | undefined | null>,
): CapabilitySummary {
  const out: CapabilitySummary = {
    exact: 0,
    estimated: 0,
    missing: 0,
    not_applicable: 0,
    unsupported: 0,
    unknown: 0,
    warningCount: 0,
    hasWarning: false,
    maxSeverity: 0,
  }
  for (const e of entries) {
    if (!e) continue
    const state = e.state
    switch (state) {
      case 'exact':
        out.exact++
        break
      case 'estimated':
        out.estimated++
        break
      case 'missing':
        out.missing++
        break
      case 'not_applicable':
        out.not_applicable++
        break
      case 'unsupported':
        out.unsupported++
        break
      default:
        out.unknown++
        break
    }
    const sev = capabilityStateSeverity(state)
    if (sev > 0) {
      out.warningCount++
      if (sev > out.maxSeverity) out.maxSeverity = sev
    }
  }
  out.hasWarning = out.warningCount > 0
  return out
}

/** Ordered rows from a static Agent capabilities map. null decl = absent from API. */
export function orderedStaticCapabilities(
  caps: Partial<Record<string, CapabilityDeclaration>> | undefined | null,
): { id: string; decl: CapabilityDeclaration | null }[] {
  const rows: { id: string; decl: CapabilityDeclaration | null }[] = []
  const seen = new Set<string>()
  for (const id of BASELINE_CAPABILITY_IDS) {
    const decl = caps?.[id] ?? null
    rows.push({ id, decl })
    seen.add(id)
  }
  if (caps) {
    for (const id of Object.keys(caps)) {
      if (seen.has(id)) continue
      const decl = caps[id]
      if (decl) rows.push({ id, decl })
    }
  }
  return rows
}

/** Ordered rows from session-resolved status. null status = absent from API. */
export function orderedSessionStatuses(
  status: SessionCapabilities['status'] | undefined | null,
): { id: string; status: SessionCapabilityStatus | null }[] {
  const rows: { id: string; status: SessionCapabilityStatus | null }[] = []
  const seen = new Set<string>()
  for (const id of BASELINE_CAPABILITY_IDS) {
    rows.push({ id, status: status?.[id] ?? null })
    seen.add(id)
  }
  if (status) {
    for (const id of Object.keys(status)) {
      if (seen.has(id)) continue
      const st = status[id]
      if (st) rows.push({ id, status: st })
    }
  }
  return rows
}

export function summarizeStaticAgent(
  caps: Partial<Record<string, CapabilityDeclaration>> | undefined | null,
): CapabilitySummary {
  return summarizeCapabilityStates(
    orderedStaticCapabilities(caps).map(r => r.decl).filter(Boolean) as CapabilityDeclaration[],
  )
}

export function summarizeSessionCaps(
  sc: SessionCapabilities | undefined | null,
): CapabilitySummary {
  if (!sc?.status) {
    return summarizeCapabilityStates([])
  }
  return summarizeCapabilityStates(
    orderedSessionStatuses(sc.status).map(r => r.status).filter(Boolean) as SessionCapabilityStatus[],
  )
}

/**
 * Compact header hint for the session Agent control.
 * Prefer missing count, then estimated; never counts not_applicable.
 */
export function sessionCapabilityHeaderHint(
  sc: SessionCapabilities | undefined | null,
): { kind: 'missing' | 'estimated' | 'unsupported' | 'calm'; count: number } {
  const s = summarizeSessionCaps(sc)
  if (s.missing > 0) return { kind: 'missing', count: s.missing }
  if (s.unsupported > 0) return { kind: 'unsupported', count: s.unsupported }
  if (s.estimated > 0) return { kind: 'estimated', count: s.estimated }
  return { kind: 'calm', count: 0 }
}

export function livenessPresentation(live: SessionLivenessStatus | undefined | null): {
  isLive: boolean
  state: string
  qualityKey: string
  reasonKey: string | null
} {
  if (!live) {
    return {
      isLive: false,
      state: 'unknown',
      qualityKey: 'capability.liveness.unavailable',
      reasonKey: null,
    }
  }
  return {
    isLive: live.is_live,
    state: live.state,
    qualityKey: live.state === 'exact'
      ? 'capability.liveness.qualityExact'
      : live.state === 'estimated'
        ? 'capability.liveness.qualityEstimated'
        : 'capability.liveness.qualityUnknown',
    reasonKey: reasonCodeLabelKey(live.reason_code),
  }
}

export function actionRows(
  actions: SessionCapabilities['actions'] | undefined | null,
): { id: CapabilityID; action: SessionActionStatus }[] {
  const ids: CapabilityID[] = ['resume', 'delete', 'terminate']
  return ids.map(id => ({
    id,
    action: actions?.[id] ?? { availability: 'unavailable' as ActionAvailability, reason_code: 'source_not_recorded' },
  }))
}

/** Guard: presentation logic must never key capability *values* by agent type. */
export function assertNoAgentCapabilityMatrixInModuleSource(source: string): boolean {
  // Used by unit tests: reject patterns that hard-code Agent→capability values.
  const forbidden = [
    /claude\s*:\s*\{[^}]*tokens/i,
    /codex\s*supports/i,
    /agentType\s*===\s*['"]claude['"]\s*\?[^;]*capability/i,
    /switch\s*\(\s*agentType\s*\)[\s\S]*case\s*['"]claude['"][\s\S]*tokens/i,
  ]
  return !forbidden.some(re => re.test(source))
}
