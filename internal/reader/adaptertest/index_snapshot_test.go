package adaptertest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestAuthoritativeEnvelopeValidatorRejectsTurnCompletionAsFinalization(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	digest := "a1" + strings.Repeat("00", 31)
	revision := "sha256:" + digest
	missing := model.NonExactGitEvidence(model.GitEvidenceMissing, model.ReasonAgentGitFactMissing)
	stringFact := func() model.GitFact[string] {
		return model.GitFact[string]{Source: model.GitSourceAgentRecorded, SourceRevision: revision, Assessment: missing}
	}
	fake := &envelopeFake{envelope: &model.IndexSnapshotEnvelope{
		Detail:            &model.SessionDetail{Session: model.Session{ID: "s1"}},
		RenderEvents:      []model.RenderEvent{},
		SourceRevision:    revision,
		SourceFingerprint: model.SourceFingerprint{Algorithm: model.SourceFingerprintSHA256, Digest: digest},
		OriginGit: &model.SessionGitOrigin{
			RepositoryURL: stringFact(), WorktreePath: stringFact(), Branch: stringFact(), HeadSHA: stringFact(),
			DirtyState: model.GitFact[model.GitDirtyState]{Value: model.GitDirtyUnknown, Source: model.GitSourceAgentRecorded, SourceRevision: revision, Assessment: missing},
		},
		Finalization: model.SessionFinalizationEvidence{
			State: model.SessionFinalized, Assessment: model.ExactSessionEvidence(),
			SignalKind: model.SessionSignalTurnComplete, SignalRecordedAt: &now, SignalAssessment: model.ExactSessionEvidence(),
		},
	}}
	validation := model.ValidateIndexSnapshotEnvelope(fake.envelope)
	if validation.OK() {
		t.Fatal("turn completion must not validate as exact session finalization")
	}
}

type envelopeFake struct {
	completeFake
	envelope *model.IndexSnapshotEnvelope
}

func (f *envelopeFake) ReadIndexSnapshotEnvelope(context.Context, model.Session) (*model.IndexSnapshotEnvelope, error) {
	return f.envelope, nil
}
