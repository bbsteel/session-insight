//go:build js || plan9 || wasip1 || windows

package gitevidence

import "os"

// These platforms do not expose a portable O_NOFOLLOW flag through os.
// os.Root still enforces worktree containment, and the caller's descriptor
// and path identity checks discard raced content.
func openSnapshotFile(root *os.Root, path string) (*os.File, error) {
	return root.Open(path)
}
