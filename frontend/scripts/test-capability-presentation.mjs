import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  BASELINE_CAPABILITY_IDS,
  actionIsConfirmedAvailable,
  actionAvailabilityLabelKey,
  capabilityDescriptionKey,
  capabilityLabelKey,
  capabilityStateIsWarning,
  capabilityStateSeverity,
  capabilityStateSymbol,
  capabilityStateTone,
  orderedSessionStatuses,
  orderedStaticCapabilities,
  reasonCodeLabelKey,
  sessionCapabilityHeaderHint,
  summarizeCapabilityStates,
  summarizeSessionCaps,
  summarizeStaticAgent,
  assertNoAgentCapabilityMatrixInModuleSource,
} from '/tmp/session-insight-capability-presentation/capabilityPresentation.js'

// --- ordering ---
assert.equal(BASELINE_CAPABILITY_IDS.length, 10)
assert.deepEqual([...BASELINE_CAPABILITY_IDS], [
  'discovery', 'replay', 'realtime', 'tokens', 'tool_results',
  'diff', 'subtasks', 'resume', 'delete', 'terminate',
])

// --- five state symbols ---
assert.equal(capabilityStateSymbol('exact'), '✓')
assert.equal(capabilityStateSymbol('estimated'), '≈')
assert.equal(capabilityStateSymbol('missing'), '!')
assert.equal(capabilityStateSymbol('not_applicable'), '—')
assert.equal(capabilityStateSymbol('unsupported'), '×')
assert.equal(capabilityStateSymbol('not_applicable').charCodeAt(0), 0x2014) // em dash, not circle
assert.notEqual(capabilityStateSymbol('not_applicable'), '○')
assert.notEqual(capabilityStateSymbol('not_applicable'), '◯')

// --- not_applicable is not a warning ---
assert.equal(capabilityStateIsWarning('not_applicable'), false)
assert.equal(capabilityStateSeverity('not_applicable'), 0)
assert.equal(capabilityStateTone('not_applicable'), 'muted')
assert.equal(capabilityStateIsWarning('missing'), true)
assert.equal(capabilityStateIsWarning('estimated'), true)
assert.ok(capabilityStateSeverity('missing') > capabilityStateSeverity('estimated'))

// --- summary: all-exact is calm ---
const calm = summarizeCapabilityStates([
  { state: 'exact' }, { state: 'exact' }, { state: 'not_applicable' },
])
assert.equal(calm.hasWarning, false)
assert.equal(calm.warningCount, 0)
assert.equal(calm.exact, 2)
assert.equal(calm.not_applicable, 1)

// --- missing severity > estimated ---
const mixed = summarizeCapabilityStates([
  { state: 'estimated' }, { state: 'missing' }, { state: 'not_applicable' },
])
assert.equal(mixed.missing, 1)
assert.equal(mixed.estimated, 1)
assert.equal(mixed.maxSeverity, 3)
assert.equal(mixed.warningCount, 2) // not_applicable excluded

// --- static ordering + unknown id keys ---
const staticRows = orderedStaticCapabilities({
  tokens: { state: 'exact' },
  discovery: { state: 'exact' },
})
assert.equal(staticRows[0].id, 'discovery')
assert.equal(staticRows.find(r => r.id === 'tokens')?.decl?.state, 'exact')
assert.equal(staticRows.find(r => r.id === 'diff')?.decl, null) // absent, not invented
assert.equal(staticRows.length, 10)

const agentSum = summarizeStaticAgent({
  discovery: { state: 'exact' },
  tokens: { state: 'estimated', reason_code: 'timestamp_heuristic' },
  subtasks: { state: 'unsupported', reason_code: 'adapter_not_implemented' },
  terminate: { state: 'not_applicable', reason_code: 'concept_absent' },
})
assert.equal(agentSum.estimated, 1)
assert.equal(agentSum.unsupported >= 1, true)
assert.ok(agentSum.hasWarning)

// --- session status distinct from static ---
const session = {
  agent_type: 'claude',
  adapter_revision: 1,
  status: {
    tokens: { state: 'missing', reason_code: 'session_not_finalized' },
    discovery: { state: 'exact' },
  },
  liveness: { is_live: false, state: 'estimated', reason_code: 'timestamp_heuristic' },
  actions: {
    resume: { availability: 'available' },
    delete: { availability: 'unavailable', reason_code: 'session_running' },
    terminate: { availability: 'runtime_check_required', reason_code: 'runtime_check_required' },
  },
}
const sessRows = orderedSessionStatuses(session.status)
assert.equal(sessRows.find(r => r.id === 'tokens')?.status?.state, 'missing')
assert.notEqual(sessRows.find(r => r.id === 'tokens')?.status?.state, 'exact')
assert.equal(sessRows.find(r => r.id === 'diff')?.status, null)
const sessSum = summarizeSessionCaps(session)
assert.equal(sessSum.missing >= 1, true)
const hint = sessionCapabilityHeaderHint(session)
assert.equal(hint.kind, 'missing')
assert.ok(hint.count >= 1)

// --- runtime_check_required is not confirmed available ---
assert.equal(actionIsConfirmedAvailable('runtime_check_required'), false)
assert.equal(actionIsConfirmedAvailable('available'), true)
assert.equal(actionIsConfirmedAvailable('unavailable'), false)
assert.equal(actionAvailabilityLabelKey('runtime_check_required'), 'capability.action.runtime_check_required')

// --- unknown reason / capability fallbacks ---
assert.equal(reasonCodeLabelKey('session_not_finalized'), 'capability.reason.session_not_finalized')
assert.equal(reasonCodeLabelKey('totally_unknown_code_xyz'), 'capability.reason.unknown')
assert.equal(reasonCodeLabelKey(''), null)
assert.equal(capabilityLabelKey('tokens'), 'capability.id.tokens')
assert.equal(capabilityLabelKey('future_cap_xyz'), 'capability.id.unknown')
assert.equal(capabilityDescriptionKey('future_cap_xyz'), 'capability.desc.unknown')

// --- no Agent-specific capability matrix in presentation module source ---
const here = dirname(fileURLToPath(import.meta.url))
const srcPath = join(here, '..', 'src', 'capabilityPresentation.ts')
const src = readFileSync(srcPath, 'utf8')
assert.equal(assertNoAgentCapabilityMatrixInModuleSource(src), true)
// Explicit: module must not hardcode Agent product capability values
assert.equal(/\bclaude\b.*\btokens\b.*exact/i.test(src), false)
assert.equal(/AGENT_CAPABILITY_MATRIX/.test(src), false)

console.log('capability presentation tests passed')
