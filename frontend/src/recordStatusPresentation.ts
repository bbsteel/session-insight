import type {
  RecordCompletenessState,
  RecordStatus,
  SessionDetail,
  SessionProvenance,
  SessionSourceFile,
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
        // Quiet green chip next to “记录状态”, not a separate “总体状态” row.
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

/** Translate a record-status presentation label; supplies {code} for unknown states. */
export function recordStatusLabel(
  pres: RecordStatusPresentation,
  t: (key: string, vars?: Record<string, string | number>) => string,
): string {
  if (pres.state === 'degraded' && pres.warningCount > 0) {
    return t('record.header.degradedCount', { n: pres.warningCount })
  }
  if (pres.labelKey === 'record.status.unknown') {
    return t('record.status.unknown', { code: String(pres.state) })
  }
  return t(pres.labelKey)
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

const KNOWN_SOURCE_ROLES = [
  'primary_transcript',
  'metadata',
  'events',
  'updates',
  'tool_results',
  'collaboration',
  'recovery',
  'snapshot',
  'edit_cache',
  'other',
] as const

export function sourceRoleLabelKey(role: string): string {
  if ((KNOWN_SOURCE_ROLES as readonly string[]).includes(role)) return `record.sourceRole.${role}`
  return 'record.sourceRole.unknown'
}

/** Hover help for a source role (what this category means). */
export function sourceRoleHelpKey(role: string): string {
  if ((KNOWN_SOURCE_ROLES as readonly string[]).includes(role)) return `record.sourceRoleHelp.${role}`
  return 'record.sourceRoleHelp.unknown'
}

/**
 * Roles that the record panel collapses by default when more than one file
 * shares the role. Driven by role only — not path heuristics or agent_type.
 */
/** Multi-file bulk roles: collapsed in the panel when n ≥ 2. */
export const COLLAPSIBLE_SOURCE_ROLES = ['edit_cache', 'snapshot', 'metadata'] as const

export function isCollapsibleSourceRole(role: string | undefined | null): boolean {
  return !!role && (COLLAPSIBLE_SOURCE_ROLES as readonly string[]).includes(role)
}

/** @deprecated use isCollapsibleSourceRole — kept for older test imports */
export function isEditCacheRole(role: string | undefined | null): boolean {
  return role === 'edit_cache'
}

export type SourceListPartition = {
  /** Always-visible rows (single-file roles and non-collapsible roles). */
  main: SessionSourceFile[]
  /** Collapsed groups keyed by role (edit_cache, snapshot, …). */
  groups: { role: string; sources: SessionSourceFile[] }[]
}

/**
 * Partition provenance sources for the record panel: keep unique/important
 * rows expanded; fold multi-file bulk roles (edit_cache, snapshot) when n≥2.
 * A single snapshot/edit_cache file stays expanded with a normal row.
 */
export function partitionSourceFiles(sources: SessionSourceFile[] | undefined | null): SourceListPartition {
  const main: SessionSourceFile[] = []
  const groupMap = new Map<string, SessionSourceFile[]>()
  for (const s of sources || []) {
    if (isCollapsibleSourceRole(s.role)) {
      const list = groupMap.get(s.role) || []
      list.push(s)
      groupMap.set(s.role, list)
    } else {
      main.push(s)
    }
  }
  // Stable group order: edit_cache then snapshot, then any future collapsible.
  const groups: { role: string; sources: SessionSourceFile[] }[] = []
  for (const role of COLLAPSIBLE_SOURCE_ROLES) {
    const list = groupMap.get(role)
    if (!list || list.length === 0) continue
    if (list.length === 1) {
      main.push(list[0])
      continue
    }
    groups.push({ role, sources: list })
  }
  for (const [role, list] of groupMap) {
    if ((COLLAPSIBLE_SOURCE_ROLES as readonly string[]).includes(role)) continue
    if (list.length === 1) main.push(list[0])
    else if (list.length > 1) groups.push({ role, sources: list })
  }
  return { main, groups }
}

/** Basename of a source path (handles / and \\). */
export function sourceFileBaseName(path: string | undefined | null): string {
  if (!path) return ''
  const parts = path.split(/[/\\]/)
  return parts[parts.length - 1] || path
}

/**
 * Known source filenames → i18n help key. Shared by basename so each agent
 * that uses the same name gets the same explanation (not agent_type switches).
 * Returns null when we have no specific file blurb (role help still applies).
 */
const SOURCE_FILE_HELP: Record<string, string> = {
  // Grok
  'summary.json': 'record.sourceFileHelp.summary_json',
  'system_prompt.txt': 'record.sourceFileHelp.system_prompt_txt',
  'prompt_context.json': 'record.sourceFileHelp.prompt_context_json',
  'signals.json': 'record.sourceFileHelp.signals_json',
  'resources_state.json': 'record.sourceFileHelp.resources_state_json',
  'announcement_state.json': 'record.sourceFileHelp.announcement_state_json',
  'updates.jsonl': 'record.sourceFileHelp.updates_jsonl',
  'chat_history.jsonl': 'record.sourceFileHelp.chat_history_jsonl',
  'events.jsonl': 'record.sourceFileHelp.events_jsonl',
  'rewind_points.jsonl': 'record.sourceFileHelp.rewind_points_jsonl',
  'hunk_records.jsonl': 'record.sourceFileHelp.hunk_records_jsonl',
  'meta.json': 'record.sourceFileHelp.meta_json',
  // Chrys
  'session.json': 'record.sourceFileHelp.session_json',
  'session.recovery.json': 'record.sourceFileHelp.session_recovery_json',
  'session.json.bak': 'record.sourceFileHelp.session_json_bak',
  // Copilot / OpenCode / Claude common
  'workspace.yaml': 'record.sourceFileHelp.workspace_yaml',
  'opencode.db': 'record.sourceFileHelp.opencode_db',
}

const SOURCE_FILE_LABEL: Record<string, string> = {
  'summary.json': 'record.sourceFileLabel.summary_json',
  'system_prompt.txt': 'record.sourceFileLabel.system_prompt_txt',
  'prompt_context.json': 'record.sourceFileLabel.prompt_context_json',
  'signals.json': 'record.sourceFileLabel.signals_json',
  'resources_state.json': 'record.sourceFileLabel.resources_state_json',
  'announcement_state.json': 'record.sourceFileLabel.announcement_state_json',
  'updates.jsonl': 'record.sourceFileLabel.updates_jsonl',
  'chat_history.jsonl': 'record.sourceFileLabel.chat_history_jsonl',
  'events.jsonl': 'record.sourceFileLabel.events_jsonl',
  'rewind_points.jsonl': 'record.sourceFileLabel.rewind_points_jsonl',
  'hunk_records.jsonl': 'record.sourceFileLabel.hunk_records_jsonl',
  'meta.json': 'record.sourceFileLabel.meta_json',
  'session.json': 'record.sourceFileLabel.session_json',
  'session.recovery.json': 'record.sourceFileLabel.session_recovery_json',
  'session.json.bak': 'record.sourceFileLabel.session_json_bak',
  'workspace.yaml': 'record.sourceFileLabel.workspace_yaml',
  'opencode.db': 'record.sourceFileLabel.opencode_db',
}

export function sourceFileHelpKey(path: string | undefined | null): string | null {
  const base = sourceFileBaseName(path).toLowerCase()
  if (!base) return null
  // turn_N.json snapshots (chrys)
  if (/^turn_\d+\.json$/i.test(base)) return 'record.sourceFileHelp.turn_snapshot_json'
  // chrys / hash mutation blobs under mutations/
  if (path && /(?:^|[/\\])mutations[/\\][^/\\]+$/i.test(path) && !base.includes('.')) {
    return 'record.sourceFileHelp.mutation_blob'
  }
  return SOURCE_FILE_HELP[base] || null
}

export function sourceFileLabelKey(path: string | undefined | null): string | null {
  const base = sourceFileBaseName(path).toLowerCase()
  if (!base) return null
  if (/^turn_\d+\.json$/i.test(base)) return 'record.sourceFileLabel.turn_snapshot_json'
  if (path && /(?:^|[/\\])mutations[/\\][^/\\]+$/i.test(path) && !base.includes('.')) {
    return 'record.sourceFileLabel.mutation_blob'
  }
  return SOURCE_FILE_LABEL[base] || null
}

/**
 * Paths that are binary / sqlite operational files: copy is useful, opening
 * in a text editor is not.
 */
export function isEditorFriendlySourcePath(path: string | undefined | null): boolean {
  if (!path) return false
  const base = path.split(/[/\\]/).pop() || path
  const lower = base.toLowerCase()
  if (lower.endsWith('.db-wal') || lower.endsWith('.db-shm') || lower.endsWith('-wal') || lower.endsWith('-shm')) {
    return false
  }
  if (/\.(db|sqlite|sqlite3|bin|so|dylib|dll|exe|png|jpg|jpeg|gif|webp|ico|pdf|zip|gz|tgz|xz)$/i.test(lower)) {
    return false
  }
  // Bare "opencode.db" style already covered by .db; also name contains .db.
  if (/\.db(\.|$)/i.test(lower)) return false
  return true
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

/** Header tone CSS class tokens for the status chip next to “记录状态”. */
export function toneClass(tone: RecordTone): string {
  switch (tone) {
    case 'success':
      // Quiet green background for complete — design: soft green, not a loud CTA.
      return 'text-[var(--accent-green)] border-[var(--accent-green)]/35 bg-[var(--accent-green)]/15'
    case 'neutral':
      return 'text-[var(--text-secondary)] border-[var(--border-default)] bg-[var(--bg-surface)]'
    case 'warning':
      return 'text-[var(--warning)] border-[var(--warning)]/40 bg-[var(--warning)]/10'
    case 'danger':
      return 'text-[var(--error)] border-[var(--error)]/40 bg-[var(--error)]/10'
    case 'muted':
      return 'text-[var(--text-muted)] border-[var(--border-default)] bg-[var(--bg-surface)]'
    default:
      return 'text-[var(--text-secondary)] border-[var(--border-default)] bg-[var(--bg-surface)]'
  }
}

/** Local timezone wall clock, second precision (no fractional seconds / no raw Z). */
export function formatRecordTime(locale: string, value: string | Date | number | undefined | null): string {
  if (value == null || value === '') return ''
  const d = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(d.getTime())) return String(value)
  return new Intl.DateTimeFormat(locale, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(d)
}

/**
 * Human size: prefer KiB (binary KB) for transcript files.
 * Below 1024 bytes shows B; otherwise one decimal KB.
 */
export function formatSourceSize(bytes: number | undefined | null): string {
  if (bytes == null || !Number.isFinite(bytes) || bytes < 0) return ''
  if (bytes < 1024) return `${Math.round(bytes)} B`
  const kb = bytes / 1024
  // One decimal for readability; drop trailing .0
  const rounded = Math.round(kb * 10) / 10
  const text = Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1)
  return `${text} KB`
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
