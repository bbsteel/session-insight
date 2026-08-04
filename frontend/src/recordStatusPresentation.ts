import type {
  RecordCompletenessState,
  RecordStatus,
  SessionDetail,
  SessionProvenance,
  WarningSummary,
} from './types'

export type RecordTone = 'neutral' | 'success' | 'warning' | 'danger' | 'muted'

export interface RecordStatusPresentation {
  state: RecordCompletenessState | 'unavailable' | string
  /** i18n key for short header label */
  labelKey: string
  /** Optional warning count for degraded pill */
  warningCount: number
  tone: RecordTone
  /** Whether replay body is expected / allowed */
  replayable: boolean
  /** Dedicated empty-state key when not replayable */
  emptyStateKey: string | null
}

const KNOWN: RecordCompletenessState[] = [
  'complete',
  'degraded',
  'metadata_only',
  'source_missing',
  'parser_unsupported',
]

export function isKnownRecordState(s: string | undefined | null): s is RecordCompletenessState {
  return !!s && (KNOWN as string[]).includes(s)
}

/** Map server state (or compact status) to UI presentation. Never invents complete. */
export function presentRecordStatus(
  provenance: SessionProvenance | null | undefined,
  compact?: RecordStatus | null,
  recordAvailable?: boolean | null,
): RecordStatusPresentation {
  const stateRaw = provenance?.state ?? compact?.state
  if (!stateRaw) {
    return {
      state: 'unavailable',
      labelKey: 'record.status.unavailable',
      warningCount: 0,
      tone: 'muted',
      replayable: recordAvailable !== false,
      emptyStateKey: null,
    }
  }
  const state = isKnownRecordState(stateRaw) ? stateRaw : stateRaw
  const warningCount =
    provenance?.warning_summary?.total ??
    compact?.warning_count ??
    0

  switch (state) {
    case 'complete':
      return {
        state,
        labelKey: 'record.status.complete',
        warningCount: 0,
        tone: 'success',
        replayable: true,
        emptyStateKey: null,
      }
    case 'degraded':
      return {
        state,
        labelKey: 'record.status.degraded',
        warningCount,
        tone: 'warning',
        replayable: true,
        emptyStateKey: null,
      }
    case 'metadata_only':
      return {
        state,
        labelKey: 'record.status.metadata_only',
        warningCount,
        tone: 'warning',
        replayable: false,
        emptyStateKey: 'record.empty.metadata_only',
      }
    case 'source_missing':
      return {
        state,
        labelKey: 'record.status.source_missing',
        warningCount,
        tone: 'danger',
        replayable: false,
        emptyStateKey: 'record.empty.source_missing',
      }
    case 'parser_unsupported':
      return {
        state,
        labelKey: 'record.status.parser_unsupported',
        warningCount,
        tone: 'danger',
        replayable: false,
        emptyStateKey: 'record.empty.parser_unsupported',
      }
    default:
      return {
        state,
        labelKey: 'record.status.unknown',
        warningCount,
        tone: 'muted',
        replayable: recordAvailable !== false,
        emptyStateKey: null,
      }
  }
}

export function presentFromSession(session: SessionDetail): RecordStatusPresentation {
  return presentRecordStatus(session.provenance, undefined, session.record_available)
}

export function impactLabelKey(impact: string): string {
  const known = [
    'metadata',
    'replay',
    'navigation',
    'tokens',
    'tools',
    'diff',
    'collaboration',
    'realtime',
  ]
  if (known.includes(impact)) return `record.impact.${impact}`
  return 'record.impact.unknown'
}

export function sourceRoleLabelKey(role: string): string {
  const known = [
    'primary_transcript',
    'metadata',
    'events',
    'updates',
    'tool_results',
    'collaboration',
    'other',
  ]
  if (known.includes(role)) return `record.sourceRole.${role}`
  return 'record.sourceRole.unknown'
}

export function warningCodeLabelKey(code: string): string {
  const known = [
    'malformed_record_skipped',
    'truncated_record',
    'sidecar_missing',
    'source_unreadable',
    'source_changed_during_read',
    'unsupported_schema_revision',
    'identity_mismatch',
    'timestamp_invalid',
    'partial_tool_result',
    'partial_collaboration_graph',
    'unknown_record_ignored',
  ]
  if (known.includes(code)) return `record.warning.${code}`
  return 'record.warning.unknown'
}

export function sourceStateLabelKey(state: string): string {
  const known = ['present', 'missing', 'unreadable', 'unsupported']
  if (known.includes(state)) return `record.sourceState.${state}`
  return 'record.sourceState.unknown'
}

/** Header tone CSS class tokens (not green full success for missing/degraded). */
export function toneClass(tone: RecordTone): string {
  switch (tone) {
    case 'success':
      return 'text-[var(--text-secondary)] border-[var(--border-default)]'
    case 'warning':
      return 'text-[var(--warning)] border-[var(--warning)]/40'
    case 'danger':
      return 'text-[var(--error)] border-[var(--error)]/40'
    case 'muted':
      return 'text-[var(--text-muted)] border-[var(--border-default)]'
    default:
      return 'text-[var(--text-secondary)] border-[var(--border-default)]'
  }
}

/**
 * Capability warning counts must never be summed with parser warning counts.
 * This helper exists so unit tests can lock the invariant.
 */
export function parserWarningCount(summary: WarningSummary | null | undefined): number {
  return summary?.total ?? 0
}

export function doNotSumCapabilityAndParser(
  capabilityMissing: number,
  parserWarnings: number,
): { capabilityMissing: number; parserWarnings: number; combinedLabelForbidden: true } {
  return {
    capabilityMissing,
    parserWarnings,
    combinedLabelForbidden: true,
  }
}
