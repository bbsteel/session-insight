//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package gitevidence

import (
	"os"
	"syscall"
)

// openSnapshotFile rejects a final symlink and cannot block if a regular file
// is raced into a FIFO between Lstat and OpenFile. The caller still verifies
// the opened descriptor with fstat and a final path-relative lstat.
func openSnapshotFile(root *os.Root, path string) (*os.File, error) {
	return root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
