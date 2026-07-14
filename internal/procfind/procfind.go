// Package procfind locates the processes that hold a file open and
// terminates a process by exact PID, cross-platform.
//
// It exists for session deletion: an agent CLI (e.g. codex) keeps its
// session log open for append while running, so "who holds this file"
// is both the accurate liveness check (idle sessions don't touch mtime)
// and the source of the exact PID to kill. No name-based process
// matching happens anywhere in this package.
package procfind

import "os"

// HoldersOf returns the PIDs of processes that currently hold path open.
// The calling process itself is excluded. An empty slice means no one
// holds the file (or the file no longer exists).
//
// Platform notes: Linux scans /proc/*/fd; macOS shells out to lsof;
// Windows asks the Restart Manager. PIDs of other users' processes may
// be invisible on Unix; for a local single-user tool that is acceptable.
func HoldersOf(path string) ([]int, error) {
	pids, err := holdersOf(path)
	if err != nil {
		return nil, err
	}
	self := os.Getpid()
	out := pids[:0]
	for _, pid := range pids {
		if pid != self {
			out = append(out, pid)
		}
	}
	return out, nil
}
