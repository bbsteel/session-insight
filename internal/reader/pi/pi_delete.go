package pi

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/shared"
)

// Pi has no OS-visible process↔session mapping to offer. Its native
// SessionManager closes the transcript descriptor after each write and appends
// with appendFileSync, so fd holders only exist during a write (verified with
// the installed Pi 1.3.0 SessionManager) — an idle-but-alive Pi process holds
// no descriptor on the session file. There is also no heartbeat or lock
// registry, so an exact PID cannot be recovered from the OS.
//
// Therefore this adapter does not implement reader.SessionProcessFinder and
// declares terminate unsupported. Delete safety comes from SessionRunning,
// which is conservative: it reports a session as running while its transcript
// was written inside model.LiveWindow, so a session that a live Pi process may
// still be appending to is never deleted out from under it.
func (r *PiReader) SessionRunning(id string) (bool, error) {
	path := r.findSessionFile(id)
	if path == "" {
		return false, fmt.Errorf("%w: %s", errSessionNotFound, id)
	}
	events, err := r.GetRenderEvents(id)
	if err != nil {
		return false, err
	}
	// Trailing in_progress is already LiveWindow-bounded at emission.
	if shared.HasTrailingInProgress(events) {
		return true, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	// A closed turn does not mean the session is over: Pi keeps the session
	// open and idle at its prompt without touching the file. Recent on-disk
	// activity is the only remaining signal, and refusing a delete for one
	// LiveWindow is the safe direction for a destructive action.
	return model.IsSessionLive(info.ModTime()), nil
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
