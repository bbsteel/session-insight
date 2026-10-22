package pi

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/bbsteel/session-insight/internal/reader/shared"
)

// SessionRunning reports whether a live Pi process may still own this session.
//
// It is a block-only liveness check (no PIDs are returned, because Pi's
// process↔session mapping is a working directory, not a per-session process, so
// a PID here could not be attributed to one session safely enough to kill).
//
// The evidence is process-based and time-independent:
//
//   - a trailing in_progress marker on a transcript written inside LiveWindow
//     (the turn is mid-flight), or
//   - a live `pi` process whose working directory encodes to this session's
//     directory, which is how Pi scopes sessions to a project.
//
// Deliberately NOT used: the transcript's own mtime. Pi keeps a session open
// and idle at its prompt without touching the file, so any mtime window —
// including model.LiveWindow — eventually reports a living session as stopped,
// after which deleting it would lose its history: Pi's next append recreates a
// headerless transcript.
func (r *PiReader) SessionRunning(id string) (bool, error) {
	path := r.findSessionFile(id)
	if path == "" {
		return false, fmt.Errorf("%w: %s", errSessionNotFound, id)
	}

	// A turn still in flight is unambiguous, and the marker is only emitted
	// for a transcript written inside LiveWindow.
	events, err := r.GetRenderEvents(id)
	if err != nil {
		return false, err
	}
	if shared.HasTrailingInProgress(events) {
		return true, nil
	}

	// Any live Pi process working in this session's cwd may hold the session
	// open or resume it later. Ignoring mtime is what makes this sound.
	if len(r.sessionProcessCandidates(path)) > 0 {
		return true, nil
	}
	return false, nil
}

// DeleteSession permanently removes every JSONL whose filename encodes this
// session id. Duplicate files (copy, crash leftover, in-progress rewrite)
// must not survive a successful delete, or the next list would rebind the id.
//
// Ordering matters for a partial failure: older duplicates go first and the
// canonical (newest) transcript last, and the loop stops at the first error.
// Removing the newest file before a stale copy it cannot read or unlink would
// destroy the current transcript while leaving the leftover to be adopted.
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
	files = orderSessionFilesOldestFirst(files)

	dirs := map[string]struct{}{}
	for _, path := range files {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			// Stop before touching the newer copies: a delete that cannot
			// complete must not leave the id bound to an older transcript.
			return err
		}
		dir := filepath.Dir(path)
		if dir != "" && dir != "." && dir != r.sessionsDir {
			dirs[dir] = struct{}{}
		}
	}
	r.forgetPath(id)
	for dir := range dirs {
		_ = os.Remove(dir)
	}
	return nil
}

// orderSessionFilesOldestFirst sorts duplicates so the canonical transcript is
// last. Ties fall back to the filename, matching newestFileIsLater.
func orderSessionFilesOldestFirst(paths []string) []string {
	type entry struct {
		path    string
		modTime int64
	}
	entries := make([]entry, 0, len(paths))
	for _, path := range paths {
		var modTime int64
		if info, err := os.Stat(path); err == nil {
			modTime = info.ModTime().UnixNano()
		}
		entries = append(entries, entry{path: path, modTime: modTime})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].modTime != entries[j].modTime {
			return entries[i].modTime < entries[j].modTime
		}
		return entries[i].path < entries[j].path
	})
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.path
	}
	return out
}
