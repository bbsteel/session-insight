package gitevidence

import (
	"bytes"
	"sort"

	"github.com/bbsteel/session-insight/internal/model"
)

// DiffSnapshots computes the effective worktree interval. It intentionally
// performs no rename inference: a local move is one deletion plus one add.
// Because the baseline is a complete effective worktree snapshot, dirty-start
// files disappear from the result unless their interval state changed.
func DiffSnapshots(baseline, final *Snapshot) (NetDiff, error) {
	if baseline == nil || final == nil || baseline.Kind != model.GitSnapshotBaseline ||
		(final.Kind != model.GitSnapshotCheckpoint && final.Kind != model.GitSnapshotFinal) {
		return NetDiff{}, &CaptureError{Code: CaptureErrorInvalidRequest}
	}
	if baseline.WorktreeID == "" || baseline.WorktreeID != final.WorktreeID {
		err := &CaptureError{Code: CaptureErrorIdentityChanged}
		return NetDiff{
			BaselineManifest: baseline.ManifestDigest, FinalManifest: final.ManifestDigest,
			Assessment: model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonWorktreeIdentityChanged),
		}, err
	}

	before := make(map[string]SnapshotFile, len(baseline.Files))
	after := make(map[string]SnapshotFile, len(final.Files))
	paths := make(map[string][]byte, len(baseline.Files)+len(final.Files))
	for _, file := range baseline.Files {
		key := string(file.PathBytes)
		before[key], paths[key] = file, file.PathBytes
	}
	for _, file := range final.Files {
		key := string(file.PathBytes)
		after[key], paths[key] = file, file.PathBytes
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Slice(ordered, func(i, j int) bool { return bytes.Compare(paths[ordered[i]], paths[ordered[j]]) < 0 })

	diff := NetDiff{
		BaselineManifest: baseline.ManifestDigest, FinalManifest: final.ManifestDigest,
		Assessment: model.ExactGitEvidence(), Files: []NetFileChange{},
	}
	var uncertainReasons []model.GitEvidenceReasonCode
	for _, path := range ordered {
		oldFile, oldExists := before[path]
		newFile, newExists := after[path]
		oldPresent := oldExists && oldFile.Present
		newPresent := newExists && newFile.Present
		if !oldPresent && !newPresent {
			continue
		}

		var status model.GitFileStatus
		statusAssessment := model.ExactGitEvidence()
		switch {
		case !oldPresent && newPresent:
			status = model.GitFileAdded
		case oldPresent && !newPresent:
			status = model.GitFileDeleted
		default:
			equal, exact, reason := equivalentSnapshotFiles(oldFile, newFile)
			if equal && exact {
				continue
			}
			status = model.GitFileModified
			if !exact {
				statusAssessment = model.NonExactGitEvidence(model.GitEvidenceEstimated, reason)
				uncertainReasons = appendUniqueReason(uncertainReasons, reason)
			}
		}

		selected := newFile
		if !newPresent {
			selected = oldFile
		}
		change := NetFileChange{
			Ordinal: len(diff.Files), Key: selected.Key,
			PathBytes: append([]byte(nil), selected.PathBytes...), DisplayPath: selected.DisplayPath,
			PathEncoding: selected.PathEncoding, Status: status, StatusAssessment: statusAssessment,
			PatchAssessment: patchAssessmentForChange(oldFile, oldPresent, newFile, newPresent),
		}
		if oldExists {
			copy := cloneSnapshotFile(oldFile)
			change.Before = &copy
		}
		if newExists {
			copy := cloneSnapshotFile(newFile)
			change.After = &copy
		}
		diff.Files = append(diff.Files, change)
	}
	if len(uncertainReasons) != 0 {
		diff.Assessment = model.NonExactGitEvidence(model.GitEvidenceEstimated, uncertainReasons[0], uncertainReasons[1:]...)
	}
	return diff, nil
}

func equivalentSnapshotFiles(before, after SnapshotFile) (equal, exact bool, reason model.GitEvidenceReasonCode) {
	if before.Kind != after.Kind || before.Mode != after.Mode {
		return false, true, ""
	}
	switch before.Kind {
	case FileRegular, FileSymlink:
		if before.ContentHash != "" && after.ContentHash != "" {
			return before.ContentHash == after.ContentHash, true, ""
		}
	case FileGitlink:
		if before.GitOID != "" && after.GitOID != "" {
			return before.GitOID == after.GitOID, true, ""
		}
	case FileMissing:
		return true, true, ""
	}
	return false, false, uncertainFileReason(before, after)
}

func uncertainFileReason(files ...SnapshotFile) model.GitEvidenceReasonCode {
	for _, file := range files {
		if file.ContentAssessment.State != model.GitEvidenceExact && file.ContentAssessment.ReasonCode != "" {
			return file.ContentAssessment.ReasonCode
		}
	}
	return model.ReasonSnapshotObjectMissing
}

func patchAssessmentForChange(before SnapshotFile, beforePresent bool, after SnapshotFile, afterPresent bool) model.GitEvidenceAssessment {
	reasons := make([]model.GitEvidenceReasonCode, 0, 2)
	if beforePresent && before.PatchAssessment.State != model.GitEvidenceExact {
		reasons = appendUniqueReason(reasons, before.PatchAssessment.ReasonCode)
	}
	if afterPresent && after.PatchAssessment.State != model.GitEvidenceExact {
		reasons = appendUniqueReason(reasons, after.PatchAssessment.ReasonCode)
	}
	if len(reasons) == 0 {
		return model.ExactGitEvidence()
	}
	return model.NonExactGitEvidence(model.GitEvidenceUnavailable, reasons[0], reasons[1:]...)
}

func cloneSnapshotFile(file SnapshotFile) SnapshotFile {
	file.PathBytes = append([]byte(nil), file.PathBytes...)
	file.States = append([]PathState(nil), file.States...)
	file.Issues = append([]FileIssueCode(nil), file.Issues...)
	return file
}
