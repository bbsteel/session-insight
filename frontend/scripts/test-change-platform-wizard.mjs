// Unit coverage for the change-platform wizard state helpers
// (frontend/src/changePlatformTypes.ts). Run via `npm run test:change-platform`.

import assert from 'node:assert/strict'

const {
  WIZARD_STEPS, initialWizardState, basicsReady, networkReady,
  confirmationKey, mappingComplete, mappingSelections, capabilityRows,
} = await import('/tmp/session-insight-change-platform/changePlatformTypes.js')

assert.deepEqual(WIZARD_STEPS, ['basics', 'network', 'analysis', 'mapping', 'finish'])
assert.equal(initialWizardState().step, 'basics')

// basics gating: all fields required, document must look like an OpenAPI doc.
const basics = { displayName: '', document: '', apiBaseUrl: '', sampleChangeUrl: '' }
assert.equal(basicsReady(basics), false)
assert.equal(basicsReady({ displayName: 'X', document: '{"openapi":"3.0.3"}', apiBaseUrl: 'https://r/api', sampleChangeUrl: 'https://r/p/1' }), true)
assert.equal(basicsReady({ displayName: 'X', document: 'hello world', apiBaseUrl: 'https://r/api', sampleChangeUrl: 'https://r/p/1' }), false)
assert.equal(basicsReady({ displayName: 'X', document: 'openapi: 3.1.0', apiBaseUrl: 'https://r/api', sampleChangeUrl: 'https://r/p/1' }), true)
assert.equal(basicsReady({ displayName: 'X', document: 'swagger: "2.0"', apiBaseUrl: 'https://r/api', sampleChangeUrl: 'https://r/p/1' }), true)

// network gating: env var names follow the backend contract.
assert.equal(networkReady({ credentialEnvName: '', allowHttp: false, allowPrivateNetwork: false }), false)
assert.equal(networkReady({ credentialEnvName: 'REVIEW_TOKEN', allowHttp: false, allowPrivateNetwork: false }), true)
assert.equal(networkReady({ credentialEnvName: '1BAD', allowHttp: false, allowPrivateNetwork: false }), false)
assert.equal(networkReady({ credentialEnvName: 'HAS DASH', allowHttp: false, allowPrivateNetwork: false }), false)

// mapping confirmation completeness
const confirmations = [
  { role: 'resolve_change', field: 'provider_object_id', candidates: [{ field: 'provider_object_id', pointer: '/id', confidence: 0.7, shape: 'integer' }], reason: 'confidence_or_ambiguity' },
]
assert.equal(mappingComplete(confirmations, {}), false)
assert.equal(mappingComplete(confirmations, { 'resolve_change.provider_object_id': '/id' }), true)
assert.equal(mappingComplete([], {}), true)
assert.deepEqual(
  mappingSelections(confirmations, { 'resolve_change.provider_object_id': '/id' }),
  [{ role: 'resolve_change', field: 'provider_object_id', pointer: '/id' }],
)
assert.equal(confirmationKey('a', 'b'), 'a.b')

// capability summary rows in stable order
const rows = capabilityRows({ metadata: 'supported', file_set: 'unsupported', patches: 'unsupported', modes: 'unsupported', commits: 'supported', content_anchor: 'head_sha', repository_id: 'unsupported' })
assert.deepEqual(rows.map(r => r.id), ['metadata', 'file_set', 'patches', 'modes', 'commits'])
assert.deepEqual(rows.map(r => r.supported), [true, false, false, false, true])

console.log('test-change-platform-wizard: all assertions passed')
