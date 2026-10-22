//go:build linux

package procfind

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
)

// WorkingDir returns the process's current working directory, or "" when it
// cannot be read (process gone, other user, or an unreadable link).
func WorkingDir(pid int) string {
	if pid <= 0 {
		return ""
	}
	dir, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
	if err != nil {
		return ""
	}
	return dir
}

// ProcessesNamed returns the live PIDs whose /proc comm (the truncated
// executable name, which an agent CLI sets via process.title) equals name.
//
// Only /proc metadata is read. Process arguments and environments are never
// inspected, so no prompts, paths in argv, or credentials pass through this
// package.
func ProcessesNamed(name string) ([]int, error) {
	if name == "" {
		return nil, nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	self := os.Getpid()
	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == self {
			continue
		}
		comm, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
		if err != nil {
			continue
		}
		if string(bytes.TrimRight(comm, "\n")) == name {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}
