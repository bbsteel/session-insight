package pi

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bbsteel/session-insight/internal/procfind"
	"github.com/bbsteel/session-insight/internal/reader/shared"
)

// SessionProcesses returns PIDs that hold the session JSONL open. Pi appends
// to the file for the lifetime of the process, including idle input waits.
func (r *PiReader) SessionProcesses(id string) ([]int, error) {
	path := r.findSessionFile(id)
	if path == "" {
		return nil, fmt.Errorf("%w: %s", errSessionNotFound, id)
	}
	return procfind.HoldersOf(path)
}

// SessionRunning is the block-only fallback when no PID is available:
// trailing in_progress already bounded by LiveWindow at emission.
func (r *PiReader) SessionRunning(id string) (bool, error) {
	if pids, err := r.SessionProcesses(id); err == nil && len(pids) > 0 {
		return true, nil
	}
	events, err := r.GetRenderEvents(id)
	if err != nil {
		return false, err
	}
	return shared.HasTrailingInProgress(events), nil
}

// DeleteSession permanently removes every JSONL whose filename encodes this
// session id. Duplicate files (copy, crash leftover, in-progress rewrite)
// must not survive a successful delete, or the next list would rebind the id.
func (r *PiReader) DeleteSession(id string) error {
	if !validSessionID(id) {
		return fmt.Errorf("invalid pi session id: %q", id)
	}
	files := r.collectSessionFiles(id)
	if known := r.knownSessionFile(id); known != "" {
		found := false
		for _, path := range files {
			if path == known {
				found = true
				break
			}
		}
		if !found {
			files = append(files, known)
		}
	}
	if len(files) == 0 {
		return fmt.Errorf("%w: %s", errSessionNotFound, id)
	}
	var first error
	removed := 0
	dirs := map[string]struct{}{}
	for _, path := range files {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			if first == nil {
				first = err
			}
			continue
		}
		removed++
		dir := filepath.Dir(path)
		if dir != "" && dir != "." && dir != r.sessionsDir {
			dirs[dir] = struct{}{}
		}
	}
	r.forgetPath(id)
	for dir := range dirs {
		_ = os.Remove(dir)
	}
	if removed == 0 {
		if first != nil {
			return first
		}
		return fmt.Errorf("%w: %s", errSessionNotFound, id)
	}
	return first
}
