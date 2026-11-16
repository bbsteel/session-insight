package dsh

import (
	"context"

	"github.com/bbsteel/session-insight/internal/model"
)

// ReadIndexSnapshotEnvelope keeps event timestamps for display while the
// indexer compares source fingerprints even when those timestamps do not move.
// No stat-only shortcut is supplied: generation replacements or byte rewrites
// may preserve both size and mtime.
func (reader *Reader) ReadIndexSnapshotEnvelope(ctx context.Context, session model.Session) (*model.IndexSnapshotEnvelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := reader.sessionPath(session.ID)
	if err != nil {
		return nil, err
	}
	var fingerprint model.SourceFingerprint
	detail, events, err := readSessionWithFingerprint(path, session.ID, &fingerprint)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	revision := string(fingerprint.Algorithm) + ":" + fingerprint.Digest
	missingOrigin := model.NonExactGitEvidence(model.GitEvidenceMissing, model.ReasonAgentGitFactMissing)
	missingStringFact := model.GitFact[string]{Assessment: missingOrigin, Source: model.GitSourceAgentRecorded, SourceRevision: revision}
	// dsh's header cwd is not a recorded Git worktree identity. Do not infer
	// repository/branch/HEAD facts or session finalization from it or turn ends.
	return &model.IndexSnapshotEnvelope{
		Detail: detail, RenderEvents: events, SourceRevision: revision, SourceFingerprint: fingerprint,
		OriginGit: &model.SessionGitOrigin{
			RepositoryURL: missingStringFact, WorktreePath: missingStringFact,
			Branch: missingStringFact, HeadSHA: missingStringFact,
			DirtyState: model.GitFact[model.GitDirtyState]{Value: model.GitDirtyUnknown, Assessment: missingOrigin, Source: model.GitSourceAgentRecorded, SourceRevision: revision},
		},
		Finalization: model.SessionFinalizationEvidence{
			State: model.SessionFinalizationUnknown, SignalKind: model.SessionSignalNone,
			Assessment:       model.NonExactSessionEvidence(model.SessionEvidenceMissing, model.ReasonSessionStateNotRecorded),
			SignalAssessment: model.NonExactSessionEvidence(model.SessionEvidenceMissing, model.ReasonSessionStateNotRecorded),
		},
	}, nil
}
