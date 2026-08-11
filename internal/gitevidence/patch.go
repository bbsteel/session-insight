package gitevidence

import (
	"bytes"
	"fmt"
	"unicode/utf8"

	diff "github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"

	"github.com/bbsteel/session-insight/internal/model"
)

const (
	hardPatchFileLimit   = 500
	hardPatchInputBytes  = 1 << 20
	hardPatchInputLines  = 5000
	hardPatchOutputBytes = 1 << 20
	hardPatchTotalBytes  = 20 << 20
)

// PatchLimits bounds the pure in-memory diff surface. Production defaults
// match source-content retention; tests may lower but never expand the hard
// ceilings above.
type PatchLimits struct {
	MaxFiles       int
	MaxInputBytes  int
	MaxInputLines  int
	MaxOutputBytes int
	MaxTotalBytes  int
}

func DefaultPatchLimits() PatchLimits {
	return PatchLimits{
		MaxFiles: hardPatchFileLimit, MaxInputBytes: hardPatchInputBytes,
		MaxInputLines: hardPatchInputLines, MaxOutputBytes: hardPatchOutputBytes,
		MaxTotalBytes: hardPatchTotalBytes,
	}
}

// RenderedPatch is aligned with one NetFileChange through ChangeKey. Content
// is populated only when Assessment is exact; callers must persist both as
// one publication step before exposing the patch through an API.
type RenderedPatch struct {
	ChangeKey  string
	Content    []byte
	Additions  int
	Deletions  int
	Assessment model.GitEvidenceAssessment
}

// RenderPatches builds deterministic unified patches only from bytes retained
// in the immutable endpoint snapshots. It never re-reads the worktree or Git
// object database, so a later checkout cannot change the published result.
func RenderPatches(baseline, final *Snapshot, net NetDiff, limits PatchLimits) []RenderedPatch {
	results := make([]RenderedPatch, 0, len(net.Files))
	if !validPatchLimits(limits) || len(net.Files) > limits.MaxFiles || baseline == nil || final == nil {
		for _, change := range net.Files {
			results = append(results, unavailablePatch(change.Key, model.ReasonSnapshotLimitExceeded))
		}
		return results
	}
	beforeContent := snapshotContentIndex(baseline)
	afterContent := snapshotContentIndex(final)
	totalBytes := 0
	for _, change := range net.Files {
		if patchChangeBinary(change) {
			results = append(results, unavailablePatch(change.Key, model.ReasonBinaryPatchUnavailable))
			continue
		}
		if patchChangeSubmodule(change) {
			results = append(results, unavailablePatch(change.Key, model.ReasonSubmoduleNotExpanded))
			continue
		}
		if change.PatchAssessment.State != model.GitEvidenceExact {
			results = append(results, RenderedPatch{ChangeKey: change.Key, Assessment: change.PatchAssessment})
			continue
		}
		before, beforeOK := patchEndpoint(change.Before, beforeContent)
		after, afterOK := patchEndpoint(change.After, afterContent)
		if !beforeOK || !afterOK {
			results = append(results, unavailablePatch(change.Key, model.ReasonSnapshotObjectMissing))
			continue
		}
		if len(before) > limits.MaxInputBytes || len(after) > limits.MaxInputBytes ||
			lineCount(before) > limits.MaxInputLines || lineCount(after) > limits.MaxInputLines {
			results = append(results, unavailablePatch(change.Key, model.ReasonSnapshotLimitExceeded))
			continue
		}
		if !utf8.Valid(before) || !utf8.Valid(after) {
			results = append(results, unavailablePatch(change.Key, model.ReasonBinaryPatchUnavailable))
			continue
		}
		content, additions, deletions := renderUnifiedPatch(change, string(before), string(after))
		if len(content) > limits.MaxOutputBytes || totalBytes+len(content) > limits.MaxTotalBytes {
			results = append(results, unavailablePatch(change.Key, model.ReasonSnapshotLimitExceeded))
			continue
		}
		totalBytes += len(content)
		results = append(results, RenderedPatch{
			ChangeKey: change.Key, Content: content, Additions: additions, Deletions: deletions,
			Assessment: model.ExactGitEvidence(),
		})
	}
	return results
}

func patchChangeBinary(change NetFileChange) bool {
	return change.Before != nil && change.Before.Binary || change.After != nil && change.After.Binary
}

func patchChangeSubmodule(change NetFileChange) bool {
	return change.Before != nil && change.Before.Kind == FileGitlink ||
		change.After != nil && change.After.Kind == FileGitlink
}

func validPatchLimits(limits PatchLimits) bool {
	return limits.MaxFiles > 0 && limits.MaxFiles <= hardPatchFileLimit &&
		limits.MaxInputBytes > 0 && limits.MaxInputBytes <= hardPatchInputBytes &&
		limits.MaxInputLines > 0 && limits.MaxInputLines <= hardPatchInputLines &&
		limits.MaxOutputBytes > 0 && limits.MaxOutputBytes <= hardPatchOutputBytes &&
		limits.MaxTotalBytes > 0 && limits.MaxTotalBytes <= hardPatchTotalBytes
}

func snapshotContentIndex(snapshot *Snapshot) map[string][]byte {
	contents := make(map[string][]byte, len(snapshot.Contents))
	for _, content := range snapshot.Contents {
		contents[content.SHA256] = content.Bytes
	}
	return contents
}

func patchEndpoint(file *SnapshotFile, contents map[string][]byte) ([]byte, bool) {
	if file == nil || !file.Present {
		return []byte{}, true
	}
	if file.Kind != FileRegular && file.Kind != FileSymlink {
		return nil, false
	}
	if file.Binary || file.ContentAssessment.State != model.GitEvidenceExact || file.ContentRef == "" {
		return nil, false
	}
	content, ok := contents[file.ContentRef]
	if !ok {
		return nil, false
	}
	return content, true
}

func renderUnifiedPatch(change NetFileChange, before, after string) ([]byte, int, int) {
	displayPath := change.DisplayPath
	from, to := "a/"+displayPath, "b/"+displayPath
	if change.Before == nil || !change.Before.Present {
		from = "/dev/null"
	}
	if change.After == nil || !change.After.Present {
		to = "/dev/null"
	}
	edits := myers.ComputeEdits(span.URIFromPath(displayPath), before, after)
	unified := diff.ToUnified(from, to, before, edits)

	var output bytes.Buffer
	fmt.Fprintf(&output, "diff --git a/%s b/%s\n", displayPath, displayPath)
	oldMode, newMode := "", ""
	if change.Before != nil && change.Before.Present {
		oldMode = change.Before.Mode
	}
	if change.After != nil && change.After.Present {
		newMode = change.After.Mode
	}
	switch {
	case oldMode == "" && newMode != "":
		fmt.Fprintf(&output, "new file mode %s\n", newMode)
	case oldMode != "" && newMode == "":
		fmt.Fprintf(&output, "deleted file mode %s\n", oldMode)
	case oldMode != newMode:
		fmt.Fprintf(&output, "old mode %s\nnew mode %s\n", oldMode, newMode)
	}
	if len(unified.Hunks) != 0 {
		fmt.Fprint(&output, unified)
	}
	additions, deletions := 0, 0
	for _, hunk := range unified.Hunks {
		for _, line := range hunk.Lines {
			switch line.Kind {
			case diff.Insert:
				additions++
			case diff.Delete:
				deletions++
			}
		}
	}
	return output.Bytes(), additions, deletions
}

func lineCount(content []byte) int {
	if len(content) == 0 {
		return 0
	}
	lines := bytes.Count(content, []byte{'\n'})
	if content[len(content)-1] != '\n' {
		lines++
	}
	return lines
}

func unavailablePatch(key string, reason model.GitEvidenceReasonCode) RenderedPatch {
	return RenderedPatch{
		ChangeKey:  key,
		Assessment: model.NonExactGitEvidence(model.GitEvidenceUnavailable, reason),
	}
}
