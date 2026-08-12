package adaptertest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

// AuthoritativeIndexSnapshotReader structurally mirrors the production reader
// contract without importing the parent reader package (which would cycle).
type AuthoritativeIndexSnapshotReader interface {
	ReadIndexSnapshotEnvelope(context.Context, model.Session) (*model.IndexSnapshotEnvelope, error)
}

type IndexSnapshotEnvelopeExpect struct {
	SessionID          string
	ForbiddenFragments []string
}

// AssertIndexSnapshotEnvelope runs the shared invariants for adapters that
// declare the authoritative envelope. Agent-specific tests still assert the
// native field mapping and fixture values.
func AssertIndexSnapshotEnvelope(t *testing.T, r Reader, expect IndexSnapshotEnvelopeExpect) *model.IndexSnapshotEnvelope {
	t.Helper()
	provider, ok := r.(AuthoritativeIndexSnapshotReader)
	if !ok {
		t.Fatalf("reader %T does not implement AuthoritativeIndexSnapshotReader", r)
	}
	envelope, err := provider.ReadIndexSnapshotEnvelope(context.Background(), model.Session{ID: expect.SessionID})
	if err != nil {
		t.Fatalf("ReadIndexSnapshotEnvelope(%q): %v", expect.SessionID, err)
	}
	if validation := model.ValidateIndexSnapshotEnvelope(envelope); !validation.OK() {
		t.Fatalf("invalid authoritative envelope: %+v", validation.Issues)
	}
	if envelope.Detail.ID != expect.SessionID {
		t.Fatalf("detail session id=%q want %q", envelope.Detail.ID, expect.SessionID)
	}
	if !strings.HasPrefix(envelope.SourceRevision, string(model.SourceFingerprintSHA256)+":") {
		t.Fatalf("source revision %q is not an authoritative sha256 fingerprint", envelope.SourceRevision)
	}
	for field, fact := range map[string]model.GitFact[string]{
		"repository_url": envelope.OriginGit.RepositoryURL,
		"worktree_path":  envelope.OriginGit.WorktreePath,
		"branch":         envelope.OriginGit.Branch,
		"head_sha":       envelope.OriginGit.HeadSHA,
	} {
		if fact.Source != model.GitSourceAgentRecorded {
			t.Errorf("%s source=%q want agent_recorded", field, fact.Source)
		}
		if fact.SourceRevision != envelope.SourceRevision {
			t.Errorf("%s source revision=%q want %q", field, fact.SourceRevision, envelope.SourceRevision)
		}
	}
	dirty := envelope.OriginGit.DirtyState
	if dirty.Source != model.GitSourceAgentRecorded || dirty.SourceRevision != envelope.SourceRevision {
		t.Errorf("dirty fact source/revision=%q/%q", dirty.Source, dirty.SourceRevision)
	}
	if dirty.Value == model.GitDirtyUnknown && dirty.Assessment.State == model.GitEvidenceExact {
		t.Error("unknown dirty state must not be fabricated as exact")
	}
	if envelope.Finalization.State == model.SessionFinalized && envelope.Finalization.SignalKind != model.SessionSignalFinalized {
		t.Error("non-session signal was promoted to finalized")
	}
	if envelope.Finalization.State == model.SessionLive && envelope.Finalization.SignalKind != model.SessionSignalLive {
		t.Error("non-session signal was promoted to live")
	}

	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range expect.ForbiddenFragments {
		if fragment != "" && strings.Contains(string(encoded), fragment) {
			t.Errorf("authoritative envelope leaked forbidden fixture fragment %q", fragment)
		}
	}
	return envelope
}
