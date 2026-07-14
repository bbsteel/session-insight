//go:build linux

package procfind

import (
	"os"
	"path/filepath"
	"strconv"
)

// holdersOf scans /proc/<pid>/fd symlinks. Unreadable entries (other
// users' processes, races with exiting processes) are skipped silently:
// partial visibility is inherent to /proc and not an error.
func holdersOf(path string) ([]int, error) {
	target, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// Resolve symlinks so the comparison matches what /proc/*/fd links to.
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}

	procEntries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	var pids []int
	for _, entry := range procEntries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		fdDir := filepath.Join("/proc", entry.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if link == target {
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids, nil
}
