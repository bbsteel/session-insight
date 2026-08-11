package model

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"
)

// GitValidationIssueCode identifies a stable contract violation. Details are
// diagnostic only; callers should branch on Code and Field.
type GitValidationIssueCode string

const (
	GitIssueMissingField      GitValidationIssueCode = "missing_field"
	GitIssueInvalidEnum       GitValidationIssueCode = "invalid_enum"
	GitIssueInvalidAssessment GitValidationIssueCode = "invalid_assessment"
	GitIssueInvalidIdentity   GitValidationIssueCode = "invalid_identity"
	GitIssueInvalidRevision   GitValidationIssueCode = "invalid_revision"
	GitIssueInvalidSHA        GitValidationIssueCode = "invalid_sha"
	GitIssueInvalidURL        GitValidationIssueCode = "invalid_url"
	GitIssueInvalidFile       GitValidationIssueCode = "invalid_file"
	GitIssueInvalidOrdinal    GitValidationIssueCode = "invalid_ordinal"
	GitIssueInvalidLink       GitValidationIssueCode = "invalid_link"
	GitIssueInvalidAuthority  GitValidationIssueCode = "invalid_authority"
	GitIssueDuplicateID       GitValidationIssueCode = "duplicate_id"
)

type GitValidationIssue struct {
	Code   GitValidationIssueCode `json:"code"`
	Field  string                 `json:"field"`
	Detail string                 `json:"detail"`
}

type GitValidation struct {
	Issues []GitValidationIssue `json:"issues"`
}

func (v GitValidation) OK() bool { return len(v.Issues) == 0 }

func (v GitValidation) Has(code GitValidationIssueCode) bool {
	for _, issue := range v.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func (v *GitValidation) add(code GitValidationIssueCode, field, detail string) {
	v.Issues = append(v.Issues, GitValidationIssue{Code: code, Field: field, Detail: detail})
}

func (v *GitValidation) finish() GitValidation {
	sort.SliceStable(v.Issues, func(i, j int) bool {
		if v.Issues[i].Field != v.Issues[j].Field {
			return v.Issues[i].Field < v.Issues[j].Field
		}
		if v.Issues[i].Code != v.Issues[j].Code {
			return v.Issues[i].Code < v.Issues[j].Code
		}
		return v.Issues[i].Detail < v.Issues[j].Detail
	})
	return *v
}

func validateRequired(v *GitValidation, field, value string) {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value || strings.ContainsRune(value, '\x00') {
		v.add(GitIssueMissingField, field, "must be a non-empty, trimmed value without NUL")
	}
}

func validateOpaque(v *GitValidation, field, value string) {
	validateRequired(v, field, value)
	if len(value) > 512 || !utf8.ValidString(value) {
		v.add(GitIssueInvalidIdentity, field, "must be valid UTF-8 and at most 512 bytes")
	}
}

func validateSHA(v *GitValidation, field, sha string, required bool) {
	if sha == "" && !required {
		return
	}
	if len(sha) != 40 && len(sha) != 64 {
		v.add(GitIssueInvalidSHA, field, "must be a 40- or 64-character lowercase hexadecimal object ID")
		return
	}
	for _, r := range sha {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			v.add(GitIssueInvalidSHA, field, "must be a 40- or 64-character lowercase hexadecimal object ID")
			return
		}
	}
}

func validateSanitizedURL(v *GitValidation, field, raw string, required bool) {
	if raw == "" && !required {
		return
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		v.add(GitIssueInvalidURL, field, "must be an absolute HTTP(S) URL")
		return
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		v.add(GitIssueInvalidURL, field, "must not contain userinfo, query, or fragment data")
	}
}

func validateAssessment(v *GitValidation, field string, a GitEvidenceAssessment) {
	if !IsKnownGitEvidenceState(a.State) {
		v.add(GitIssueInvalidAssessment, field+".state", fmt.Sprintf("unknown evidence state %q", a.State))
		return
	}
	if a.State == GitEvidenceExact {
		if a.Reasons == nil {
			v.add(GitIssueInvalidAssessment, field+".reasons", "must be an explicit JSON array, not null")
		}
		if a.ReasonCode != "" || len(a.Reasons) != 0 {
			v.add(GitIssueInvalidAssessment, field, "exact evidence must not carry degradation reasons")
		}
		return
	}
	if !IsKnownGitEvidenceReasonCode(a.ReasonCode) {
		v.add(GitIssueInvalidAssessment, field+".reason_code", "non-exact evidence requires a declared primary reason")
	}
	if a.Reasons == nil {
		v.add(GitIssueInvalidAssessment, field+".reasons", "must be an explicit JSON array, not null")
	}
	if len(a.Reasons) == 0 || a.Reasons[0] != a.ReasonCode {
		v.add(GitIssueInvalidAssessment, field+".reasons", "reasons must start with the primary reason_code")
	}
	seen := make(map[GitEvidenceReasonCode]bool, len(a.Reasons))
	for i, reason := range a.Reasons {
		if !IsKnownGitEvidenceReasonCode(reason) {
			v.add(GitIssueInvalidAssessment, fmt.Sprintf("%s.reasons[%d]", field, i), fmt.Sprintf("unknown reason code %q", reason))
		}
		if seen[reason] {
			v.add(GitIssueInvalidAssessment, fmt.Sprintf("%s.reasons[%d]", field, i), fmt.Sprintf("duplicate reason code %q", reason))
		}
		seen[reason] = true
	}
}

// ValidateGitEvidenceAssessment validates one standalone per-fact precision
// value using the same rules as the aggregate contracts.
func ValidateGitEvidenceAssessment(assessment GitEvidenceAssessment) GitValidation {
	var v GitValidation
	validateAssessment(&v, "assessment", assessment)
	return v.finish()
}

func validateFact[T any](v *GitValidation, field string, fact GitFact[T]) {
	validateAssessment(v, field+".assessment", fact.Assessment)
	if !IsKnownGitEvidenceSource(fact.Source) {
		v.add(GitIssueInvalidEnum, field+".source", fmt.Sprintf("unknown evidence source %q", fact.Source))
	}
	if fact.Assessment.State == GitEvidenceExact && fact.SourceRevision == "" {
		v.add(GitIssueInvalidRevision, field+".source_revision", "exact facts require an authoritative source revision")
	}
}

func validateIdentity(v *GitValidation, field string, identity ChangeRequestIdentity) {
	if !IsKnownChangeProviderKind(identity.Provider) {
		v.add(GitIssueInvalidEnum, field+".provider", fmt.Sprintf("unknown provider %q", identity.Provider))
		return
	}
	if identity.Provider == ChangeProviderGeneric {
		validateOpaque(v, field+".generic_opaque_id", identity.GenericOpaqueID)
		if identity.HostID != "" || identity.TargetRepository != nil || identity.ProviderObjectID != "" {
			v.add(GitIssueInvalidIdentity, field, "generic identity cannot fabricate host, repository, or provider object IDs")
		}
		return
	}
	validateOpaque(v, field+".host_id", identity.HostID)
	validateOpaque(v, field+".provider_object_id", identity.ProviderObjectID)
	if identity.GenericOpaqueID != "" {
		v.add(GitIssueInvalidIdentity, field+".generic_opaque_id", "provider-resolved identity cannot carry a generic opaque ID")
	}
	if identity.TargetRepository == nil {
		v.add(GitIssueMissingField, field+".target_repository", "provider-resolved identity requires a target repository")
		return
	}
	validateOpaque(v, field+".target_repository.host_id", identity.TargetRepository.HostID)
	validateOpaque(v, field+".target_repository.immutable_id", identity.TargetRepository.ImmutableID)
	validateRequired(v, field+".target_repository.slug", identity.TargetRepository.Slug)
	if identity.TargetRepository.HostID != identity.HostID {
		v.add(GitIssueInvalidIdentity, field+".target_repository.host_id", "repository and Change Request host IDs must match")
	}
}

func validateContentVersion(v *GitValidation, field string, content ChangeRequestContentVersion) {
	validateOpaque(v, field+".content_version_key", string(content.Key))
	validateSHA(v, field+".base_ref_sha", content.BaseRefSHA, false)
	validateSHA(v, field+".diff_base_sha", content.DiffBaseSHA, false)
	validateSHA(v, field+".head_sha", content.HeadSHA, false)
	if content.NativeVersion == "" && (content.DiffBaseSHA == "" || content.HeadSHA == "" || content.FileManifestDigest == "") {
		v.add(GitIssueInvalidRevision, field, "a derived content version requires diff-base SHA, head SHA, and file-manifest digest")
	}
}

func validateCompleteness(v *GitValidation, field string, c ChangeRequestCompleteness) {
	validateAssessment(v, field+".metadata", c.Metadata)
	validateAssessment(v, field+".file_set", c.FileSet)
	validateAssessment(v, field+".patches", c.Patches)
	validateAssessment(v, field+".modes", c.Modes)
	validateAssessment(v, field+".commits", c.Commits)
}

func validateEvidenceLink(v *GitValidation, field string, link GitEvidenceLink) {
	validateRequired(v, field+".root_agent_type", link.RootAgentType)
	validateRequired(v, field+".root_session_id", link.RootSessionID)
	validateRequired(v, field+".source_agent_type", link.SourceAgentType)
	validateRequired(v, field+".source_session_id", link.SourceSessionID)
	validateRequired(v, field+".source_revision", link.SourceRevision)
	if link.PositionsRevision < 1 {
		v.add(GitIssueInvalidRevision, field+".positions_revision", "must be >= 1 and is independent of source_revision")
	}
	if (link.BackingAgentType == "") != (link.BackingSessionID == "") {
		v.add(GitIssueInvalidIdentity, field, "backing agent type and Session ID must be present together")
	}
	validateAssessment(v, field+".assessment", link.Assessment)
	if link.EventID == "" && link.ToolCallID == "" && link.TurnIndex == nil && link.RecordedAt == nil {
		v.add(GitIssueMissingField, field, "evidence link requires at least one event, tool-call, turn, or timestamp anchor")
	}
}

func validateFile(v *GitValidation, field string, file GitFileChange) {
	if file.Ordinal < 0 {
		v.add(GitIssueInvalidOrdinal, field+".ordinal", "must be non-negative")
	}
	validateOpaque(v, field+".key", file.Key)
	switch file.Layer {
	case GitFileLayerTree, GitFileLayerIndex, GitFileLayerWorktree, GitFileLayerHosted:
	default:
		v.add(GitIssueInvalidEnum, field+".layer", fmt.Sprintf("unknown file layer %q", file.Layer))
	}
	if file.DisplayPath == "" {
		v.add(GitIssueMissingField, field+".display_path", "safe display path is required")
	}
	switch file.PathEncoding {
	case GitPathUTF8:
		if file.PathBytesB64 != "" {
			v.add(GitIssueInvalidFile, field+".path_bytes_b64", "UTF-8 paths must not carry a raw-byte fallback")
		}
	case GitPathBytesB64:
		if file.PathBytesB64 == "" {
			v.add(GitIssueInvalidFile, field+".path_bytes_b64", "bytes_b64 paths require raw path bytes")
		}
	default:
		v.add(GitIssueInvalidEnum, field+".path_encoding", fmt.Sprintf("unknown path encoding %q", file.PathEncoding))
	}
	switch file.Status {
	case GitFileAdded, GitFileModified, GitFileDeleted, GitFileRenamed, GitFileCopied:
	default:
		v.add(GitIssueInvalidEnum, field+".status", fmt.Sprintf("unknown file status %q", file.Status))
	}
	if file.Status == GitFileRenamed || file.Status == GitFileCopied {
		if file.OldDisplayPath == "" {
			v.add(GitIssueInvalidFile, field+".old_display_path", "rename/copy requires an old display path")
		}
		if file.Layer != GitFileLayerHosted && file.StatusAssessment.State == GitEvidenceExact {
			v.add(GitIssueInvalidFile, field+".status_assessment", "local rename/copy may be estimated only; exact R/C requires provider-native evidence")
		}
	}
	validateAssessment(v, field+".status_assessment", file.StatusAssessment)
	validateAssessment(v, field+".patch_assessment", file.PatchAssessment)
	if file.Evidence == nil {
		v.add(GitIssueInvalidFile, field+".evidence", "must be an explicit JSON array, not null")
	}
	for i, link := range file.Evidence {
		validateEvidenceLink(v, fmt.Sprintf("%s.evidence[%d]", field, i), link)
	}
}

func validateCandidateCommit(v *GitValidation, field string, commit GitCandidateCommit) {
	if commit.Ordinal < 0 {
		v.add(GitIssueInvalidOrdinal, field+".ordinal", "must be non-negative")
	}
	validateSHA(v, field+".sha", commit.SHA, true)
	validateRequired(v, field+".subject", commit.Subject)
	switch commit.Relation {
	case GitCommitDescendant, GitCommitChangeMembership, GitCommitPathOverlap, GitCommitTimeWindow:
	default:
		v.add(GitIssueInvalidEnum, field+".relation", fmt.Sprintf("unknown candidate relation %q", commit.Relation))
	}
	validateAssessment(v, field+".assessment", commit.Assessment)
	if commit.Evidence == nil {
		v.add(GitIssueInvalidFile, field+".evidence", "must be an explicit JSON array, not null")
	}
	for i, link := range commit.Evidence {
		validateEvidenceLink(v, fmt.Sprintf("%s.evidence[%d]", field, i), link)
	}
}

// ValidateChangeRequestIdentity validates one canonical provider or generic
// identity without performing provider resolution.
func ValidateChangeRequestIdentity(identity ChangeRequestIdentity) GitValidation {
	var v GitValidation
	validateIdentity(&v, "identity", identity)
	return v.finish()
}

// ValidateChangeRequestReference validates a locally parsed, sanitized
// reference. It does not claim that provider object or repository IDs exist.
func ValidateChangeRequestReference(ref ChangeRequestReference) GitValidation {
	var v GitValidation
	if !IsKnownChangeProviderKind(ref.Provider) {
		v.add(GitIssueInvalidEnum, "provider", fmt.Sprintf("unknown provider %q", ref.Provider))
	}
	validateSanitizedURL(&v, "display_origin", ref.DisplayOrigin, true)
	validateSanitizedURL(&v, "normalized_url", ref.NormalizedURL, true)
	if ref.Provider != ChangeProviderGeneric {
		validateRequired(&v, "target_repository_slug", ref.TargetRepositorySlug)
		validateRequired(&v, "display_number", ref.DisplayNumber)
	}
	return v.finish()
}

// ValidateChangeRequestSnapshot validates a provider snapshot as a stable,
// version-fixed contract value. It does not infer authority for any Session.
func ValidateChangeRequestSnapshot(snapshot *ChangeRequestSnapshot) GitValidation {
	var v GitValidation
	if snapshot == nil {
		v.add(GitIssueMissingField, "snapshot", "snapshot is required")
		return v.finish()
	}
	validateOpaque(&v, "snapshot_id", snapshot.SnapshotID)
	validateIdentity(&v, "identity", snapshot.Identity)
	validateContentVersion(&v, "content", snapshot.Content)
	validateRequired(&v, "metadata_revision", snapshot.MetadataRevision)
	switch snapshot.Kind {
	case ChangeRequestPullRequest, ChangeRequestMergeRequest, ChangeRequestChange, ChangeRequestCodeReview:
	default:
		v.add(GitIssueInvalidEnum, "kind", fmt.Sprintf("unknown Change Request kind %q", snapshot.Kind))
	}
	switch snapshot.LifecycleState {
	case ChangeLifecycleOpen, ChangeLifecycleMerged, ChangeLifecycleClosed, ChangeLifecycleAbandoned, ChangeLifecycleUnknown:
	default:
		v.add(GitIssueInvalidEnum, "lifecycle_state", fmt.Sprintf("unknown lifecycle state %q", snapshot.LifecycleState))
	}
	validateRequired(&v, "display_number", snapshot.DisplayNumber)
	validateSanitizedURL(&v, "web_url", snapshot.WebURL, true)
	validateCompleteness(&v, "completeness", snapshot.Completeness)
	if snapshot.FetchedAt.IsZero() {
		v.add(GitIssueMissingField, "fetched_at", "provider fetch timestamp is required")
	}
	if snapshot.Files == nil {
		v.add(GitIssueInvalidFile, "files", "must be an explicit JSON array, not null")
	}
	if snapshot.Commits == nil {
		v.add(GitIssueInvalidFile, "commits", "must be an explicit JSON array, not null")
	}
	seenFiles := map[string]bool{}
	for i, file := range snapshot.Files {
		field := fmt.Sprintf("files[%d]", i)
		validateFile(&v, field, file)
		if file.Ordinal != i {
			v.add(GitIssueInvalidOrdinal, field+".ordinal", fmt.Sprintf("must equal stable array ordinal %d", i))
		}
		if seenFiles[file.Key] {
			v.add(GitIssueDuplicateID, field+".key", fmt.Sprintf("duplicate file key %q", file.Key))
		}
		seenFiles[file.Key] = true
	}
	seenCommits := map[string]bool{}
	for i, commit := range snapshot.Commits {
		field := fmt.Sprintf("commits[%d]", i)
		validateCandidateCommit(&v, field, commit)
		if commit.Ordinal != i {
			v.add(GitIssueInvalidOrdinal, field+".ordinal", fmt.Sprintf("must equal stable array ordinal %d", i))
		}
		if seenCommits[commit.SHA] {
			v.add(GitIssueDuplicateID, field+".sha", fmt.Sprintf("duplicate commit SHA %q", commit.SHA))
		}
		seenCommits[commit.SHA] = true
	}
	return v.finish()
}

func validateChangeLink(v *GitValidation, field string, link SessionChangeRequestLink, evidence *SessionGitEvidence) {
	if link.Ordinal < 0 {
		v.add(GitIssueInvalidOrdinal, field+".ordinal", "must be non-negative")
	}
	validateOpaque(v, field+".link_id", link.LinkID)
	validateRequired(v, field+".root_agent_type", link.RootAgentType)
	validateRequired(v, field+".root_session_id", link.RootSessionID)
	validateRequired(v, field+".source_agent_type", link.SourceAgentType)
	validateRequired(v, field+".source_session_id", link.SourceSessionID)
	if link.CollaborationRevision < 1 {
		v.add(GitIssueInvalidRevision, field+".collaboration_revision", "must be >= 1")
	}
	validateIdentity(v, field+".change", link.Change)
	switch link.Relationship {
	case ChangeRelationshipExclusive, ChangeRelationshipContributing, ChangeRelationshipRelated:
	default:
		v.add(GitIssueInvalidEnum, field+".relationship", fmt.Sprintf("unknown relationship %q", link.Relationship))
	}
	switch link.Method {
	case ChangeLinkExplicit, ChangeLinkAgentNative, ChangeLinkURLMention, ChangeLinkHeadSHA, ChangeLinkCommitMembership, ChangeLinkBranch:
	default:
		v.add(GitIssueInvalidEnum, field+".method", fmt.Sprintf("unknown link method %q", link.Method))
	}
	validateAssessment(v, field+".assessment", link.Assessment)
	switch link.ConfirmationSource {
	case ChangeConfirmationNone, ChangeConfirmationUser:
	default:
		v.add(GitIssueInvalidEnum, field+".confirmation_source", fmt.Sprintf("unknown confirmation source %q", link.ConfirmationSource))
	}
	if link.RootAgentType != evidence.RootAgentType || link.RootSessionID != evidence.RootSessionID {
		v.add(GitIssueInvalidLink, field, "link attribution root must match its SessionGitEvidence owner")
	}
	if link.Relationship == ChangeRelationshipExclusive || link.Relationship == ChangeRelationshipContributing {
		if link.RepositoryEntryKey == "" || link.RepositoryEntryKey != evidence.RepositoryEntryKey {
			v.add(GitIssueInvalidLink, field+".repository_entry_key", "exclusive/contributing link must match the evidence repository entry")
		}
		if link.ContentVersionKey == "" {
			v.add(GitIssueInvalidRevision, field+".content_version_key", "exclusive/contributing link must fix a content version")
		}
	}
	if link.Relationship == ChangeRelationshipExclusive {
		if link.Method != ChangeLinkExplicit || link.ConfirmationSource != ChangeConfirmationUser || link.ConfirmationRevision == "" {
			v.add(GitIssueInvalidLink, field, "exclusive authority requires explicit user confirmation tied to a confirmation revision")
		}
	} else if link.ConfirmationSource == ChangeConfirmationUser && link.ConfirmationRevision == "" {
		v.add(GitIssueInvalidRevision, field+".confirmation_revision", "user confirmation requires a confirmation revision")
	}
	if link.Evidence == nil {
		v.add(GitIssueInvalidLink, field+".evidence", "must be an explicit JSON array, not null")
	}
	for i, anchor := range link.Evidence {
		validateEvidenceLink(v, fmt.Sprintf("%s.evidence[%d]", field, i), anchor)
	}
}

// ValidateChangeRequestBindRequest validates only client-writable binding
// fields. Server-derived root, collaboration and worktree facts do not exist
// on this DTO and therefore cannot be forged by the client.
func ValidateChangeRequestBindRequest(request ChangeRequestBindRequest) GitValidation {
	var v GitValidation
	validateIdentity(&v, "change", request.Change)
	switch request.Relationship {
	case ChangeRelationshipExclusive, ChangeRelationshipContributing, ChangeRelationshipRelated:
	default:
		v.add(GitIssueInvalidEnum, "relationship", fmt.Sprintf("unknown relationship %q", request.Relationship))
	}
	if request.Relationship == ChangeRelationshipExclusive || request.Relationship == ChangeRelationshipContributing {
		validateOpaque(&v, "repository_entry_key", request.RepositoryEntryKey)
		validateOpaque(&v, "content_version_key", string(request.ContentVersionKey))
	}
	if request.Change.Provider == ChangeProviderGeneric && request.Relationship != ChangeRelationshipRelated {
		v.add(GitIssueInvalidLink, "relationship", "generic unresolved Change Requests may be related only")
	}
	if request.Relationship == ChangeRelationshipExclusive {
		if !request.ConfirmExclusive || strings.TrimSpace(request.ConfirmationRevision) == "" {
			v.add(GitIssueInvalidLink, "confirm_exclusive", "exclusive binding requires explicit confirmation tied to a confirmation revision")
		}
	} else if request.ConfirmExclusive || request.ConfirmationRevision != "" {
		v.add(GitIssueInvalidLink, "confirm_exclusive", "exclusive confirmation fields are valid only for an exclusive relationship")
	}
	return v.finish()
}

// ValidateSessionGitEvidence enforces root/repository ownership, version-fixed
// exclusive links and the authority-selection invariants without consulting a
// database or provider.
func ValidateSessionGitEvidence(evidence *SessionGitEvidence) GitValidation {
	var v GitValidation
	if evidence == nil {
		v.add(GitIssueMissingField, "evidence", "Session Git evidence is required")
		return v.finish()
	}
	validateRequired(&v, "root_agent_type", evidence.RootAgentType)
	validateRequired(&v, "root_session_id", evidence.RootSessionID)
	validateOpaque(&v, "repository_entry_key", evidence.RepositoryEntryKey)
	if evidence.Revision < 1 {
		v.add(GitIssueInvalidRevision, "revision", "must be >= 1")
	}
	validateAssessment(&v, "assessment", evidence.Assessment)
	validateOpaque(&v, "repository.repository_entry_key", evidence.Repository.RepositoryEntryKey)
	if evidence.Repository.RepositoryEntryKey != evidence.RepositoryEntryKey {
		v.add(GitIssueInvalidIdentity, "repository.repository_entry_key", "repository key must match repository_entry_key")
	}
	validateRequired(&v, "repository.worktree_root", evidence.Repository.WorktreeRoot)
	validateOpaque(&v, "repository.common_root_id", evidence.Repository.CommonRootID)
	validateOpaque(&v, "repository.worktree_id", evidence.Repository.WorktreeID)
	validateSHA(&v, "repository.head_sha", evidence.Repository.HeadSHA, false)
	validateAssessment(&v, "repository.assessment", evidence.Repository.Assessment)
	if evidence.Origin != nil {
		validateFact(&v, "origin.repository_url", evidence.Origin.RepositoryURL)
		validateFact(&v, "origin.worktree_path", evidence.Origin.WorktreePath)
		validateFact(&v, "origin.branch", evidence.Origin.Branch)
		validateFact(&v, "origin.head_sha", evidence.Origin.HeadSHA)
		validateFact(&v, "origin.dirty_state", evidence.Origin.DirtyState)
		if evidence.Origin.DirtyState.Value != "" && !IsKnownGitDirtyState(evidence.Origin.DirtyState.Value) {
			v.add(GitIssueInvalidEnum, "origin.dirty_state.value", fmt.Sprintf("unknown dirty state %q", evidence.Origin.DirtyState.Value))
		}
		if evidence.Origin.HeadSHA.Value != "" {
			validateSHA(&v, "origin.head_sha.value", evidence.Origin.HeadSHA.Value, false)
		}
	}
	validateSnapshot := func(field string, snapshot *GitSnapshotSummary, expected GitSnapshotKind) {
		if snapshot == nil {
			return
		}
		validateOpaque(&v, field+".snapshot_id", snapshot.SnapshotID)
		if snapshot.Kind != expected {
			v.add(GitIssueInvalidEnum, field+".kind", fmt.Sprintf("must be %q", expected))
		}
		validateSHA(&v, field+".head_sha", snapshot.HeadSHA, false)
		validateRequired(&v, field+".source_revision", snapshot.SourceRevision)
		if snapshot.CaptureStartedAt.IsZero() || snapshot.CaptureEndedAt.IsZero() || snapshot.CaptureEndedAt.Before(snapshot.CaptureStartedAt) {
			v.add(GitIssueInvalidRevision, field, "requires a non-negative capture window")
		}
		validateAssessment(&v, field+".assessment", snapshot.Assessment)
	}
	validateSnapshot("baseline", evidence.Baseline, GitSnapshotBaseline)
	validateSnapshot("final", evidence.Final, GitSnapshotFinal)
	if evidence.Files == nil {
		v.add(GitIssueInvalidFile, "files", "must be an explicit JSON array, not null")
	}
	if evidence.CandidateCommits == nil {
		v.add(GitIssueInvalidFile, "candidate_commits", "must be an explicit JSON array, not null")
	}
	if evidence.ChangeRequests == nil {
		v.add(GitIssueInvalidLink, "change_requests", "must be an explicit JSON array, not null")
	}
	seenFiles := map[string]bool{}
	for i, file := range evidence.Files {
		field := fmt.Sprintf("files[%d]", i)
		validateFile(&v, field, file)
		if file.Ordinal != i {
			v.add(GitIssueInvalidOrdinal, field+".ordinal", fmt.Sprintf("must equal stable array ordinal %d", i))
		}
		if seenFiles[file.Key] {
			v.add(GitIssueDuplicateID, field+".key", fmt.Sprintf("duplicate file key %q", file.Key))
		}
		seenFiles[file.Key] = true
	}
	for i, commit := range evidence.CandidateCommits {
		field := fmt.Sprintf("candidate_commits[%d]", i)
		validateCandidateCommit(&v, field, commit)
		if commit.Ordinal != i {
			v.add(GitIssueInvalidOrdinal, field+".ordinal", fmt.Sprintf("must equal stable array ordinal %d", i))
		}
	}
	links := make(map[string]SessionChangeRequestLink, len(evidence.ChangeRequests))
	exclusiveCount := 0
	for i, link := range evidence.ChangeRequests {
		field := fmt.Sprintf("change_requests[%d]", i)
		validateChangeLink(&v, field, link, evidence)
		if link.Ordinal != i {
			v.add(GitIssueInvalidOrdinal, field+".ordinal", fmt.Sprintf("must equal stable array ordinal %d", i))
		}
		if _, exists := links[link.LinkID]; exists {
			v.add(GitIssueDuplicateID, field+".link_id", fmt.Sprintf("duplicate link ID %q", link.LinkID))
		}
		links[link.LinkID] = link
		if link.Relationship == ChangeRelationshipExclusive {
			exclusiveCount++
		}
	}
	if exclusiveCount > 1 {
		v.add(GitIssueInvalidAuthority, "change_requests", "multiple exclusive links for one repository binding are ambiguous")
	}
	switch evidence.Authority {
	case GitAuthorityHostedChange:
		if evidence.AuthoritySelection == nil {
			v.add(GitIssueInvalidAuthority, "authority_selection", "hosted_change authority requires a selection")
			break
		}
		selection := evidence.AuthoritySelection
		selected, ok := links[selection.LinkID]
		if !ok || selected.Relationship != ChangeRelationshipExclusive || selected.ContentVersionKey != selection.ContentVersionKey {
			v.add(GitIssueInvalidAuthority, "authority_selection", "selection must reference the matching fixed version of an exclusive link")
		}
		if selection.RootAgentType != evidence.RootAgentType || selection.RootSessionID != evidence.RootSessionID || selection.RepositoryEntryKey != evidence.RepositoryEntryKey {
			v.add(GitIssueInvalidAuthority, "authority_selection", "selection root and repository binding must match the evidence owner")
		}
		if selection.Coverage != ChangeCoverageCompleteDelivery {
			v.add(GitIssueInvalidAuthority, "authority_selection.coverage", "hosted authority requires complete_delivery coverage")
		}
	case GitAuthorityLocalInterval:
		if evidence.AuthoritySelection != nil {
			v.add(GitIssueInvalidAuthority, "authority_selection", "local_interval authority cannot select a Change Request")
		}
		if evidence.Baseline == nil || evidence.Final == nil {
			v.add(GitIssueInvalidAuthority, "authority", "local_interval authority requires baseline and final snapshots")
		}
	case GitAuthorityCommitGraph:
		if evidence.AuthoritySelection != nil {
			v.add(GitIssueInvalidAuthority, "authority_selection", "commit_graph authority cannot select a Change Request")
		}
		if len(evidence.CandidateCommits) == 0 {
			v.add(GitIssueInvalidAuthority, "candidate_commits", "commit_graph authority requires at least one candidate")
		}
	case GitAuthorityNone:
		if evidence.AuthoritySelection != nil {
			v.add(GitIssueInvalidAuthority, "authority_selection", "none authority cannot select a Change Request")
		}
	default:
		v.add(GitIssueInvalidEnum, "authority", fmt.Sprintf("unknown authority %q", evidence.Authority))
	}
	if evidence.GeneratedAt.IsZero() {
		v.add(GitIssueMissingField, "generated_at", "generation timestamp is required")
	}
	return v.finish()
}

// ValidateSessionGitEvidenceEnvelope validates the top-level repositories[]
// response and rejects cross-root or duplicate opaque repository entries.
func ValidateSessionGitEvidenceEnvelope(envelope *SessionGitEvidenceEnvelope) GitValidation {
	var v GitValidation
	if envelope == nil {
		v.add(GitIssueMissingField, "envelope", "Session Git evidence envelope is required")
		return v.finish()
	}
	validateRequired(&v, "root_agent_type", envelope.RootAgentType)
	validateRequired(&v, "root_session_id", envelope.RootSessionID)
	if envelope.Repositories == nil {
		v.add(GitIssueInvalidIdentity, "repositories", "must be an explicit JSON array, not null")
	}
	seen := map[string]bool{}
	for i := range envelope.Repositories {
		repository := &envelope.Repositories[i]
		field := fmt.Sprintf("repositories[%d]", i)
		if repository.RootAgentType != envelope.RootAgentType || repository.RootSessionID != envelope.RootSessionID {
			v.add(GitIssueInvalidIdentity, field, "repository entry attribution root must match the envelope")
		}
		if seen[repository.RepositoryEntryKey] {
			v.add(GitIssueDuplicateID, field+".repository_entry_key", fmt.Sprintf("duplicate repository entry key %q", repository.RepositoryEntryKey))
		}
		seen[repository.RepositoryEntryKey] = true
		for _, issue := range ValidateSessionGitEvidence(repository).Issues {
			v.add(issue.Code, field+"."+issue.Field, issue.Detail)
		}
	}
	return v.finish()
}
