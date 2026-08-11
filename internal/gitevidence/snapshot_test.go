package gitevidence

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestCaptureAndNetDiffUseEffectiveDirtyStartInterval(t *testing.T) {
	repositoryPath := createRepository(t, t.TempDir(), "interval")
	for name, content := range map[string]string{
		"removed.txt":  "remove me\n",
		"reverted.txt": "same at both ends\n",
		"unstaged.txt": "before\n",
	} {
		writeFixtureFile(t, repositoryPath, name, []byte(content), 0o644)
	}
	gitCommand(t, repositoryPath, "add", "--", "removed.txt", "reverted.txt", "unstaged.txt")
	gitCommand(t, repositoryPath, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "more files")

	// Dirty before the Session: these exact states belong to the baseline and
	// must not appear merely because they differ from HEAD.
	writeFixtureFile(t, repositoryPath, "tracked.txt", []byte("dirty before session\n"), 0o644)
	writeFixtureFile(t, repositoryPath, "preexisting-untracked.txt", []byte("already here\n"), 0o644)
	capturer, repository := snapshotFixture(t, repositoryPath, CaptureConfig{})
	baseline := captureFixture(t, capturer, repository, model.GitSnapshotBaseline, "source-start")

	writeFixtureFile(t, repositoryPath, "tracked.txt", []byte("changed in session\n"), 0o755)
	writeFixtureFile(t, repositoryPath, "staged.txt", []byte("staged\n"), 0o644)
	gitCommand(t, repositoryPath, "add", "--", "staged.txt")
	writeFixtureFile(t, repositoryPath, "unstaged.txt", []byte("after\n"), 0o644)
	if err := os.Remove(filepath.Join(repositoryPath, "removed.txt")); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, repositoryPath, "new-untracked.txt", []byte("new\n"), 0o644)
	writeFixtureFile(t, repositoryPath, "reverted.txt", []byte("temporary edit\n"), 0o644)
	writeFixtureFile(t, repositoryPath, "reverted.txt", []byte("same at both ends\n"), 0o644)
	final := captureFixture(t, capturer, repository, model.GitSnapshotFinal, "source-final")

	diff, err := DiffSnapshots(baseline, final)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]model.GitFileStatus{
		"new-untracked.txt": model.GitFileAdded,
		"removed.txt":       model.GitFileDeleted,
		"staged.txt":        model.GitFileAdded,
		"tracked.txt":       model.GitFileModified,
		"unstaged.txt":      model.GitFileModified,
	}
	if len(diff.Files) != len(want) {
		t.Fatalf("diff files = %v", summarizeDiff(diff))
	}
	for _, file := range diff.Files {
		if want[file.DisplayPath] != file.Status {
			t.Errorf("%s status = %s, want %s", file.DisplayPath, file.Status, want[file.DisplayPath])
		}
		delete(want, file.DisplayPath)
	}
	if len(want) != 0 {
		t.Fatalf("missing diff paths: %v", want)
	}
	for _, absent := range []string{"preexisting-untracked.txt", "reverted.txt"} {
		if diffHasPath(diff, absent) {
			t.Fatalf("%s should disappear from the interval diff", absent)
		}
	}
	assertFileStates(t, final, "staged.txt", PathTracked, PathStaged)
	assertFileStates(t, final, "unstaged.txt", PathTracked, PathUnstaged)
	assertFileStates(t, final, "new-untracked.txt", PathUntracked)
	assertFileStates(t, final, "removed.txt", PathTracked, PathUnstaged, PathRemoved)
	if file := snapshotFile(t, final, "tracked.txt"); file.Mode != "100755" {
		t.Fatalf("tracked mode = %q", file.Mode)
	}
}

func TestLocalRenameRemainsAddAndDelete(t *testing.T) {
	repositoryPath := createRepository(t, t.TempDir(), "rename")
	capturer, repository := snapshotFixture(t, repositoryPath, CaptureConfig{})
	baseline := captureFixture(t, capturer, repository, model.GitSnapshotBaseline, "before-rename")
	if err := os.Rename(filepath.Join(repositoryPath, "tracked.txt"), filepath.Join(repositoryPath, "renamed.txt")); err != nil {
		t.Fatal(err)
	}
	final := captureFixture(t, capturer, repository, model.GitSnapshotFinal, "after-rename")
	diff, err := DiffSnapshots(baseline, final)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Files) != 2 || !diffHasStatus(diff, "tracked.txt", model.GitFileDeleted) ||
		!diffHasStatus(diff, "renamed.txt", model.GitFileAdded) {
		t.Fatalf("rename diff = %v", summarizeDiff(diff))
	}
}

func TestCaptureClassifiesBinarySymlinkGitlinkSpecialAndOversize(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFO and Unix symlink fixture")
	}
	parent := t.TempDir()
	repositoryPath := createRepository(t, parent, "types")
	writeFixtureFile(t, repositoryPath, "binary.dat", []byte{'a', 0, 'b'}, 0o644)
	writeFixtureFile(t, repositoryPath, "oversize.txt", bytes.Repeat([]byte("x"), 256), 0o644)
	outside := filepath.Join(parent, "outside-secret")
	writeFixtureFile(t, parent, "outside-secret", []byte("DO-NOT-READ"), 0o600)
	if err := os.Symlink("../outside-secret", filepath.Join(repositoryPath, "escape-link")); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("mkfifo", filepath.Join(repositoryPath, "named-pipe")).CombinedOutput(); err != nil {
		t.Fatalf("mkfifo: %v: %s", err, output)
	}
	head := strings.TrimSpace(gitCommand(t, repositoryPath, "rev-parse", "HEAD"))
	trackedOID := strings.TrimSpace(gitCommand(t, repositoryPath, "rev-parse", "HEAD:tracked.txt"))
	gitCommand(t, repositoryPath, "update-index", "--add", "--cacheinfo", "100644,"+trackedOID+",named-pipe")
	gitCommand(t, repositoryPath, "update-index", "--add", "--cacheinfo", "160000,"+head+",vendor/module")
	if err := os.MkdirAll(filepath.Join(repositoryPath, "vendor", "module"), 0o755); err != nil {
		t.Fatal(err)
	}

	config := CaptureConfig{Limits: DefaultCaptureLimits()}
	config.Limits.MaxFileBytes = 64
	config.Limits.MaxSessionBytes = 512
	capturer, repository := snapshotFixture(t, repositoryPath, config)
	snapshot := captureFixture(t, capturer, repository, model.GitSnapshotCheckpoint, "typed-files")

	binaryFile := snapshotFile(t, snapshot, "binary.dat")
	if !binaryFile.Binary || !hasIssue(binaryFile, FileIssueBinary) || binaryFile.PatchAssessment.ReasonCode != model.ReasonBinaryPatchUnavailable {
		t.Fatalf("binary classification = %+v", binaryFile)
	}
	link := snapshotFile(t, snapshot, "escape-link")
	if link.Kind != FileSymlink || !hasIssue(link, FileIssueSymlink) {
		t.Fatalf("symlink classification = %+v", link)
	}
	linkContent := snapshotContent(t, snapshot, link.ContentRef)
	if string(linkContent) != "../outside-secret" || bytes.Contains(linkContent, []byte("DO-NOT-READ")) {
		t.Fatalf("symlink content followed target: %q", linkContent)
	}
	gitlink := snapshotFile(t, snapshot, "vendor/module")
	if gitlink.Kind != FileGitlink || gitlink.GitOID != head || gitlink.PatchAssessment.ReasonCode != model.ReasonSubmoduleNotExpanded {
		t.Fatalf("gitlink classification = %+v", gitlink)
	}
	special := snapshotFile(t, snapshot, "named-pipe")
	if special.Kind != FileSpecial || !hasIssue(special, FileIssueSpecial) || special.ContentRef != "" {
		t.Fatalf("special classification = %+v", special)
	}
	oversize := snapshotFile(t, snapshot, "oversize.txt")
	if !hasIssue(oversize, FileIssueOversize) || oversize.ContentRef != "" || oversize.ContentAssessment.ReasonCode != model.ReasonSnapshotLimitExceeded {
		t.Fatalf("oversize classification = %+v", oversize)
	}
	if snapshot.Assessment.State != model.GitEvidenceEstimated {
		t.Fatalf("snapshot assessment = %+v", snapshot.Assessment)
	}
	_ = outside
}

func TestCaptureRepresentsRemovedGitlinkAsMissing(t *testing.T) {
	repositoryPath := createRepository(t, t.TempDir(), "removed-gitlink")
	head := strings.TrimSpace(gitCommand(t, repositoryPath, "rev-parse", "HEAD"))
	gitCommand(t, repositoryPath, "update-index", "--add", "--cacheinfo", "160000,"+head+",vendor/module")
	capturer, repository := snapshotFixture(t, repositoryPath, CaptureConfig{})
	snapshot := captureFixture(t, capturer, repository, model.GitSnapshotFinal, "removed-gitlink")
	file := snapshotFile(t, snapshot, "vendor/module")
	if file.Present || file.Kind != FileMissing || !hasIssue(file, FileIssueRemoved) {
		t.Fatalf("removed gitlink = %+v", file)
	}
}

func TestCaptureRejectsSymlinkParentEscapeWithoutReadingTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix symlink fixture")
	}
	parent := t.TempDir()
	repositoryPath := createRepository(t, parent, "parent-link")
	outsideDir := filepath.Join(parent, "outside")
	if err := os.Mkdir(outsideDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, outsideDir, "secret", []byte("SECRET"), 0o600)
	if err := os.Symlink(outsideDir, filepath.Join(repositoryPath, "linked-dir")); err != nil {
		t.Fatal(err)
	}
	capturer, _ := snapshotFixture(t, repositoryPath, CaptureConfig{})
	root, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	base := newSnapshotFile("linked-dir/secret", indexEntry{}, false, pathStatus{untracked: true})
	file, content, retry := capturer.readPathOnce(context.Background(), root, "linked-dir/secret", base, 1024, 0)
	if retry || content != nil || !hasIssue(file, FileIssueUnsafePath) || file.ContentRef != "" {
		t.Fatalf("symlink-parent result file=%+v content=%q retry=%v", file, content, retry)
	}
}

func TestOpenSnapshotFileDoesNotFollowFinalSymlink(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" || runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("platform does not expose O_NOFOLLOW through os.Root")
	}
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "root")
	if err := os.Mkdir(rootPath, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, parent, "secret", []byte("DO-NOT-READ"), 0o600)
	if err := os.Symlink("../secret", filepath.Join(rootPath, "leaf")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if handle, err := openSnapshotFile(root, "leaf"); err == nil {
		handle.Close()
		t.Fatal("final symlink was followed")
	}
}

func TestCaptureFileRaceDegradesWithoutPublishingRacedBytes(t *testing.T) {
	repositoryPath := createRepository(t, t.TempDir(), "file-race")
	capturer, repository := snapshotFixture(t, repositoryPath, CaptureConfig{})
	original := []byte("tracked\n")
	capturer.afterFileRead = func(path string, _ int) {
		if path != "tracked.txt" {
			return
		}
		writeFixtureFile(t, repositoryPath, path, []byte("transient\n"), 0o644)
		writeFixtureFile(t, repositoryPath, path, original, 0o644)
	}
	snapshot := captureFixture(t, capturer, repository, model.GitSnapshotCheckpoint, "file-race")
	file := snapshotFile(t, snapshot, "tracked.txt")
	if !hasIssue(file, FileIssueRaced) || file.ContentRef != "" || file.ContentAssessment.ReasonCode != model.ReasonCaptureRaced {
		t.Fatalf("raced file = %+v", file)
	}
}

func TestCaptureRetriesWholeSnapshotAndFailsClosed(t *testing.T) {
	repositoryPath := createRepository(t, t.TempDir(), "snapshot-race")
	config := CaptureConfig{Limits: DefaultCaptureLimits()}
	config.Limits.Attempts = 2
	capturer, repository := snapshotFixture(t, repositoryPath, config)
	capturer.afterManifestRead = func(attempt int) {
		writeFixtureFile(t, repositoryPath, "race-"+string(rune('a'+attempt)), []byte("race\n"), 0o644)
	}
	result, err := capturer.Capture(context.Background(), *repository, CaptureRequest{
		Kind: model.GitSnapshotFinal, SourceRevision: "racing-source",
	})
	var captureErr *CaptureError
	if !errors.As(err, &captureErr) || captureErr.Code != CaptureErrorRaced {
		t.Fatalf("capture error = %v", err)
	}
	if result.Snapshot != nil || result.Assessment.ReasonCode != model.ReasonCaptureRaced {
		t.Fatalf("raced result = %+v", result)
	}
}

func TestCaptureRejectsChangedWorktreeIdentity(t *testing.T) {
	repositoryPath := createRepository(t, t.TempDir(), "identity")
	capturer, repository := snapshotFixture(t, repositoryPath, CaptureConfig{})
	repository.WorktreeID = "worktree:sha256:" + strings.Repeat("0", 64)
	result, err := capturer.Capture(context.Background(), *repository, CaptureRequest{
		Kind: model.GitSnapshotBaseline, SourceRevision: "wrong-identity",
	})
	var captureErr *CaptureError
	if !errors.As(err, &captureErr) || captureErr.Code != CaptureErrorIdentityChanged {
		t.Fatalf("identity error = %v", err)
	}
	if result.Snapshot != nil || result.Assessment.ReasonCode != model.ReasonWorktreeIdentityChanged {
		t.Fatalf("identity result = %+v", result)
	}
}

func TestCaptureHonorsPathAndSessionContentLimits(t *testing.T) {
	repositoryPath := createRepository(t, t.TempDir(), "limits")
	writeFixtureFile(t, repositoryPath, "another.txt", []byte("12345"), 0o644)
	pathConfig := CaptureConfig{Limits: DefaultCaptureLimits()}
	pathConfig.Limits.MaxPaths = 1
	pathCapturer, repository := snapshotFixture(t, repositoryPath, pathConfig)
	result, err := pathCapturer.Capture(context.Background(), *repository, CaptureRequest{
		Kind: model.GitSnapshotBaseline, SourceRevision: "path-limit",
	})
	var captureErr *CaptureError
	if !errors.As(err, &captureErr) || captureErr.Code != CaptureErrorLimit || result.Assessment.ReasonCode != model.ReasonSnapshotLimitExceeded {
		t.Fatalf("path limit result=%+v err=%v", result, err)
	}

	sessionConfig := CaptureConfig{Limits: DefaultCaptureLimits()}
	sessionConfig.Limits.MaxSessionBytes = 9
	sessionCapturer, repository := snapshotFixture(t, repositoryPath, sessionConfig)
	snapshot := captureFixture(t, sessionCapturer, repository, model.GitSnapshotBaseline, "session-limit")
	limited := 0
	for _, file := range snapshot.Files {
		if hasIssue(file, FileIssueSessionLimit) {
			limited++
		}
	}
	if limited == 0 || snapshot.Assessment.State != model.GitEvidenceEstimated {
		t.Fatalf("session limit snapshot = %+v", snapshot)
	}
}

func TestValidateSnapshotPathRejectsTraversalAndInvalidBytes(t *testing.T) {
	for _, path := range []string{"", ".", "..", "../secret", "a/../secret", "/absolute", "a//b", "nul\x00byte", strings.Repeat("x", 9)} {
		t.Run(strings.ReplaceAll(path, "/", "_"), func(t *testing.T) {
			err := validateSnapshotPath(path, 8)
			var captureErr *CaptureError
			if !errors.As(err, &captureErr) || captureErr.Code != CaptureErrorUnsafePath {
				t.Fatalf("validateSnapshotPath(%q) error = %v, want unsafe path", path, err)
			}
		})
	}
	if err := validateSnapshotPath("a/b", 8); err != nil {
		t.Fatalf("safe path rejected: %v", err)
	}
}

func TestBoundedFileReadHonorsCaptureContext(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := readBoundedWithContext(ctx, reader, 1024); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bounded read error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("bounded read took %s", elapsed)
	}
}

func TestCaptureDoesNotMutateRepositoryOrRunConfiguredHelpers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	parent := t.TempDir()
	repositoryPath := createRepository(t, parent, "read-only-capture")
	marker := filepath.Join(parent, "helper-invoked")
	helper := filepath.Join(parent, "hostile-helper.sh")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf invoked > '"+marker+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, repositoryPath, "config", "core.fsmonitor", helper)
	gitCommand(t, repositoryPath, "config", "diff.external", helper)
	gitCommand(t, repositoryPath, "config", "core.hooksPath", filepath.Join(parent, "hooks"))
	capturer, repository := snapshotFixture(t, repositoryPath, CaptureConfig{})
	before := repositoryControlDigest(t, filepath.Join(repositoryPath, ".git"))
	_ = captureFixture(t, capturer, repository, model.GitSnapshotBaseline, "read-only")
	after := repositoryControlDigest(t, filepath.Join(repositoryPath, ".git"))
	if before != after {
		t.Fatalf("capture mutated repository: before=%s after=%s", before, after)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("configured helper executed: %v", err)
	}
}

func snapshotFixture(t *testing.T, repositoryPath string, config CaptureConfig) (*Capturer, *Repository) {
	t.Helper()
	runner := testRunner(t, nil)
	resolver, err := NewResolver(runner)
	if err != nil {
		t.Fatal(err)
	}
	repository := resolvedRepository(t, resolver, repositoryPath)
	capturer, err := NewCapturer(runner, config)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	var tick int64
	capturer.now = func() time.Time {
		tick++
		return base.Add(time.Duration(tick) * time.Millisecond)
	}
	return capturer, repository
}

func captureFixture(t *testing.T, capturer *Capturer, repository *Repository, kind model.GitSnapshotKind, revision string) *Snapshot {
	t.Helper()
	result, err := capturer.Capture(context.Background(), *repository, CaptureRequest{Kind: kind, SourceRevision: revision})
	if err != nil {
		t.Fatal(err)
	}
	if result.Snapshot == nil {
		t.Fatalf("capture returned no snapshot: %+v", result)
	}
	return result.Snapshot
}

func writeFixtureFile(t *testing.T, root, relative string, content []byte, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func snapshotFile(t *testing.T, snapshot *Snapshot, path string) SnapshotFile {
	t.Helper()
	for _, file := range snapshot.Files {
		if string(file.PathBytes) == path {
			return file
		}
	}
	t.Fatalf("snapshot path %q not found", path)
	return SnapshotFile{}
}

func snapshotContent(t *testing.T, snapshot *Snapshot, ref string) []byte {
	t.Helper()
	for _, content := range snapshot.Contents {
		if content.SHA256 == ref {
			return content.Bytes
		}
	}
	t.Fatalf("content ref %q not found", ref)
	return nil
}

func hasIssue(file SnapshotFile, issue FileIssueCode) bool {
	for _, candidate := range file.Issues {
		if candidate == issue {
			return true
		}
	}
	return false
}

func assertFileStates(t *testing.T, snapshot *Snapshot, path string, states ...PathState) {
	t.Helper()
	file := snapshotFile(t, snapshot, path)
	if len(file.States) != len(states) {
		t.Fatalf("%s states = %v, want %v", path, file.States, states)
	}
	for index := range states {
		if file.States[index] != states[index] {
			t.Fatalf("%s states = %v, want %v", path, file.States, states)
		}
	}
}

func diffHasPath(diff NetDiff, path string) bool {
	for _, file := range diff.Files {
		if string(file.PathBytes) == path {
			return true
		}
	}
	return false
}

func diffHasStatus(diff NetDiff, path string, status model.GitFileStatus) bool {
	for _, file := range diff.Files {
		if string(file.PathBytes) == path && file.Status == status {
			return true
		}
	}
	return false
}

func summarizeDiff(diff NetDiff) []string {
	result := make([]string, 0, len(diff.Files))
	for _, file := range diff.Files {
		result = append(result, string(file.Status)+":"+file.DisplayPath)
	}
	return result
}
