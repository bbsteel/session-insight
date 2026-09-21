package worktreereview

import (
	"os"
	"path/filepath"
)

// DefaultJournalRoot resolves the Worktree Review Session Journal root:
// WORKTREE_REVIEW_JOURNAL_ROOT override, then $XDG_STATE_HOME, then the
// platform default ~/.local/state. The path is trusted service
// configuration; it is never read from a reviewed repository. "" means the
// journal cannot live on this machine.
func DefaultJournalRoot() string {
	if override := os.Getenv("WORKTREE_REVIEW_JOURNAL_ROOT"); override != "" {
		return override
	}
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "worktree-review", "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "worktree-review", "sessions")
}
