package gitevidence

import (
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func testMutationForLink() FileMutationEvidence {
	recorded := time.Date(2026, 8, 11, 8, 0, 0, 0, time.UTC)
	return FileMutationEvidence{
		ID: "mutation:v1:test", RootAgentType: "codex", RootSessionID: "root",
		SourceAgentType: "claude", SourceSessionID: "source",
		BackingAgentType: "claude", BackingSessionID: "backing",
		InvocationID: "child", SourceRevision: "source-revision",
		EventID: "event-edit", ToolCallID: "call-edit", TurnIndex: 4,
		RecordedAt: &recorded, ToolName: "Edit", Operation: MutationEdit,
		Path: "src/file.go", Result: MutationSucceeded,
	}
}

func TestResolveMutationEvidenceLinkMatchingOrder(t *testing.T) {
	mutation := testMutationForLink()
	t.Run("exact IDs", func(t *testing.T) {
		positions := MutationPositionSet{
			SourceRevision: mutation.SourceRevision, PositionsRevision: 7,
			Anchors: []MutationPositionAnchor{{
				Path: mutation.Path, EventID: mutation.EventID, ToolCallID: mutation.ToolCallID,
			}},
		}
		resolution := ResolveMutationEvidenceLink(mutation, mutation.Path, positions)
		assertMutationLink(t, resolution, MutationLinkExact, model.GitEvidenceExact)
		if resolution.Link.EventID != mutation.EventID || resolution.Link.ToolCallID != mutation.ToolCallID || resolution.Link.PositionsRevision != 7 {
			t.Fatalf("exact link=%+v", resolution.Link)
		}
	})

	t.Run("attribution turn and time", func(t *testing.T) {
		turn := mutation.TurnIndex
		recorded := mutation.RecordedAt.Add(time.Second)
		positions := MutationPositionSet{
			SourceRevision: mutation.SourceRevision, PositionsRevision: 8,
			Anchors: []MutationPositionAnchor{{
				Path:          mutation.Path,
				RootAgentType: mutation.RootAgentType, RootSessionID: mutation.RootSessionID,
				SourceAgentType: mutation.SourceAgentType, SourceSessionID: mutation.SourceSessionID,
				BackingAgentType: mutation.BackingAgentType, BackingSessionID: mutation.BackingSessionID,
				InvocationID: mutation.InvocationID, TurnIndex: &turn, RecordedAt: &recorded,
			}},
		}
		resolution := ResolveMutationEvidenceLink(mutation, mutation.Path, positions)
		assertMutationLink(t, resolution, MutationLinkExact, model.GitEvidenceExact)
	})

	t.Run("path only", func(t *testing.T) {
		positions := MutationPositionSet{
			SourceRevision: mutation.SourceRevision, PositionsRevision: 9,
			Anchors: []MutationPositionAnchor{{Path: mutation.Path}},
		}
		resolution := ResolveMutationEvidenceLink(mutation, mutation.Path, positions)
		assertMutationLink(t, resolution, MutationLinkEstimated, model.GitEvidenceEstimated)
		if resolution.Link.Assessment.ReasonCode != model.ReasonSourceMissing {
			t.Fatalf("estimated assessment=%+v", resolution.Link.Assessment)
		}
	})
}

func TestResolveMutationEvidenceLinkReportsStaleAndUnavailable(t *testing.T) {
	mutation := testMutationForLink()
	tests := []struct {
		name      string
		mutation  FileMutationEvidence
		finalPath string
		positions MutationPositionSet
		status    MutationLinkStatus
		reason    MutationLinkReason
	}{
		{
			name: "source revision mismatch", mutation: mutation, finalPath: mutation.Path,
			positions: MutationPositionSet{SourceRevision: "new-source", PositionsRevision: 2},
			status:    MutationLinkStale, reason: MutationLinkReasonSourceRevisionMismatch,
		},
		{
			name: "positions unavailable", mutation: mutation, finalPath: mutation.Path,
			positions: MutationPositionSet{SourceRevision: mutation.SourceRevision},
			status:    MutationLinkUnavailable, reason: MutationLinkReasonPositionsUnavailable,
		},
		{
			name: "invalid final path", mutation: mutation, finalPath: "../outside",
			positions: MutationPositionSet{SourceRevision: mutation.SourceRevision, PositionsRevision: 2},
			status:    MutationLinkUnavailable, reason: MutationLinkReasonInvalidPath,
		},
		{
			name: "no match", mutation: mutation, finalPath: mutation.Path,
			positions: MutationPositionSet{SourceRevision: mutation.SourceRevision, PositionsRevision: 2},
			status:    MutationLinkUnavailable, reason: MutationLinkReasonNoMatch,
		},
		{
			name: "different final path", mutation: mutation, finalPath: "src/other.go",
			positions: MutationPositionSet{SourceRevision: mutation.SourceRevision, PositionsRevision: 2},
			status:    MutationLinkUnavailable, reason: MutationLinkReasonNoMatch,
		},
		{
			name: "ambiguous path only", mutation: mutation, finalPath: mutation.Path,
			positions: MutationPositionSet{SourceRevision: mutation.SourceRevision, PositionsRevision: 2, Anchors: []MutationPositionAnchor{{Path: mutation.Path}, {Path: mutation.Path}}},
			status:    MutationLinkUnavailable, reason: MutationLinkReasonAmbiguous,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolution := ResolveMutationEvidenceLink(test.mutation, test.finalPath, test.positions)
			if resolution.Status != test.status || resolution.Reason != test.reason || resolution.Link != nil {
				t.Fatalf("resolution=%+v want status=%q reason=%q", resolution, test.status, test.reason)
			}
		})
	}
}

func TestResolveMutationEvidenceLinkNeverAttachesNonFinalMutation(t *testing.T) {
	base := testMutationForLink()
	positions := MutationPositionSet{
		SourceRevision: base.SourceRevision, PositionsRevision: 2,
		Anchors: []MutationPositionAnchor{{Path: base.Path, EventID: base.EventID}},
	}
	for _, test := range []struct {
		name       string
		result     MutationResultState
		rolledBack bool
	}{
		{name: "failed", result: MutationFailed},
		{name: "rejected", result: MutationRejected},
		{name: "unknown", result: MutationUnknown},
		{name: "rolled back", result: MutationSucceeded, rolledBack: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			mutation := base
			mutation.Result = test.result
			mutation.RolledBack = test.rolledBack
			resolution := ResolveMutationEvidenceLink(mutation, mutation.Path, positions)
			if resolution.Status != MutationLinkUnavailable || resolution.Reason != MutationLinkReasonMutationNotFinal || resolution.Link != nil {
				t.Fatalf("resolution=%+v", resolution)
			}
		})
	}
}

func assertMutationLink(t *testing.T, resolution MutationLinkResolution, status MutationLinkStatus, evidenceState model.GitEvidenceState) {
	t.Helper()
	if resolution.Status != status || resolution.Reason != "" || resolution.Link == nil {
		t.Fatalf("resolution=%+v want status=%q", resolution, status)
	}
	if resolution.Link.Assessment.State != evidenceState || resolution.Link.SourceRevision == "" ||
		resolution.Link.RootAgentType == "" || resolution.Link.SourceAgentType == "" ||
		resolution.Link.TurnIndex == nil {
		t.Fatalf("link=%+v", resolution.Link)
	}
}
