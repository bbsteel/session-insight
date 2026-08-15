import assert from 'node:assert/strict'
import {
  canLoadChangeRequestHostedDetails,
  canSelectHostedAuthority,
  changeRequestDisplayName,
  changeRequestHostNeedsApproval,
  changeRequestLookupPresentation,
  changeRequestSessionGroups,
} from '/tmp/session-insight-git-evidence/gitEvidence.js'

const exact = { state: 'exact', reasons: [] }
const snapshot = {
  snapshot_id: 'snapshot-1',
  identity: { provider: 'github' },
  content: { content_version_key: 'version-1', head_sha: 'a'.repeat(40) },
  metadata_revision: 'metadata-1',
  kind: 'pull_request',
  display_number: '42',
  lifecycle_state: 'open',
  draft: false,
  title: 'Add Git evidence',
  web_url: 'https://github.com/example/project/pull/42',
  files: [],
  commits: [],
  completeness: {
    metadata: exact,
    file_set: exact,
    patches: exact,
    modes: exact,
    commits: exact,
  },
  fetched_at: '2026-08-11T00:00:00Z',
}
const record = {
  change_key: 'change-1',
  identity: { provider: 'github' },
  snapshot,
  cache_state: 'current',
  cache_assessment: exact,
  aliases: [snapshot.web_url],
}

assert.equal(changeRequestDisplayName(record), '#42 Add Git evidence')
assert.equal(canSelectHostedAuthority(record, 'repo-entry-1'), true)
assert.equal(canSelectHostedAuthority({ ...record, cache_state: 'stale' }, 'repo-entry-1'), false)
assert.equal(canSelectHostedAuthority(record, ''), false)
assert.equal(canSelectHostedAuthority({
  ...record,
  snapshot: {
    ...snapshot,
    completeness: {
      ...snapshot.completeness,
      patches: { state: 'missing', reason_code: 'change_request_partial', reasons: ['change_request_partial'] },
    },
  },
}, 'repo-entry-1'), false)

const linked = {
  change_key: 'change-1',
  link_id: 'link-1',
  root_agent_type: 'codex',
  root_session_id: 'session-linked',
  relationship: 'exclusive',
  match: 'linked',
  assessment: exact,
}
const candidate = {
  change_key: 'change-1',
  root_agent_type: 'codex',
  root_session_id: 'session-candidate',
  match: 'head_sha',
  assessment: { state: 'estimated', reason_code: 'change_link_ambiguous', reasons: ['change_link_ambiguous'] },
}
const groups = changeRequestSessionGroups({
  change: record,
  linked_sessions: [linked],
  candidate_sessions: [candidate],
})
assert.deepEqual(groups.linked, [linked])
assert.deepEqual(groups.candidates, [candidate])
assert.notEqual(groups.linked, groups.candidates)

const creationMatch = {
  root_agent_type: 'codex',
  root_session_id: 'session-created',
  evidence: {
    evidence_id: 'cr-create-1',
    reference: {
      provider: 'github',
      display_origin: 'https://github.com',
      target_repository_slug: 'example/project',
      display_number: '42',
      normalized_url: snapshot.web_url,
    },
    command_kind: 'github_cli_pr_create',
    tool_name: 'exec',
    event_id: 'invoke',
    turn_index: 7,
    recorded_at: '2026-08-11T16:17:21Z',
    source_revision: 'sha256:source',
    assessment: exact,
  },
}
const resolveResult = {
  reference: creationMatch.evidence.reference,
  creation_sessions: [creationMatch],
  matches: [{
    change: record,
    linked_sessions: [linked],
    candidate_sessions: [candidate],
  }],
  assessment: exact,
}
const presentation = changeRequestLookupPresentation(resolveResult)
assert.deepEqual(presentation.creationSessions, [creationMatch])
assert.equal(presentation.hostedLookups.length, 1)
assert.notEqual(presentation.creationSessions, resolveResult.creation_sessions)
assert.notEqual(presentation.hostedLookups[0].linked_sessions, presentation.creationSessions)
assert.equal(changeRequestHostNeedsApproval(resolveResult), false)
assert.equal(canLoadChangeRequestHostedDetails(resolveResult), false)
assert.equal(canLoadChangeRequestHostedDetails({
  ...resolveResult,
  matches: [],
}), true)
assert.equal(canLoadChangeRequestHostedDetails({
  ...resolveResult,
  matches: [],
  assessment: { state: 'missing', reason_code: 'change_host_not_approved', reasons: ['change_host_not_approved'] },
}), false)
assert.equal(canLoadChangeRequestHostedDetails({
  reference: { ...creationMatch.evidence.reference, provider: 'generic' },
  creation_sessions: [creationMatch],
  matches: [],
  assessment: exact,
}), false)
assert.deepEqual(changeRequestLookupPresentation({
  reference: creationMatch.evidence.reference,
  assessment: exact,
}).creationSessions, [])
assert.equal(canSelectHostedAuthority({
  ...record,
  cache_state: 'unexpected-cache',
  snapshot: {
    ...snapshot,
    completeness: {
      ...snapshot.completeness,
      metadata: { state: 'mystery', reasons: [] },
    },
  },
}, 'repo-entry-1'), false)
assert.equal(changeRequestDisplayName({
  change_key: 'change-unknown',
  identity: { provider: 'mystery-forge' },
  cache_state: 'unknown-cache',
  cache_assessment: { state: 'mystery', reasons: [] },
  aliases: [],
}), 'change-unknown')

console.log('Git evidence presentation tests passed')
