package model

import (
	"strings"
	"testing"
	"time"
)

func TestValidateIndexSnapshotEnvelope(t *testing.T) {
	envelope := validIndexSnapshotEnvelope()
	if validation := ValidateIndexSnapshotEnvelope(envelope); !validation.OK() {
		t.Fatalf("valid envelope rejected: %+v", validation.Issues)
	}

	envelope.OriginGit.HeadSHA.SourceRevision = "sha256:" + strings.Repeat("b", 64)
	validation := ValidateIndexSnapshotEnvelope(envelope)
	if !validation.Has(GitIssueInvalidRevision) {
		t.Fatalf("mismatched fact revision was accepted: %+v", validation.Issues)
	}
}

func TestValidateIndexSnapshotEnvelopeKeepsSourceAndLayoutRevisionsDistinct(t *testing.T) {
	envelope := validIndexSnapshotEnvelope()
	envelope.SourceRevision = "7"
	validation := ValidateIndexSnapshotEnvelope(envelope)
	if !validation.Has(GitIssueInvalidRevision) {
		t.Fatalf("layout-shaped revision was accepted as source fingerprint: %+v", validation.Issues)
	}
}

func TestValidateIndexSnapshotEnvelopeRejectsFabricatedDirtyAndFinalized(t *testing.T) {
	envelope := validIndexSnapshotEnvelope()
	envelope.OriginGit.DirtyState.Assessment = ExactGitEvidence()
	envelope.Finalization.State = SessionFinalized
	envelope.Finalization.Assessment = ExactSessionEvidence()
	envelope.Finalization.SignalKind = SessionSignalTurnComplete
	validation := ValidateIndexSnapshotEnvelope(envelope)
	if !validation.Has(GitIssueInvalidAssessment) {
		t.Fatalf("fabricated dirty/finalized state was accepted: %+v", validation.Issues)
	}
}

func validIndexSnapshotEnvelope() *IndexSnapshotEnvelope {
	digest := strings.Repeat("a", 64)
	revision := "sha256:" + digest
	recordedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	exactString := func(value string) GitFact[string] {
		return GitFact[string]{
			Value: value, Assessment: ExactGitEvidence(), Source: GitSourceAgentRecorded,
			RecordedAt: &recordedAt, SourceRevision: revision,
		}
	}
	missing := NonExactGitEvidence(GitEvidenceMissing, ReasonAgentGitFactMissing)
	return &IndexSnapshotEnvelope{
		Detail:            &SessionDetail{Session: Session{ID: "session-1"}},
		RenderEvents:      []RenderEvent{},
		SourceRevision:    revision,
		SourceFingerprint: SourceFingerprint{Algorithm: SourceFingerprintSHA256, Digest: digest, SizeBytes: 42},
		OriginGit: &SessionGitOrigin{
			RepositoryURL: exactString("https://example.test/acme/widgets.git"),
			WorktreePath:  exactString("/workspace/widgets"),
			Branch:        exactString("feature/evidence"),
			HeadSHA:       exactString("0123456789abcdef0123456789abcdef01234567"),
			DirtyState: GitFact[GitDirtyState]{
				Value: GitDirtyUnknown, Assessment: missing, Source: GitSourceAgentRecorded,
				RecordedAt: &recordedAt, SourceRevision: revision,
			},
		},
		Finalization: SessionFinalizationEvidence{
			State:            SessionFinalizationUnknown,
			Assessment:       NonExactSessionEvidence(SessionEvidenceMissing, ReasonTurnMarkerNotSessionFinalization),
			SignalKind:       SessionSignalTurnComplete,
			SignalRecordedAt: &recordedAt,
			SignalAssessment: ExactSessionEvidence(),
		},
	}
}
