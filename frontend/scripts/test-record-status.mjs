import assert from 'node:assert/strict'
import {
  doNotSumCapabilityAndParser,
  formatRecordTime,
  formatSourceSize,
  isKnownRecordState,
  parserWarningCount,
  presentFromSession,
  presentRecordStatus,
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
// Complete is neutral — not a celebratory green CTA.
assert.equal(complete.tone, 'neutral')
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

console.log('test-record-status: ok')
