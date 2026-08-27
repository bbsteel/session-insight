export type GitEvidenceState = 'exact' | 'estimated' | 'missing' | 'unavailable'

export interface GitEvidenceAssessment {
  state: GitEvidenceState
  reason_code?: string
  reasons: string[]
}

export interface GitEvidenceLink {
  root_agent_type: string
  root_session_id: string
  source_agent_type: string
  source_session_id: string
  invocation_id?: string
  turn_index?: number
  assessment: GitEvidenceAssessment
}

export interface GitFileChange {
  ordinal: number
  key: string
  layer: 'tree' | 'index' | 'worktree' | 'hosted_change' | string
  display_path: string
  old_display_path?: string
  status: 'added' | 'modified' | 'deleted' | 'renamed' | 'copied' | string
  binary: boolean
  submodule: boolean
  additions?: number
  deletions?: number
  status_assessment: GitEvidenceAssessment
  patch_assessment: GitEvidenceAssessment
  evidence: GitEvidenceLink[]
}

export interface GitCandidateCommit {
  ordinal: number
  sha: string
  subject: string
  author_name?: string
  authored_at?: string
  committed_at?: string
  relation: string
  assessment: GitEvidenceAssessment
  evidence: GitEvidenceLink[]
}

export interface HostedRepositoryIdentity {
  host_id: string
  immutable_id: string
  slug: string
}

export interface ChangeRequestIdentity {
  provider: string
  host_id?: string
  target_repository?: HostedRepositoryIdentity
  provider_object_id?: string
  generic_opaque_id?: string
}

export interface ChangeRequestContentVersion {
  content_version_key: string
  native_version?: string
  base_ref_sha?: string
  diff_base_sha?: string
  head_sha?: string
  file_manifest_digest?: string
}

export interface ChangeRequestCompleteness {
  metadata: GitEvidenceAssessment
  file_set: GitEvidenceAssessment
  patches: GitEvidenceAssessment
  modes: GitEvidenceAssessment
  commits: GitEvidenceAssessment
}

export interface ChangeRequestSnapshot {
  snapshot_id: string
  identity: ChangeRequestIdentity
  content: ChangeRequestContentVersion
  metadata_revision: string
  kind: 'pull_request' | 'merge_request' | 'change' | 'code_review' | string
  display_number: string
  lifecycle_state: string
  draft: boolean
  title: string
  web_url: string
  source_repository?: HostedRepositoryIdentity
  source_ref?: string
  target_ref?: string
  files: GitFileChange[]
  commits: GitCandidateCommit[]
  completeness: ChangeRequestCompleteness
  fetched_at: string
}

export interface ChangeRequestRecord {
  change_key: string
  identity: ChangeRequestIdentity
  snapshot?: ChangeRequestSnapshot
  cache_state: string
  cache_assessment: GitEvidenceAssessment
  aliases: string[]
}

export type ChangeRequestRelationship = 'exclusive' | 'contributing' | 'related'

export interface SessionChangeRequestLink {
  ordinal: number
  link_id: string
  repository_entry_key?: string
  change: ChangeRequestIdentity
  content_version_key?: string
  relationship: ChangeRequestRelationship
  method: string
  assessment: GitEvidenceAssessment
  confirmation_source: string
  evidence: GitEvidenceLink[]
}

// A PR/MR reference the session itself recorded (created via CLI or mentioned
// in the transcript). Derived from exact creation evidence; no explicit user
// link and no per-agent Git snapshot contract is required.
export interface SessionRecordedChangeReference {
  kind: 'created' | 'mentioned' | string
  reference: ChangeRequestReference
  tool_name: string
  turn_index: number
  recorded_at: string
}

export interface SessionChangeRequestList {
  links: SessionChangeRequestLink[]
  derived: SessionRecordedChangeReference[]
}

export interface SessionGitRepositoryEvidence {
  repository_entry_key: string
  revision: number
  assessment: GitEvidenceAssessment
  provisional: boolean
  repository: {
    repository_entry_key: string
    worktree_root: string
    common_root_id: string
    worktree_id: string
    branch?: string
    head_sha?: string
    assessment: GitEvidenceAssessment
  }
  files: GitFileChange[]
  candidate_commits: GitCandidateCommit[]
  change_requests: SessionChangeRequestLink[]
  authority: 'hosted_change' | 'local_interval' | 'commit_graph' | 'none' | string
  stale: boolean
  generated_at: string
}

export interface SessionGitEvidenceEnvelope {
  root_agent_type: string
  root_session_id: string
  revision: number
  assessment: GitEvidenceAssessment
  provisional: boolean
  stale: boolean
  generated_at: string
  repositories: SessionGitRepositoryEvidence[]
}

export interface ChangeRequestSessionMatch {
  change_key: string
  link_id?: string
  root_agent_type: string
  root_session_id: string
  repository_entry_key?: string
  content_version_key?: string
  relationship?: ChangeRequestRelationship
  match: string
  assessment: GitEvidenceAssessment
}

export interface ChangeRequestReference {
  provider: string
  display_origin: string
  target_repository_slug?: string
  display_number?: string
  normalized_url: string
}

export interface ChangeRequestLookup {
  change: ChangeRequestRecord
  linked_sessions: ChangeRequestSessionMatch[]
  candidate_sessions: ChangeRequestSessionMatch[]
}

export interface ChangeRequestCreationSessionMatch {
  root_agent_type: string
  root_session_id: string
  evidence: {
    evidence_id: string
    reference: ChangeRequestReference
    command_kind: string
    tool_name: string
    event_id: string
    tool_call_id?: string
    turn_index: number
    invocation_id?: string
    recorded_at: string
    source_revision: string
    assessment: GitEvidenceAssessment
  }
}

export interface ChangeRequestResolveResponse {
  reference: ChangeRequestReference
  creation_sessions: ChangeRequestCreationSessionMatch[]
  matches: ChangeRequestLookup[]
  assessment: GitEvidenceAssessment
}

export interface ChangeHostPreview {
  host: {
    key: string
    provider: string
    display_origin: string
    endpoint_origins: string[]
  }
  requires_http_approval: boolean
  requires_private_network_approval: boolean
}

export function changeRequestDisplayName(record: ChangeRequestRecord): string {
  const snapshot = record.snapshot
  if (snapshot?.title) {
    const marker = snapshot.display_number ? `#${snapshot.display_number}` : ''
    return marker ? `${marker} ${snapshot.title}` : snapshot.title
  }
  return record.aliases[0] || record.identity.target_repository?.slug || record.change_key
}

export function canSelectHostedAuthority(record: ChangeRequestRecord, repositoryEntryKey: string): boolean {
  const snapshot = record.snapshot
  if (!repositoryEntryKey || !snapshot || record.cache_state !== 'current') return false
  const completeness = snapshot.completeness
  return Boolean(snapshot.content.content_version_key) &&
    completeness.metadata.state === 'exact' &&
    completeness.file_set.state === 'exact' &&
    completeness.patches.state === 'exact' &&
    completeness.modes.state === 'exact' &&
    completeness.commits.state === 'exact'
}

export function changeRequestSessionGroups(lookup: ChangeRequestLookup): {
  linked: ChangeRequestSessionMatch[]
  candidates: ChangeRequestSessionMatch[]
} {
  return {
    linked: [...lookup.linked_sessions],
    candidates: [...lookup.candidate_sessions],
  }
}
