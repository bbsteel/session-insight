package chrys

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/shared"
)

// DeleteSession permanently removes a chrys session: the whole
// ~/.chrys/sessions/<id>/ directory, which holds everything the session
// ever produced (session.json, the session.recovery.json sidecar, sub-agent
// transcripts under sub_agents/, and file-edit snapshots under mutations/).
// Chrys keeps no global cross-session store, so the directory is the entire
// footprint.
func (r *ChrysReader) DeleteSession(id string) error {
	if !validSessionID(id) {
		return fmt.Errorf("invalid chrys session id: %q", id)
	}
	dir := filepath.Join(r.sessionsDir, id)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("chrys session not found: %s", id)
	}
	return os.RemoveAll(dir)
}

// SessionRunning reports whether the session's last turn looks in-flight.
// Chrys leaves no process↔session marker on disk (no lock file, no
// heartbeat, and it does not hold the session file open), so this is the
// agent's own recovery-checkpoint marker bounded by source freshness:
// the in_progress render event is emitted from the raw checkpoint, which a
// killed chrys leaves behind forever, so without the mtime bound a dead
// session would count as running indefinitely.
func (r *ChrysReader) SessionRunning(id string) (bool, error) {
	if !validSessionID(id) {
		return false, fmt.Errorf("invalid chrys session id: %q", id)
	}
	events, err := r.GetRenderEvents(id)
	if err != nil {
		return false, err
	}
	if !shared.HasTrailingInProgress(events) {
		return false, nil
	}
	return model.IsSessionLive(r.lastSessionWrite(id)), nil
}

// lastSessionWrite is the freshest mtime across the primary session file
// and its recovery sidecar — while a turn is in flight the newest writes
// land only in the sidecar (see readEffectiveSession).
func (r *ChrysReader) lastSessionWrite(id string) time.Time {
	var latest time.Time
	for _, name := range []string{"session.json", "session.recovery.json"} {
		if info, err := os.Stat(filepath.Join(r.sessionsDir, id, name)); err == nil {
			if info.ModTime().After(latest) {
				latest = info.ModTime()
			}
		}
	}
	return latest
}
