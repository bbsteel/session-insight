package reader_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bbsteel/session-insight/internal/reader"
)

// The WORKTREE_REVIEW_JOURNAL_ROOT override must be discovered even where a
// home directory is unavailable: worktree-review discovery runs before the
// homeDir early return (PR #176 review).
func TestDiscoverHonorsWorktreeReviewOverride(t *testing.T) {
	journalRoot := filepath.Join(t.TempDir(), "journals")
	if err := os.MkdirAll(journalRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKTREE_REVIEW_JOURNAL_ROOT", journalRoot)

	readers := reader.Discover()
	found := false
	for _, r := range readers {
		if r.AgentType() == "worktree-review" {
			found = true
		}
	}
	if !found {
		t.Fatal("worktree-review reader not discovered despite a valid journal root override")
	}
}
