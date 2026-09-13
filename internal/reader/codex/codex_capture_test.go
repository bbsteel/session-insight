package codex

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func snapshotDirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// Regression: error returns after CreateTemp must not clobber the named
// snapshotPath return value, or the deferred cleanup removes "" and leaks the
// capture. A cancelled context exercises the pre-write error branch.
func TestCaptureCodexSourceCancelledContextRemovesSnapshot(t *testing.T) {
	snapshotDir := t.TempDir()
	sourcePath := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(sourcePath, []byte("{\"type\":\"session_meta\"}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := captureCodexSource(ctx, sourcePath, snapshotDir)
	if err == nil {
		t.Fatal("expected context cancelled error")
	}
	if names := snapshotDirEntries(t, snapshotDir); len(names) != 0 {
		t.Fatalf("cancelled capture leaked snapshot file(s): %v", names)
	}
}

// Success keeps the capture for the caller and reports the content fingerprint.
func TestCaptureCodexSourceSuccess(t *testing.T) {
	snapshotDir := t.TempDir()
	content := strings.Repeat("{\"type\":\"message\"}\n", 1000)
	sourcePath := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(sourcePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	snapshotPath, fingerprint, err := captureCodexSource(context.Background(), sourcePath, snapshotDir)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if filepath.Dir(snapshotPath) != snapshotDir {
		t.Fatalf("snapshot must live in the configured dir: %s", snapshotPath)
	}
	copied, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if string(copied) != content {
		t.Fatalf("snapshot content mismatch: %d bytes", len(copied))
	}
	wantDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	if fingerprint.Digest != wantDigest || fingerprint.SizeBytes != int64(len(content)) {
		t.Fatalf("fingerprint = %+v, want digest %s size %d", fingerprint, wantDigest, len(content))
	}
}

// A cancelled ReadIndexSnapshotEnvelope must leave the configured snapshot dir
// empty once the read returns.
func TestReadIndexSnapshotEnvelopeCancelledLeavesNoSnapshot(t *testing.T) {
	snapshotDir := t.TempDir()
	sessionsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sessionsDir, "s1.jsonl"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	r := New(sessionsDir, WithSnapshotDir(snapshotDir))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.ReadIndexSnapshotEnvelope(ctx, model.Session{ID: "s1"}); err == nil {
		t.Fatal("expected context cancelled error")
	}
	if names := snapshotDirEntries(t, snapshotDir); len(names) != 0 {
		t.Fatalf("cancelled envelope read leaked snapshot file(s): %v", names)
	}
}

// Construction reclaims captures orphaned by a crashed process, but leaves
// fresh (plausibly in-flight) ones alone.
func TestNewSweepsStaleSnapshotsOnly(t *testing.T) {
	snapshotDir := t.TempDir()
	stale := filepath.Join(snapshotDir, indexSnapshotTempPrefix+"stale.jsonl")
	fresh := filepath.Join(snapshotDir, indexSnapshotTempPrefix+"fresh.jsonl")
	unrelated := filepath.Join(snapshotDir, "unrelated.jsonl")
	for _, path := range []string{stale, fresh, unrelated} {
		if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * staleIndexSnapshotMaxAge)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(unrelated, old, old); err != nil {
		t.Fatal(err)
	}

	New(t.TempDir(), WithSnapshotDir(snapshotDir))

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale snapshot was not swept: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh snapshot must survive the sweep: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated file must survive the sweep: %v", err)
	}
}
