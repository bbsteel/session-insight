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

// DeleteSession permanently removes the session JSONL. Pi stores one file per
// session; there is no global history sidecar to purge.
func (r *PiReader) DeleteSession(id string) error {
	if !validSessionID(id) {
		return fmt.Errorf("invalid pi session id: %q", id)
	}
	path := r.findSessionFile(id)
	if path == "" {
		return fmt.Errorf("%w: %s", errSessionNotFound, id)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	r.pathsMu.Lock()
	delete(r.paths, id)
	r.pathsMu.Unlock()
	dir := filepath.Dir(path)
	if dir != "" && dir != "." && dir != r.sessionsDir {
		_ = os.Remove(dir)
	}
	return nil
}
