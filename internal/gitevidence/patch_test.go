package gitevidence

import (
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestRenderPatchesUsesOnlyRetainedEndpointContent(t *testing.T) {
	before := []byte("line one\nold line\n")
	after := []byte("line one\nnew line\n")
	baseline := &Snapshot{Contents: []ContentBlob{{SHA256: "before", Bytes: before}}}
	final := &Snapshot{Contents: []ContentBlob{{SHA256: "after", Bytes: after}}}
	change := NetFileChange{
		Key: "change", DisplayPath: "src/file.go", Status: model.GitFileModified,
		Before:          &SnapshotFile{Present: true, Kind: FileRegular, Mode: "100644", ContentRef: "before", ContentAssessment: model.ExactGitEvidence()},
		After:           &SnapshotFile{Present: true, Kind: FileRegular, Mode: "100755", ContentRef: "after", ContentAssessment: model.ExactGitEvidence()},
		PatchAssessment: model.ExactGitEvidence(),
	}
	patches := RenderPatches(baseline, final, NetDiff{Files: []NetFileChange{change}}, DefaultPatchLimits())
	if len(patches) != 1 || patches[0].Assessment.State != model.GitEvidenceExact || patches[0].Additions != 1 || patches[0].Deletions != 1 {
		t.Fatalf("patch result = %+v", patches)
	}
	text := string(patches[0].Content)
	for _, want := range []string{
		"diff --git a/src/file.go b/src/file.go\n",
		"old mode 100644\nnew mode 100755\n",
		"--- a/src/file.go\n+++ b/src/file.go\n",
		"-old line\n+new line\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("patch missing %q:\n%s", want, text)
		}
	}
}

func TestRenderPatchesDegradesMissingAndOverLimitContent(t *testing.T) {
	missing := NetFileChange{
		Key: "missing", DisplayPath: "missing.txt",
		Before:          &SnapshotFile{Present: true, Kind: FileRegular, ContentRef: "absent", ContentAssessment: model.ExactGitEvidence()},
		After:           &SnapshotFile{Present: true, Kind: FileRegular, ContentRef: "after", ContentAssessment: model.ExactGitEvidence()},
		PatchAssessment: model.ExactGitEvidence(),
	}
	large := NetFileChange{
		Key: "large", DisplayPath: "large.txt",
		Before:          &SnapshotFile{Present: false},
		After:           &SnapshotFile{Present: true, Kind: FileRegular, ContentRef: "large", ContentAssessment: model.ExactGitEvidence()},
		PatchAssessment: model.ExactGitEvidence(),
	}
	limits := DefaultPatchLimits()
	limits.MaxInputBytes = 3
	patches := RenderPatches(
		&Snapshot{},
		&Snapshot{Contents: []ContentBlob{{SHA256: "after", Bytes: []byte("new\n")}, {SHA256: "large", Bytes: []byte("large\n")}}},
		NetDiff{Files: []NetFileChange{missing, large}}, limits,
	)
	if patches[0].Assessment.ReasonCode != model.ReasonSnapshotObjectMissing ||
		patches[1].Assessment.ReasonCode != model.ReasonSnapshotLimitExceeded {
		t.Fatalf("degraded patches = %+v", patches)
	}
}
