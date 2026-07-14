package copilot

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bbsteel/session-insight/internal/procfind"
	"github.com/bbsteel/session-insight/internal/reader/shared"
)

// sessionFiles are the per-session files a live copilot plausibly keeps
// open; fd holders of any of them are exact, kill-safe PIDs.
var sessionFiles = []string{
	"events.jsonl", "session.db", "session.db-wal", "session.db-shm",
}

// SessionProcesses returns only fd-verified holders of the session's files.
// The inuse.<pid>.lock files copilot drops in the session directory are
// deliberately NOT trusted here: they survive process death (observed on
// real data), so after PID reuse a lock-named PID could point at an
// unrelated process — unacceptable for a kill list. Alive lock PIDs count
// toward SessionRunning below instead, where the worst a false positive
// can do is refuse a deletion.
func (r *CopilotReader) SessionProcesses(id string) ([]int, error) {
	if !validSessionID(id) {
		return nil, fmt.Errorf("invalid copilot session id: %q", id)
	}
	dir := filepath.Join(r.sessionDir, id)
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("copilot session not found: %s", id)
	}

	seen := map[int]bool{}
	var pids []int
	probe := make([]string, 0, len(sessionFiles)+2)
	for _, name := range sessionFiles {
		probe = append(probe, filepath.Join(dir, name))
	}
	if locks, err := filepath.Glob(filepath.Join(dir, "inuse.*.lock")); err == nil {
		probe = append(probe, locks...)
	}
	for _, p := range probe {
		holders, err := procfind.HoldersOf(p)
		if err != nil {
			return nil, err
		}
		for _, pid := range holders {
			if !seen[pid] {
				seen[pid] = true
				pids = append(pids, pid)
			}
		}
	}
	return pids, nil
}

// SessionRunning is the block-only fallback for a copilot that holds no
// file descriptors (never observed, but unverified across versions): the
// turn_start-without-turn_end marker bounded by LiveWindow, plus any
// inuse.<pid>.lock whose PID is still alive.
func (r *CopilotReader) SessionRunning(id string) (bool, error) {
	if !validSessionID(id) {
		return false, fmt.Errorf("invalid copilot session id: %q", id)
	}
	dir := filepath.Join(r.sessionDir, id)

	if locks, err := filepath.Glob(filepath.Join(dir, "inuse.*.lock")); err == nil {
		for _, lock := range locks {
			if pid, ok := lockPID(lock); ok && procfind.Alive(pid) {
				return true, nil
			}
		}
	}

	events, err := r.copilotEvents(id)
	if err != nil {
		return false, err
	}
	// The trailing in_progress event is already LiveWindow-bounded at
	// emission (shared.TrailingInProgress over events.jsonl mtime).
	return shared.HasTrailingInProgress(events), nil
}

func lockPID(path string) (int, bool) {
	name := filepath.Base(path)
	s := strings.TrimSuffix(strings.TrimPrefix(name, "inuse."), ".lock")
	pid, err := strconv.Atoi(s)
	return pid, err == nil && pid > 0
}

// DeleteSession permanently removes a copilot session: its rows in the
// global ~/.copilot/session-store.db (turns, checkpoints, file/ref
// indexes, trajectory events, and the FTS index — full message text lives
// there, which is exactly what a sensitive-data deletion must reach), then
// the session-state directory itself. The store purge goes first because
// it is idempotent — if the directory removal then fails, a retry still
// converges — mirroring the codex history-before-rollout ordering.
func (r *CopilotReader) DeleteSession(id string) error {
	if !validSessionID(id) {
		return fmt.Errorf("invalid copilot session id: %q", id)
	}
	dir := filepath.Join(r.sessionDir, id)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("copilot session not found: %s", id)
	}
	if err := r.purgeSessionStore(id); err != nil {
		return fmt.Errorf("purge session-store.db: %w", err)
	}
	return os.RemoveAll(dir)
}

// storeTables maps each session-store.db table that carries per-session
// rows to the column holding the session id. Tables are purged only if
// they exist, so schema drift across copilot versions degrades to a
// partial (still idempotent) purge instead of an error.
var storeTables = []struct{ table, column string }{
	{"turns", "session_id"},
	{"checkpoints", "session_id"},
	{"session_files", "session_id"},
	{"session_refs", "session_id"},
	{"forge_trajectory_events", "session_id"},
	{"search_index", "session_id"},
	{"sessions", "id"},
}

func (r *CopilotReader) purgeSessionStore(id string) error {
	storePath := filepath.Join(filepath.Dir(r.sessionDir), "session-store.db")
	if _, err := os.Stat(storePath); os.IsNotExist(err) {
		return nil
	}
	// A running copilot (other sessions) may hold this database; the busy
	// timeout rides out its write locks instead of failing immediately.
	db, err := sql.Open("sqlite3", "file:"+storePath+"?_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, t := range storeTables {
		var n int
		if err := tx.QueryRow(
			"SELECT count(*) FROM sqlite_master WHERE name = ?", t.table,
		).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		if _, err := tx.Exec(
			"DELETE FROM "+t.table+" WHERE "+t.column+" = ?", id,
		); err != nil {
			return fmt.Errorf("delete from %s: %w", t.table, err)
		}
	}
	return tx.Commit()
}
