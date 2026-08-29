package model

import "time"

// GitEvidenceState is the precision of one Git or hosted-change fact. Exact
// means the stated observation or hosted revision is exact; it never implies
// that a Session caused the observed change.
type GitEvidenceState string

const (
	GitEvidenceExact       GitEvidenceState = "exact"
	GitEvidenceEstimated   GitEvidenceState = "estimated"
	GitEvidenceMissing     GitEvidenceState = "missing"
	GitEvidenceUnavailable GitEvidenceState = "unavailable"
)

// IsKnownGitEvidenceState reports whether state is part of the frozen API
// vocabulary.
func IsKnownGitEvidenceState(state GitEvidenceState) bool {
	switch state {
	case GitEvidenceExact, GitEvidenceEstimated, GitEvidenceMissing, GitEvidenceUnavailable:
		return true
	default:
		return false
	}
}

// GitEvidenceReasonCode is a stable, non-localized explanation for evidence
// that is not exact. UI copy is keyed by these values; producers must not emit
// raw Git, filesystem, or provider errors as reason codes.
type GitEvidenceReasonCode string

const (
	ReasonBaselineNotCaptured                GitEvidenceReasonCode = "baseline_not_captured"
	ReasonBaselineDirtyStateMissing          GitEvidenceReasonCode = "baseline_dirty_state_missing"
	ReasonBaselineCapturedAfterMutation      GitEvidenceReasonCode = "baseline_captured_after_mutation"
	ReasonFinalNotCaptured                   GitEvidenceReasonCode = "final_not_captured"
	ReasonSessionStillLive                   GitEvidenceReasonCode = "session_still_live"
	ReasonAgentGitFactMissing                GitEvidenceReasonCode = "agent_git_fact_missing"
	ReasonAgentGitFactInvalid                GitEvidenceReasonCode = "agent_git_fact_invalid"
	ReasonAgentGitFactTimestampUnavailable   GitEvidenceReasonCode = "agent_git_fact_timestamp_unavailable"
	ReasonRepositoryNotFound                 GitEvidenceReasonCode = "repository_not_found"
	ReasonNotAGitRepository                  GitEvidenceReasonCode = "not_a_git_repository"
	ReasonWorktreeIdentityChanged            GitEvidenceReasonCode = "worktree_identity_changed"
	ReasonHeadHistoryRewritten               GitEvidenceReasonCode = "head_history_rewritten"
	ReasonSharedWorktreeOverlap              GitEvidenceReasonCode = "shared_worktree_overlap"
	ReasonSnapshotLimitExceeded              GitEvidenceReasonCode = "snapshot_limit_exceeded"
	ReasonSnapshotObjectMissing              GitEvidenceReasonCode = "snapshot_object_missing"
	ReasonCaptureRaced                       GitEvidenceReasonCode = "capture_raced"
	ReasonSourceRevisionChanged              GitEvidenceReasonCode = "source_revision_changed"
	ReasonBinaryPatchUnavailable             GitEvidenceReasonCode = "binary_patch_unavailable"
	ReasonSubmoduleNotExpanded               GitEvidenceReasonCode = "submodule_not_expanded"
	ReasonForeignImport                      GitEvidenceReasonCode = "foreign_import"
	ReasonSourceMissing                      GitEvidenceReasonCode = "source_missing"
	ReasonGitCommandFailed                   GitEvidenceReasonCode = "git_command_failed"
	ReasonGitCommandTimedOut                 GitEvidenceReasonCode = "git_command_timed_out"
	ReasonChangeHostNotApproved              GitEvidenceReasonCode = "change_host_not_approved"
	ReasonChangeHostAuthRequired             GitEvidenceReasonCode = "change_host_auth_required"
	ReasonChangeProviderUnsupported          GitEvidenceReasonCode = "change_provider_unsupported"
	ReasonChangeRequestNotFound              GitEvidenceReasonCode = "change_request_not_found"
	ReasonChangeRequestPartial               GitEvidenceReasonCode = "change_request_partial"
	ReasonChangeRequestOverflow              GitEvidenceReasonCode = "change_request_overflow"
	ReasonChangeRequestRevisionChanged       GitEvidenceReasonCode = "change_request_revision_changed"
	ReasonChangeRequestCaptureRaced          GitEvidenceReasonCode = "change_request_capture_raced"
	ReasonChangeRequestPendingReconfirmation GitEvidenceReasonCode = "change_request_pending_reconfirmation"
	ReasonChangeLinkAmbiguous                GitEvidenceReasonCode = "change_link_ambiguous"
	ReasonChangeLinkContributingOnly         GitEvidenceReasonCode = "change_link_contributing_only"
	ReasonChangeAliasAmbiguous               GitEvidenceReasonCode = "change_alias_ambiguous"
	ReasonChangeHostRevoked                  GitEvidenceReasonCode = "change_host_revoked"
)

// IsKnownGitEvidenceReasonCode reports whether code is part of the frozen
// reason vocabulary.
func IsKnownGitEvidenceReasonCode(code GitEvidenceReasonCode) bool {
	switch code {
	case ReasonBaselineNotCaptured, ReasonBaselineDirtyStateMissing,
		ReasonBaselineCapturedAfterMutation, ReasonFinalNotCaptured,
		ReasonSessionStillLive, ReasonAgentGitFactMissing,
		ReasonAgentGitFactInvalid, ReasonAgentGitFactTimestampUnavailable,
		ReasonRepositoryNotFound, ReasonNotAGitRepository,
		ReasonWorktreeIdentityChanged, ReasonHeadHistoryRewritten,
		ReasonSharedWorktreeOverlap, ReasonSnapshotLimitExceeded,
		ReasonSnapshotObjectMissing, ReasonCaptureRaced,
		ReasonSourceRevisionChanged, ReasonBinaryPatchUnavailable,
		ReasonSubmoduleNotExpanded, ReasonForeignImport, ReasonSourceMissing,
		ReasonGitCommandFailed, ReasonGitCommandTimedOut,
		ReasonChangeHostNotApproved, ReasonChangeHostAuthRequired,
		ReasonChangeProviderUnsupported, ReasonChangeRequestNotFound,
		ReasonChangeRequestPartial, ReasonChangeRequestOverflow,
		ReasonChangeRequestRevisionChanged, ReasonChangeRequestCaptureRaced,
		ReasonChangeRequestPendingReconfirmation, ReasonChangeLinkAmbiguous,
		ReasonChangeLinkContributingOnly, ReasonChangeAliasAmbiguous,
		ReasonChangeHostRevoked:
		return true
	default:
		return false
	}
}

// GitEvidenceAssessment carries a primary reason for compact clients and the
// full ordered set of applicable reasons. For non-exact states ReasonCode is
// required and Reasons starts with ReasonCode.
type GitEvidenceAssessment struct {
	State      GitEvidenceState        `json:"state"`
	ReasonCode GitEvidenceReasonCode   `json:"reason_code,omitempty"`
	Reasons    []GitEvidenceReasonCode `json:"reasons"`
}

// ExactGitEvidence constructs an exact assessment with no reason.
func ExactGitEvidence() GitEvidenceAssessment {
	return GitEvidenceAssessment{State: GitEvidenceExact, Reasons: []GitEvidenceReasonCode{}}
}

// NonExactGitEvidence constructs a non-exact assessment with a stable primary
// reason and ordered full reason set. Validation still rejects an exact state
// or an empty reason so callers cannot use this helper to bypass the contract.
func NonExactGitEvidence(state GitEvidenceState, reason GitEvidenceReasonCode, additional ...GitEvidenceReasonCode) GitEvidenceAssessment {
	reasons := make([]GitEvidenceReasonCode, 1, 1+len(additional))
	reasons[0] = reason
	reasons = append(reasons, additional...)
	return GitEvidenceAssessment{State: state, ReasonCode: reason, Reasons: reasons}
}

// GitEvidenceSource identifies which evidence layer recorded a fact.
type GitEvidenceSource string

const (
	GitSourceAgentRecorded GitEvidenceSource = "agent_recorded"
	GitSourceSIObserved    GitEvidenceSource = "si_observed"
	GitSourceHostedChange  GitEvidenceSource = "hosted_change"
)

func IsKnownGitEvidenceSource(source GitEvidenceSource) bool {
	switch source {
	case GitSourceAgentRecorded, GitSourceSIObserved, GitSourceHostedChange:
		return true
	default:
		return false
	}
}

// GitFact preserves a value together with field-level precision and source
// revision. A missing value still serializes its zero Value; clients must use
// Assessment rather than guessing presence from the value.
type GitFact[T any] struct {
	Value          T                     `json:"value"`
	Assessment     GitEvidenceAssessment `json:"assessment"`
	Source         GitEvidenceSource     `json:"source"`
	RecordedAt     *time.Time            `json:"recorded_at,omitempty"`
	SourceRevision string                `json:"source_revision,omitempty"`
}

type GitDirtyState string

const (
	GitDirtyClean   GitDirtyState = "clean"
	GitDirtyDirty   GitDirtyState = "dirty"
	GitDirtyUnknown GitDirtyState = "unknown"
)

func IsKnownGitDirtyState(state GitDirtyState) bool {
	switch state {
	case GitDirtyClean, GitDirtyDirty, GitDirtyUnknown:
		return true
	default:
		return false
	}
}

type SessionGitOrigin struct {
	RepositoryURL GitFact[string]        `json:"repository_url"`
	WorktreePath  GitFact[string]        `json:"worktree_path"`
	Branch        GitFact[string]        `json:"branch"`
	HeadSHA       GitFact[string]        `json:"head_sha"`
	DirtyState    GitFact[GitDirtyState] `json:"dirty_state"`
}

// GitRepositoryBinding identifies exactly one repository/worktree entry for
// one attribution root. CommonRootID and WorktreeID are opaque hashes; raw Git
// administrative paths are storage-private.
type GitRepositoryBinding struct {
	// RepositoryEntryKey is an opaque, server-issued API key. Bind clients
	// submit this key, never a root identity, worktree path, or binding hash.
	RepositoryEntryKey string                `json:"repository_entry_key"`
	WorktreeRoot       string                `json:"worktree_root"`
	CommonRootID       string                `json:"common_root_id"`
	WorktreeID         string                `json:"worktree_id"`
	Branch             string                `json:"branch,omitempty"`
	HeadSHA            string                `json:"head_sha,omitempty"`
	Assessment         GitEvidenceAssessment `json:"assessment"`
}

type GitSnapshotKind string

const (
	GitSnapshotBaseline   GitSnapshotKind = "baseline"
	GitSnapshotCheckpoint GitSnapshotKind = "checkpoint"
	GitSnapshotFinal      GitSnapshotKind = "final"
)

type GitSnapshotSummary struct {
	SnapshotID       string                `json:"snapshot_id"`
	Kind             GitSnapshotKind       `json:"kind"`
	HeadSHA          string                `json:"head_sha,omitempty"`
	ManifestDigest   string                `json:"manifest_digest,omitempty"`
	SourceRevision   string                `json:"source_revision"`
	CaptureStartedAt time.Time             `json:"capture_started_at"`
	CaptureEndedAt   time.Time             `json:"capture_completed_at"`
	Assessment       GitEvidenceAssessment `json:"assessment"`
}

type GitFileLayer string

const (
	GitFileLayerTree     GitFileLayer = "tree"
	GitFileLayerIndex    GitFileLayer = "index"
	GitFileLayerWorktree GitFileLayer = "worktree"
	GitFileLayerHosted   GitFileLayer = "hosted_change"
)

type GitFileStatus string

const (
	GitFileAdded    GitFileStatus = "added"
	GitFileModified GitFileStatus = "modified"
	GitFileDeleted  GitFileStatus = "deleted"
	GitFileRenamed  GitFileStatus = "renamed"
	GitFileCopied   GitFileStatus = "copied"
)

type GitPathEncoding string

const (
	GitPathUTF8     GitPathEncoding = "utf8"
	GitPathBytesB64 GitPathEncoding = "bytes_b64"
)

// GitEvidenceLink anchors one derived Git fact to the precise Session source
// dimensions that supplied it. Root, source/backing Session and invocation
// identity remain separate so collaboration evidence is not flattened.
type GitEvidenceLink struct {
	RootAgentType    string `json:"root_agent_type"`
	RootSessionID    string `json:"root_session_id"`
	SourceAgentType  string `json:"source_agent_type"`
	SourceSessionID  string `json:"source_session_id"`
	BackingAgentType string `json:"backing_agent_type,omitempty"`
	BackingSessionID string `json:"backing_session_id,omitempty"`
	InvocationID     string `json:"invocation_id,omitempty"`
	// SourceRevision identifies the authoritative adapter source snapshot.
	// PositionsRevision independently identifies the rendered position layout;
	// matching one never implies that the other is current.
	SourceRevision    string                `json:"source_revision"`
	PositionsRevision int64                 `json:"positions_revision"`
	EventID           string                `json:"event_id,omitempty"`
	ToolCallID        string                `json:"tool_call_id,omitempty"`
	TurnIndex         *int                  `json:"turn_index,omitempty"`
	RecordedAt        *time.Time            `json:"recorded_at,omitempty"`
	Assessment        GitEvidenceAssessment `json:"assessment"`
}

type GitFileChange struct {
	Ordinal          int                   `json:"ordinal"`
	Key              string                `json:"key"`
	Layer            GitFileLayer          `json:"layer"`
	DisplayPath      string                `json:"display_path"`
	OldDisplayPath   string                `json:"old_display_path,omitempty"`
	PathBytesB64     string                `json:"path_bytes_b64,omitempty"`
	OldPathBytesB64  string                `json:"old_path_bytes_b64,omitempty"`
	PathEncoding     GitPathEncoding       `json:"path_encoding"`
	Status           GitFileStatus         `json:"status"`
	OldMode          string                `json:"old_mode,omitempty"`
	NewMode          string                `json:"new_mode,omitempty"`
	Binary           bool                  `json:"binary"`
	Submodule        bool                  `json:"submodule"`
	Additions        *int                  `json:"additions,omitempty"`
	Deletions        *int                  `json:"deletions,omitempty"`
	StatusAssessment GitEvidenceAssessment `json:"status_assessment"`
	PatchAssessment  GitEvidenceAssessment `json:"patch_assessment"`
	Evidence         []GitEvidenceLink     `json:"evidence"`
}

type GitCandidateCommitRelation string

const (
	GitCommitDescendant       GitCandidateCommitRelation = "descendant"
	GitCommitChangeMembership GitCandidateCommitRelation = "change_request_membership"
	GitCommitPathOverlap      GitCandidateCommitRelation = "path_overlap"
	GitCommitTimeWindow       GitCandidateCommitRelation = "time_window"
)

type GitCandidateCommit struct {
	Ordinal     int                        `json:"ordinal"`
	SHA         string                     `json:"sha"`
	Subject     string                     `json:"subject"`
	AuthorName  string                     `json:"author_name,omitempty"`
	AuthoredAt  *time.Time                 `json:"authored_at,omitempty"`
	CommittedAt *time.Time                 `json:"committed_at,omitempty"`
	Relation    GitCandidateCommitRelation `json:"relation"`
	Assessment  GitEvidenceAssessment      `json:"assessment"`
	Evidence    []GitEvidenceLink          `json:"evidence"`
}

// ChangeProviderKind is provider-neutral. The first automatic providers are
// GitHub and GitLab; Generic stores an exact sanitized URL without inventing
// provider semantics.
type ChangeProviderKind string

const (
	ChangeProviderGitHub              ChangeProviderKind = "github"
	ChangeProviderGitLab              ChangeProviderKind = "gitlab"
	ChangeProviderGitea               ChangeProviderKind = "gitea"
	ChangeProviderForgejo             ChangeProviderKind = "forgejo"
	ChangeProviderBitbucketCloud      ChangeProviderKind = "bitbucket_cloud"
	ChangeProviderBitbucketDataCenter ChangeProviderKind = "bitbucket_data_center"
	ChangeProviderAzureDevOps         ChangeProviderKind = "azure_devops"
	ChangeProviderGerrit              ChangeProviderKind = "gerrit"
	// ChangeProviderOpenAPI is the single execution-engine kind for previously
	// unknown change hosts adapted through a verified declarative Provider
	// Profile. Individual platforms are distinguished by host_id/profile_id,
	// never by adding another provider kind.
	ChangeProviderOpenAPI ChangeProviderKind = "openapi"
	ChangeProviderGeneric ChangeProviderKind = "generic"
)

func IsKnownChangeProviderKind(kind ChangeProviderKind) bool {
	switch kind {
	case ChangeProviderGitHub, ChangeProviderGitLab, ChangeProviderGitea,
		ChangeProviderForgejo, ChangeProviderBitbucketCloud,
		ChangeProviderBitbucketDataCenter, ChangeProviderAzureDevOps,
		ChangeProviderGerrit, ChangeProviderOpenAPI, ChangeProviderGeneric:
		return true
	default:
		return false
	}
}

type HostedRepositoryIdentity struct {
	HostID      string `json:"host_id"`
	ImmutableID string `json:"immutable_id"`
	Slug        string `json:"slug"`
}

type HostedRepositoryReference struct {
	Provider ChangeProviderKind `json:"provider"`
	// HostID binds an OpenAPI-profile reference to exactly one approved host.
	// Built-in providers fill their fixed public host key; Generic stays empty.
	HostID          string `json:"host_id,omitempty"`
	DisplayOrigin   string `json:"display_origin"`
	Slug            string `json:"slug"`
	SanitizedRemote string `json:"sanitized_remote"`
}

type ChangeRequestReference struct {
	Provider ChangeProviderKind `json:"provider"`
	// HostID binds an OpenAPI-profile reference to exactly one approved host.
	// Built-in providers fill their fixed public host key; Generic stays empty.
	HostID               string `json:"host_id,omitempty"`
	DisplayOrigin        string `json:"display_origin"`
	TargetRepositorySlug string `json:"target_repository_slug,omitempty"`
	DisplayNumber        string `json:"display_number,omitempty"`
	NormalizedURL        string `json:"normalized_url"`
}

// ChangeRequestIdentity is canonical only after provider resolution. Generic
// identities instead use GenericOpaqueID and have no fabricated repository or
// display-number identity.
type ChangeRequestIdentity struct {
	Provider         ChangeProviderKind        `json:"provider"`
	HostID           string                    `json:"host_id,omitempty"`
	TargetRepository *HostedRepositoryIdentity `json:"target_repository,omitempty"`
	ProviderObjectID string                    `json:"provider_object_id,omitempty"`
	GenericOpaqueID  string                    `json:"generic_opaque_id,omitempty"`
}

// ContentVersionKey fixes one immutable hosted Change Request content
// version. It must not be derived from updated_at or ETag alone.
type ContentVersionKey string

type ChangeRequestContentVersion struct {
	Key                ContentVersionKey `json:"content_version_key"`
	NativeVersion      string            `json:"native_version,omitempty"`
	BaseRefSHA         string            `json:"base_ref_sha,omitempty"`
	DiffBaseSHA        string            `json:"diff_base_sha,omitempty"`
	HeadSHA            string            `json:"head_sha,omitempty"`
	FileManifestDigest string            `json:"file_manifest_digest,omitempty"`
}

type ChangeRequestDimension string

const (
	ChangeDimensionMetadata ChangeRequestDimension = "metadata"
	ChangeDimensionFileSet  ChangeRequestDimension = "file_set"
	ChangeDimensionPatches  ChangeRequestDimension = "patches"
	ChangeDimensionModes    ChangeRequestDimension = "modes"
	ChangeDimensionCommits  ChangeRequestDimension = "commits"
)

// ChangeRequestCompleteness keeps hosted metadata, files, patches, modes and
// commits independent. A provider limit must degrade only affected dimensions.
type ChangeRequestCompleteness struct {
	Metadata GitEvidenceAssessment `json:"metadata"`
	FileSet  GitEvidenceAssessment `json:"file_set"`
	Patches  GitEvidenceAssessment `json:"patches"`
	Modes    GitEvidenceAssessment `json:"modes"`
	Commits  GitEvidenceAssessment `json:"commits"`
}

type ChangeRequestKind string

const (
	ChangeRequestPullRequest  ChangeRequestKind = "pull_request"
	ChangeRequestMergeRequest ChangeRequestKind = "merge_request"
	ChangeRequestChange       ChangeRequestKind = "change"
	ChangeRequestCodeReview   ChangeRequestKind = "code_review"
)

type ChangeRequestLifecycleState string

const (
	ChangeLifecycleOpen      ChangeRequestLifecycleState = "open"
	ChangeLifecycleMerged    ChangeRequestLifecycleState = "merged"
	ChangeLifecycleClosed    ChangeRequestLifecycleState = "closed"
	ChangeLifecycleAbandoned ChangeRequestLifecycleState = "abandoned"
	ChangeLifecycleUnknown   ChangeRequestLifecycleState = "unknown"
)

type ChangeRequestSummary struct {
	Identity         ChangeRequestIdentity       `json:"identity"`
	Content          ChangeRequestContentVersion `json:"content"`
	Kind             ChangeRequestKind           `json:"kind"`
	DisplayNumber    string                      `json:"display_number"`
	LifecycleState   ChangeRequestLifecycleState `json:"lifecycle_state"`
	Draft            bool                        `json:"draft"`
	Title            string                      `json:"title"`
	WebURL           string                      `json:"web_url"`
	SourceRepository *HostedRepositoryIdentity   `json:"source_repository,omitempty"`
	SourceRef        string                      `json:"source_ref,omitempty"`
	TargetRef        string                      `json:"target_ref,omitempty"`
	MergeCommitSHA   string                      `json:"merge_commit_sha,omitempty"`
	SquashCommitSHA  string                      `json:"squash_commit_sha,omitempty"`
	Completeness     ChangeRequestCompleteness   `json:"completeness"`
}

type ChangeRequestSnapshot struct {
	SnapshotID       string                      `json:"snapshot_id"`
	Identity         ChangeRequestIdentity       `json:"identity"`
	Content          ChangeRequestContentVersion `json:"content"`
	MetadataRevision string                      `json:"metadata_revision"`
	Kind             ChangeRequestKind           `json:"kind"`
	DisplayNumber    string                      `json:"display_number"`
	LifecycleState   ChangeRequestLifecycleState `json:"lifecycle_state"`
	Draft            bool                        `json:"draft"`
	Title            string                      `json:"title"`
	WebURL           string                      `json:"web_url"`
	SourceRepository *HostedRepositoryIdentity   `json:"source_repository,omitempty"`
	SourceRef        string                      `json:"source_ref,omitempty"`
	TargetRef        string                      `json:"target_ref,omitempty"`
	MergeCommitSHA   string                      `json:"merge_commit_sha,omitempty"`
	SquashCommitSHA  string                      `json:"squash_commit_sha,omitempty"`
	Files            []GitFileChange             `json:"files"`
	Commits          []GitCandidateCommit        `json:"commits"`
	Completeness     ChangeRequestCompleteness   `json:"completeness"`
	ETag             string                      `json:"etag,omitempty"`
	FetchedAt        time.Time                   `json:"fetched_at"`
}

type ChangeRequestRelationship string

const (
	ChangeRelationshipExclusive    ChangeRequestRelationship = "exclusive"
	ChangeRelationshipContributing ChangeRequestRelationship = "contributing"
	ChangeRelationshipRelated      ChangeRequestRelationship = "related"
)

type ChangeRequestLinkMethod string

const (
	ChangeLinkExplicit         ChangeRequestLinkMethod = "explicit"
	ChangeLinkAgentNative      ChangeRequestLinkMethod = "agent_native"
	ChangeLinkURLMention       ChangeRequestLinkMethod = "url_mention"
	ChangeLinkHeadSHA          ChangeRequestLinkMethod = "head_sha"
	ChangeLinkCommitMembership ChangeRequestLinkMethod = "commit_membership"
	ChangeLinkBranch           ChangeRequestLinkMethod = "branch"
)

type ChangeRequestConfirmationSource string

const (
	ChangeConfirmationNone ChangeRequestConfirmationSource = "none"
	ChangeConfirmationUser ChangeRequestConfirmationSource = "user"
)

type SessionChangeRequestLink struct {
	Ordinal               int                             `json:"ordinal"`
	LinkID                string                          `json:"link_id"`
	RootAgentType         string                          `json:"root_agent_type"`
	RootSessionID         string                          `json:"root_session_id"`
	SourceAgentType       string                          `json:"source_agent_type"`
	SourceSessionID       string                          `json:"source_session_id"`
	CollaborationRevision int64                           `json:"collaboration_revision"`
	InvocationID          string                          `json:"invocation_id,omitempty"`
	RepositoryEntryKey    string                          `json:"repository_entry_key,omitempty"`
	Change                ChangeRequestIdentity           `json:"change"`
	ContentVersionKey     ContentVersionKey               `json:"content_version_key,omitempty"`
	Relationship          ChangeRequestRelationship       `json:"relationship"`
	Method                ChangeRequestLinkMethod         `json:"method"`
	Assessment            GitEvidenceAssessment           `json:"assessment"`
	ConfirmationSource    ChangeRequestConfirmationSource `json:"confirmation_source"`
	ConfirmationRevision  string                          `json:"confirmation_revision,omitempty"`
	Evidence              []GitEvidenceLink               `json:"evidence"`
}

// ChangeRequestBindRequest is the client-writable bind contract. Root/source
// Session identity, collaboration revision, invocation, and worktree identity
// are intentionally absent: the server derives them from the URL-path Session
// and this opaque repository entry key.
type ChangeRequestBindRequest struct {
	ChangeKey          string                         `json:"change_key"`
	RepositoryEntryKey string                         `json:"repository_entry_key,omitempty"`
	ContentVersionKey  ContentVersionKey              `json:"content_version_key,omitempty"`
	Relationship       ChangeRequestRelationship      `json:"relationship"`
	Confirmation       *ChangeRequestBindConfirmation `json:"confirmation,omitempty"`
}

// ChangeRequestBindConfirmation is an explicit user assertion about one
// already-resolved content version. The server still derives the persisted
// confirmation revision and every Session/repository identity.
type ChangeRequestBindConfirmation struct {
	CompleteDelivery  bool              `json:"complete_delivery"`
	ContentVersionKey ContentVersionKey `json:"content_version_key"`
}

type GitEvidenceAuthority string

const (
	GitAuthorityHostedChange  GitEvidenceAuthority = "hosted_change"
	GitAuthorityLocalInterval GitEvidenceAuthority = "local_interval"
	GitAuthorityCommitGraph   GitEvidenceAuthority = "commit_graph"
	GitAuthorityNone          GitEvidenceAuthority = "none"
)

type ChangeRequestAuthorityCoverage string

const ChangeCoverageCompleteDelivery ChangeRequestAuthorityCoverage = "complete_delivery"

type ChangeRequestAuthoritySelection struct {
	LinkID             string                         `json:"link_id"`
	ContentVersionKey  ContentVersionKey              `json:"content_version_key"`
	RootAgentType      string                         `json:"root_agent_type"`
	RootSessionID      string                         `json:"root_session_id"`
	RepositoryEntryKey string                         `json:"repository_entry_key"`
	Coverage           ChangeRequestAuthorityCoverage `json:"coverage"`
}

// SessionGitEvidence covers exactly one attribution root and repository
// binding. A multi-repository Session is represented as multiple entities by
// the API/store layer, never by weakening the binding on this object.
type SessionGitEvidence struct {
	RootAgentType      string                           `json:"root_agent_type"`
	RootSessionID      string                           `json:"root_session_id"`
	RepositoryEntryKey string                           `json:"repository_entry_key"`
	Revision           int64                            `json:"revision"`
	Assessment         GitEvidenceAssessment            `json:"assessment"`
	Provisional        bool                             `json:"provisional"`
	Repository         GitRepositoryBinding             `json:"repository"`
	Origin             *SessionGitOrigin                `json:"origin,omitempty"`
	Baseline           *GitSnapshotSummary              `json:"baseline,omitempty"`
	Final              *GitSnapshotSummary              `json:"final,omitempty"`
	Files              []GitFileChange                  `json:"files"`
	CandidateCommits   []GitCandidateCommit             `json:"candidate_commits"`
	ChangeRequests     []SessionChangeRequestLink       `json:"change_requests"`
	Authority          GitEvidenceAuthority             `json:"authority"`
	AuthoritySelection *ChangeRequestAuthoritySelection `json:"authority_selection,omitempty"`
	Stale              bool                             `json:"stale"`
	GeneratedAt        time.Time                        `json:"generated_at"`
}

// SessionGitEvidenceEnvelope is the top-level Session API contract. Each
// repository entry owns its own authority decision; no global authority can
// accidentally promote a Change Request across repositories.
type SessionGitEvidenceEnvelope struct {
	RootAgentType string                `json:"root_agent_type"`
	RootSessionID string                `json:"root_session_id"`
	Revision      int64                 `json:"revision"`
	Assessment    GitEvidenceAssessment `json:"assessment"`
	Provisional   bool                  `json:"provisional"`
	Stale         bool                  `json:"stale"`
	GeneratedAt   time.Time             `json:"generated_at"`
	Repositories  []SessionGitEvidence  `json:"repositories"`
}
