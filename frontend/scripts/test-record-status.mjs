import assert from 'node:assert/strict'
import {
  doNotSumCapabilityAndParser,
  formatRecordTime,
  formatSourceSize,
  isKnownRecordState,
  isCollapsibleSourceRole,
  isEditorFriendlySourcePath,
  isEditCacheRole,
  parserWarningCount,
  partitionSourceFiles,
  sourceFileHelpKey,
  sourceFileLabelKey,
  sourceRoleHelpKey,
  presentFromSession,
  presentRecordStatus,
  recordStatusLabel,
  impactLabelKey,
  warningCodeLabelKey,
  sourceRoleLabelKey,
} from '../.runtime/record-status-test/recordStatusPresentation.js'

// --- state mapping ---
const complete = presentRecordStatus({
  state: 'complete',
  captured_at: '2026-08-04T00:00:00Z',
  adapter_revision: 1,
  sources: [],
  warning_summary: { total: 0 },
  warnings: [],
})
assert.equal(complete.state, 'complete')
// Soft green chip next to “记录状态”, not a separate “总体状态” row.
assert.equal(complete.tone, 'success')
assert.equal(complete.replayable, true)
assert.equal(complete.emptyStateKey, null)

const degraded = presentRecordStatus({
  state: 'degraded',
  captured_at: '2026-08-04T00:00:00Z',
  adapter_revision: 1,
  sources: [],
  warning_summary: { total: 3 },
  warnings: [],
})
assert.equal(degraded.state, 'degraded')
assert.equal(degraded.warningCount, 3)
assert.equal(degraded.tone, 'warning')
assert.equal(degraded.replayable, true)

for (const st of ['metadata_only', 'source_missing', 'parser_unsupported']) {
  const p = presentRecordStatus({
    state: st,
    captured_at: '2026-08-04T00:00:00Z',
    adapter_revision: 1,
    sources: [],
    warning_summary: { total: 0 },
    warnings: [],
  })
  assert.equal(p.replayable, false, st)
  assert.ok(p.emptyStateKey, st)
  assert.notEqual(p.tone, 'success', `${st} must not look like full success`)
}

// unavailable when no provenance
const unavail = presentRecordStatus(null)
assert.equal(unavail.state, 'unavailable')
assert.equal(unavail.tone, 'muted')

// unknown code fallback
const unknown = presentRecordStatus({
  state: 'weird_future_state',
  captured_at: '2026-08-04T00:00:00Z',
  adapter_revision: 1,
  sources: [],
  warning_summary: { total: 0 },
  warnings: [],
})
assert.equal(unknown.labelKey, 'record.status.unknown')
// Shared label helper must pass {code} so the placeholder is not left literal.
const fakeT = (key, vars) => {
  if (key === 'record.status.unknown') return `unknown:${vars?.code ?? ''}`
  if (key === 'record.header.degradedCount') return `degraded:${vars?.n ?? 0}`
  return key
}
assert.equal(recordStatusLabel(unknown, fakeT), 'unknown:weird_future_state')
assert.equal(recordStatusLabel(degraded, fakeT), 'degraded:3')
assert.equal(recordStatusLabel(complete, fakeT), 'record.status.complete')

// compact list status
const compact = presentRecordStatus(null, {
  state: 'degraded',
  warning_count: 2,
  captured_at: '2026-08-04T00:00:00Z',
})
assert.equal(compact.warningCount, 2)

// session helper
const sess = presentFromSession({
  id: 'x',
  agent_type: 'claude',
  name: 'n',
  repository: '',
  branch: '',
  cwd: '',
  turn_count: 0,
  message_count: 0,
  is_live: false,
  bookmarked: false,
  created_at: '',
  updated_at: '',
  model_name: '',
  turns: [],
  provenance: {
    state: 'source_missing',
    captured_at: '2026-08-04T00:00:00Z',
    adapter_revision: 1,
    sources: [],
    warning_summary: { total: 0 },
    warnings: [],
  },
  record_available: false,
})
assert.equal(sess.state, 'source_missing')
assert.equal(sess.replayable, false)

// label key maps
assert.equal(impactLabelKey('replay'), 'record.impact.replay')
assert.equal(impactLabelKey('nope'), 'record.impact.unknown')
assert.equal(warningCodeLabelKey('sidecar_missing'), 'record.warning.sidecar_missing')
assert.equal(warningCodeLabelKey('weird'), 'record.warning.unknown')
assert.equal(sourceRoleLabelKey('primary_transcript'), 'record.sourceRole.primary_transcript')

// capability vs parser counts must not be summed into one number
const split = doNotSumCapabilityAndParser(2, 5)
assert.equal(split.capabilityMissing, 2)
assert.equal(split.parserWarnings, 5)
assert.equal(split.combinedLabelForbidden, true)
assert.equal(parserWarningCount({ total: 5 }), 5)
assert.equal(isKnownRecordState('complete'), true)
assert.equal(isKnownRecordState('nope'), false)

// Local time to the second (no fractional / raw Z); size in KB.
const local = formatRecordTime('en', '2026-08-04T06:32:17.754538719Z')
assert.ok(local.length > 0, 'formatRecordTime returns a value')
assert.ok(!local.includes('T') || !local.endsWith('Z'), 'not raw ISO Z')
assert.ok(!/\.\d{3,}/.test(local), 'no fractional seconds: ' + local)
assert.equal(formatSourceSize(500), '500 B')
assert.equal(formatSourceSize(4949339), '4833.3 KB')
assert.equal(formatSourceSize(2048), '2 KB')
assert.equal(formatSourceSize(-1), '')
assert.equal(formatSourceSize(Number.NaN), '')

// Bulk roles collapse by role (edit_cache + snapshot), not path heuristics.
assert.equal(isCollapsibleSourceRole('edit_cache'), true)
assert.equal(isCollapsibleSourceRole('snapshot'), true)
assert.equal(isCollapsibleSourceRole('metadata'), true)
assert.equal(isCollapsibleSourceRole('events'), false)
assert.equal(isEditCacheRole('edit_cache'), true)
assert.equal(sourceRoleHelpKey('snapshot'), 'record.sourceRoleHelp.snapshot')
assert.equal(sourceFileHelpKey('/a/b/summary.json'), 'record.sourceFileHelp.summary_json')
assert.equal(sourceFileLabelKey('/a/b/signals.json'), 'record.sourceFileLabel.signals_json')
assert.equal(sourceFileHelpKey('/a/b/unknown-xyz.dat'), null)
assert.equal(isEditorFriendlySourcePath('/x/opencode.db'), false)
assert.equal(isEditorFriendlySourcePath('/x/session.json'), true)
assert.equal(isEditorFriendlySourcePath('/x/opencode.db-wal'), false)
const parts = partitionSourceFiles([
  { role: 'primary_transcript', path: '/s/session.json', state: 'present' },
  { role: 'collaboration', path: '/s/sub_agents/sessions/a.json', state: 'present' },
  { role: 'edit_cache', path: '/s/mutations/aaa', state: 'present' },
  { role: 'edit_cache', path: '/s/mutations/bbb', state: 'present' },
  { role: 'snapshot', path: '/s/snapshots/turn_1.json', state: 'present' },
  { role: 'snapshot', path: '/s/snapshots/turn_2.json', state: 'present' },
  { role: 'metadata', path: '/s/summary.json', state: 'present' },
  { role: 'metadata', path: '/s/signals.json', state: 'present' },
  { role: 'recovery', path: '/s/session.recovery.json', state: 'present' },
])
assert.equal(parts.main.length, 3) // primary, collab, recovery
assert.equal(parts.groups.length, 3)
assert.equal(parts.groups[0].role, 'edit_cache')
assert.equal(parts.groups[0].sources.length, 2)
assert.equal(parts.groups[1].role, 'snapshot')
assert.equal(parts.groups[1].sources.length, 2)
assert.equal(parts.groups[2].role, 'metadata')
assert.equal(parts.groups[2].sources.length, 2)

console.log('test-record-status: ok')
