package model

import (
	"fmt"
	"strings"
)

// ValidateIndexSnapshotEnvelope checks the invariants shared by every Agent
// adapter that implements the authoritative snapshot protocol.
func ValidateIndexSnapshotEnvelope(envelope *IndexSnapshotEnvelope) GitValidation {
	var v GitValidation
	if envelope == nil {
		v.add(GitIssueMissingField, "envelope", "authoritative snapshot envelope is required")
		return v.finish()
	}
	if envelope.Detail == nil {
		v.add(GitIssueMissingField, "detail", "session detail is required")
	}
	if envelope.RenderEvents == nil {
		v.add(GitIssueMissingField, "render_events", "must be an explicit array, not null")
	}
	validateRequired(&v, "source_revision", envelope.SourceRevision)
	validateSourceFingerprint(&v, envelope)
	validateOriginGit(&v, envelope)
	validateFinalizationEvidence(&v, envelope.Finalization)
	return v.finish()
}

func validateSourceFingerprint(v *GitValidation, envelope *IndexSnapshotEnvelope) {
	fingerprint := envelope.SourceFingerprint
	if fingerprint.Algorithm != SourceFingerprintSHA256 {
		v.add(GitIssueInvalidEnum, "source_fingerprint.algorithm", fmt.Sprintf("unknown fingerprint algorithm %q", fingerprint.Algorithm))
	}
	if len(fingerprint.Digest) != 64 {
		v.add(GitIssueInvalidRevision, "source_fingerprint.digest", "sha256 digest must be 64 lowercase hexadecimal characters")
	} else {
		for _, r := range fingerprint.Digest {
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
				v.add(GitIssueInvalidRevision, "source_fingerprint.digest", "sha256 digest must be 64 lowercase hexadecimal characters")
				break
			}
		}
	}
	if fingerprint.SizeBytes < 0 {
		v.add(GitIssueInvalidRevision, "source_fingerprint.size_bytes", "must be non-negative")
	}
	expectedRevision := string(fingerprint.Algorithm) + ":" + fingerprint.Digest
	if envelope.SourceRevision != expectedRevision {
		v.add(GitIssueInvalidRevision, "source_revision", "must identify the exact source fingerprint")
	}
}

func validateOriginGit(v *GitValidation, envelope *IndexSnapshotEnvelope) {
	if envelope.OriginGit == nil {
		v.add(GitIssueMissingField, "origin_git", "origin facts must be explicit, including typed missing facts")
		return
	}
	origin := envelope.OriginGit
	facts := []struct {
		field string
		fact  GitFact[string]
	}{
		{"origin_git.repository_url", origin.RepositoryURL},
		{"origin_git.worktree_path", origin.WorktreePath},
		{"origin_git.branch", origin.Branch},
		{"origin_git.head_sha", origin.HeadSHA},
	}
	for _, entry := range facts {
		validateFact(v, entry.field, entry.fact)
		if entry.fact.Source != GitSourceAgentRecorded {
			v.add(GitIssueInvalidAssessment, entry.field+".source", "adapter origin facts must use agent_recorded source")
		}
		validateOriginFactRevision(v, entry.field, entry.fact.SourceRevision, envelope.SourceRevision)
		if entry.fact.Assessment.State == GitEvidenceExact {
			validateRequired(v, entry.field+".value", entry.fact.Value)
			if entry.fact.RecordedAt == nil {
				v.add(GitIssueInvalidAssessment, entry.field+".recorded_at", "an exact Agent-recorded fact requires its recorded time")
			}
		}
		if (entry.fact.Assessment.State == GitEvidenceMissing || entry.fact.Assessment.State == GitEvidenceUnavailable) && entry.fact.Value != "" {
			v.add(GitIssueInvalidAssessment, entry.field+".value", "missing or unavailable facts must not expose an untrusted raw value")
		}
	}
	validateFact(v, "origin_git.dirty_state", origin.DirtyState)
	if origin.DirtyState.Source != GitSourceAgentRecorded {
		v.add(GitIssueInvalidAssessment, "origin_git.dirty_state.source", "adapter origin facts must use agent_recorded source")
	}
	validateOriginFactRevision(v, "origin_git.dirty_state", origin.DirtyState.SourceRevision, envelope.SourceRevision)

	if origin.RepositoryURL.Value != "" {
		validateSanitizedURL(v, "origin_git.repository_url.value", origin.RepositoryURL.Value, true)
	}
	if origin.HeadSHA.Value != "" {
		validateSHA(v, "origin_git.head_sha.value", origin.HeadSHA.Value, true)
	}
	if origin.WorktreePath.Value != "" {
		path := origin.WorktreePath.Value
		if len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
			v.add(GitIssueInvalidIdentity, "origin_git.worktree_path.value", "must be a bounded single-line path without NUL")
		}
	}
	if !IsKnownGitDirtyState(origin.DirtyState.Value) {
		v.add(GitIssueInvalidEnum, "origin_git.dirty_state.value", fmt.Sprintf("unknown dirty state %q", origin.DirtyState.Value))
	}
	if origin.DirtyState.Assessment.State == GitEvidenceExact && origin.DirtyState.Value == GitDirtyUnknown {
		v.add(GitIssueInvalidAssessment, "origin_git.dirty_state", "unknown dirty state cannot be exact")
	}
	if origin.DirtyState.Assessment.State == GitEvidenceExact && origin.DirtyState.RecordedAt == nil {
		v.add(GitIssueInvalidAssessment, "origin_git.dirty_state.recorded_at", "an exact Agent-recorded fact requires its recorded time")
	}
}

func validateOriginFactRevision(v *GitValidation, field, factRevision, envelopeRevision string) {
	if factRevision == "" {
		v.add(GitIssueInvalidRevision, field+".source_revision", "origin facts, including missing facts, must be tied to the authoritative source revision")
	} else if factRevision != envelopeRevision {
		v.add(GitIssueInvalidRevision, field+".source_revision", "must match the authoritative envelope source revision")
	}
}

func validateFinalizationEvidence(v *GitValidation, evidence SessionFinalizationEvidence) {
	if !IsKnownSessionFinalizationState(evidence.State) {
		v.add(GitIssueInvalidEnum, "finalization.state", fmt.Sprintf("unknown finalization state %q", evidence.State))
	}
	validateSessionAssessment(v, "finalization.assessment", evidence.Assessment)
	if !IsKnownSessionFinalizationSignalKind(evidence.SignalKind) {
		v.add(GitIssueInvalidEnum, "finalization.signal_kind", fmt.Sprintf("unknown finalization signal %q", evidence.SignalKind))
	}
	validateSessionAssessment(v, "finalization.signal_assessment", evidence.SignalAssessment)

	if evidence.SignalKind == SessionSignalNone {
		if evidence.SignalRecordedAt != nil {
			v.add(GitIssueInvalidAssessment, "finalization.signal_recorded_at", "a missing signal cannot have a recorded time")
		}
		if evidence.SignalAssessment.Precision == SessionEvidenceExact {
			v.add(GitIssueInvalidAssessment, "finalization.signal_assessment", "a missing signal cannot be exact")
		}
	} else if evidence.SignalAssessment.Precision == SessionEvidenceExact && evidence.SignalRecordedAt == nil {
		v.add(GitIssueInvalidAssessment, "finalization.signal_recorded_at", "an exact signal requires its recorded time")
	}

	if evidence.Assessment.Precision == SessionEvidenceExact {
		switch evidence.State {
		case SessionFinalized:
			if evidence.SignalKind != SessionSignalFinalized {
				v.add(GitIssueInvalidAssessment, "finalization", "exact finalization requires an Agent session-finalized signal")
			}
		case SessionLive:
			if evidence.SignalKind != SessionSignalLive {
				v.add(GitIssueInvalidAssessment, "finalization", "exact liveness requires an Agent session-live signal")
			}
		case SessionFinalizationUnknown:
			v.add(GitIssueInvalidAssessment, "finalization", "unknown session state cannot be exact")
		}
	}
	if evidence.SignalKind == SessionSignalTurnComplete && evidence.State == SessionFinalized {
		v.add(GitIssueInvalidAssessment, "finalization", "turn completion is not session finalization")
	}
}

func validateSessionAssessment(v *GitValidation, field string, assessment SessionEvidenceAssessment) {
	if !IsKnownSessionEvidencePrecision(assessment.Precision) {
		v.add(GitIssueInvalidAssessment, field+".precision", fmt.Sprintf("unknown session evidence precision %q", assessment.Precision))
		return
	}
	if assessment.Precision == SessionEvidenceExact {
		if assessment.ReasonCode != "" {
			v.add(GitIssueInvalidAssessment, field+".reason_code", "exact session evidence cannot carry a reason")
		}
		return
	}
	if !IsKnownSessionEvidenceReasonCode(assessment.ReasonCode) {
		v.add(GitIssueInvalidAssessment, field+".reason_code", "non-exact session evidence requires a declared reason")
	}
}
