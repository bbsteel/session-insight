import assert from 'node:assert/strict'
import {
  buildExportRequest,
  bundleFilename,
  formatBundleSummary,
  importedBadgeText,
  initialExportSelection,
  sessionSelectionKey,
} from '/tmp/session-insight-import-presentation/importPresentation.js'

// Fake translator: echoes the key and sorted interpolation vars so assertions
// pin both the key choice and the values passed to it.
const t = (key, vars = {}) =>
  `${key}(${Object.entries(vars).map(([name, value]) => `${name}=${value}`).sort().join(',')})`

// sessionSelectionKey / initialExportSelection — never pre-select the whole list
assert.equal(sessionSelectionKey({ agent_type: 'claude', id: 'abc' }), 'claude:abc')
assert.equal(sessionSelectionKey({ agent_type: 'grok', id: '1-2-3' }), 'grok:1-2-3')
const many = [
  { agent_type: 'claude', id: 'a' },
  { agent_type: 'claude', id: 'b' },
  { agent_type: 'opencode', id: 'c' },
]
assert.deepEqual(
  initialExportSelection(many, null),
  [],
  'without a preferred session the export list starts empty',
)
assert.deepEqual(
  initialExportSelection(many, undefined),
  [],
)
assert.deepEqual(
  initialExportSelection(many, { agent_type: 'claude', id: 'b' }),
  ['claude:b'],
  'the focused session is pre-checked when it is in the filtered list',
)
assert.deepEqual(
  initialExportSelection(many, { agent_type: 'claude', id: 'missing' }),
  [],
  'a preferred id outside the filtered list does not select anything',
)
assert.deepEqual(
  initialExportSelection(many, { agent_type: '', id: 'a' }),
  [],
  'empty agent_type is not a valid preferred key',
)

// importedBadgeText
assert.equal(
  importedBadgeText(t, 'workstation'),
  'sidebar.importedBadge(host=workstation)',
)
assert.equal(
  importedBadgeText(t, ''),
  'sidebar.importedBadge(host=importBundle.unknownHost())',
  'a missing host falls back to the localized unknown-host label',
)
assert.equal(
  importedBadgeText(t, '  padded  '),
  'sidebar.importedBadge(host=padded)',
)

// buildExportRequest
const sessions = [
  { agent_type: 'claude', id: 's1' },
  { agent_type: 'codex', id: 's2' },
]
assert.deepEqual(
  buildExportRequest(sessions, { includeRaw: true, redact: false, caseLabel: '  INC-7  ' }),
  {
    sessions: [
      { agent_type: 'claude', id: 's1' },
      { agent_type: 'codex', id: 's2' },
    ],
    include_raw: true,
    redact: false,
    case_label: 'INC-7',
  },
)
assert.deepEqual(
  buildExportRequest([], { includeRaw: false, redact: true, caseLabel: '' }),
  { sessions: [], include_raw: false, redact: true, case_label: '' },
)

// formatBundleSummary
assert.equal(
  formatBundleSummary(t, { origin_host: 'workstation', case_label: 'INC-7', session_count: 3 }),
  'INC-7 · workstation · importBundle.bundleCount(count=3)',
)
assert.equal(
  formatBundleSummary(t, { origin_host: 'workstation', case_label: '', session_count: 1 }),
  'workstation · importBundle.bundleCount(count=1)',
  'an empty case label is omitted',
)
assert.equal(
  formatBundleSummary(t, { origin_host: '', case_label: '', session_count: 0 }),
  'importBundle.unknownHost() · importBundle.bundleCount(count=0)',
)

// bundleFilename — local-time components, zero-padded
assert.equal(
  bundleFilename(new Date(2026, 7, 6, 16, 5, 9)),
  'si-bundle-20260806-160509.sibundle',
)
assert.equal(
  bundleFilename(new Date(2026, 0, 1, 0, 0, 0)),
  'si-bundle-20260101-000000.sibundle',
)

console.log('import presentation tests passed')
