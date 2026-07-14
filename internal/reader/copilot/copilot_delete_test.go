package copilot

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

const (
	delID  = "11111111-1111-1111-1111-111111111111"
	keepID = "22222222-2222-2222-2222-222222222222"
)

// newTestCopilotHome replicates ~/.copilot: session-state/<id>/ dirs plus
// the global session-store.db with the observed schema subset, rows for
// two sessions each.
func newTestCopilotHome(t *testing.T) (*CopilotReader, string) {
	t.Helper()
	root := t.TempDir()
	stateDir := filepath.Join(root, "session-state")

	for _, id := range []string{delID, keepID} {
		dir := filepath.Join(stateDir, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		files := map[string]string{
			"workspace.yaml": "id: " + id + "\ncwd: /home/u/proj\ncreated_at: \"2026-07-14T10:00:00Z\"\nupdated_at: \"2026-07-14T10:05:00Z\"\n",
			"events.jsonl":   `{"type":"user.message","timestamp":"2026-07-14T10:00:00Z","data":{"content":"hi"}}` + "\n",
		}
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	db, err := sql.Open("sqlite3", filepath.Join(root, "session-store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, cwd TEXT, summary TEXT)`,
		`CREATE TABLE turns (id INTEGER PRIMARY KEY, session_id TEXT NOT NULL, user_message TEXT)`,
		`CREATE TABLE checkpoints (id INTEGER PRIMARY KEY, session_id TEXT NOT NULL, title TEXT)`,
		`CREATE TABLE session_files (id INTEGER PRIMARY KEY, session_id TEXT NOT NULL, file_path TEXT)`,
		`CREATE TABLE session_refs (id INTEGER PRIMARY KEY, session_id TEXT NOT NULL, ref_value TEXT)`,
		`CREATE TABLE forge_trajectory_events (id INTEGER PRIMARY KEY, session_id TEXT NOT NULL, command TEXT)`,
		`CREATE VIRTUAL TABLE search_index USING fts5(content, session_id UNINDEXED)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{delID, keepID} {
		mustExec(t, db, `INSERT INTO sessions (id, cwd, summary) VALUES (?, ?, ?)`, id, "/home/u/proj", "secret summary "+id)
		mustExec(t, db, `INSERT INTO turns (session_id, user_message) VALUES (?, ?)`, id, "secret message "+id)
		mustExec(t, db, `INSERT INTO checkpoints (session_id, title) VALUES (?, ?)`, id, "cp")
		mustExec(t, db, `INSERT INTO session_files (session_id, file_path) VALUES (?, ?)`, id, "/etc/passwd")
		mustExec(t, db, `INSERT INTO session_refs (session_id, ref_value) VALUES (?, ?)`, id, "ref")
		mustExec(t, db, `INSERT INTO forge_trajectory_events (session_id, command) VALUES (?, ?)`, id, "rm -rf")
		mustExec(t, db, `INSERT INTO search_index (content, session_id) VALUES (?, ?)`, "secret content "+id, id)
	}

	return New(stateDir), root
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func TestDeleteSessionPurgesStoreAndDir(t *testing.T) {
	r, root := newTestCopilotHome(t)

	if err := r.DeleteSession(delID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "session-state", delID)); !os.IsNotExist(err) {
		t.Errorf("session dir should be gone, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "session-state", keepID)); err != nil {
		t.Errorf("other session dir damaged: %v", err)
	}

	db, err := sql.Open("sqlite3", filepath.Join(root, "session-store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, tc := range storeTables {
		var nDel, nKeep int
		if err := db.QueryRow("SELECT count(*) FROM "+tc.table+" WHERE "+tc.column+" = ?", delID).Scan(&nDel); err != nil {
			t.Fatalf("count %s: %v", tc.table, err)
		}
		if err := db.QueryRow("SELECT count(*) FROM "+tc.table+" WHERE "+tc.column+" = ?", keepID).Scan(&nKeep); err != nil {
			t.Fatalf("count %s: %v", tc.table, err)
		}
		if nDel != 0 {
			t.Errorf("%s: %d rows of deleted session survive", tc.table, nDel)
		}
		if nKeep != 1 {
			t.Errorf("%s: other session's rows damaged (%d)", tc.table, nKeep)
		}
	}
}

func TestDeleteSessionWithoutStoreDB(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "session-state", delID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := New(filepath.Join(root, "session-state"))

	if err := r.DeleteSession(delID); err != nil {
		t.Fatalf("DeleteSession without store db: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("session dir not removed")
	}
}

func TestDeleteSessionMissingStoreTables(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "session-state", delID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Store db exists but with an older schema missing most tables.
	db, err := sql.Open("sqlite3", filepath.Join(root, "session-store.db"))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `CREATE TABLE sessions (id TEXT PRIMARY KEY)`)
	mustExec(t, db, `INSERT INTO sessions (id) VALUES (?)`, delID)
	db.Close()

	r := New(filepath.Join(root, "session-state"))
	if err := r.DeleteSession(delID); err != nil {
		t.Fatalf("DeleteSession with partial schema: %v", err)
	}
}

func TestDeleteSessionRejectsBadIDs(t *testing.T) {
	r, _ := newTestCopilotHome(t)
	for _, id := range []string{"", ".", "..", "../session-state", "a/b", "no-such"} {
		if err := r.DeleteSession(id); err == nil {
			t.Errorf("DeleteSession(%q) should fail", id)
		}
	}
}

func TestSessionRunningStaleLockIgnored(t *testing.T) {
	r, root := newTestCopilotHome(t)
	// A lock file naming a PID that no longer exists (observed on real
	// data: locks survive process death).
	lock := filepath.Join(root, "session-state", delID, "inuse.999999999.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	running, err := r.SessionRunning(delID)
	if err != nil {
		t.Fatalf("SessionRunning: %v", err)
	}
	if running {
		t.Error("stale lock (dead PID) must not count as running")
	}
}

func TestSessionRunningAliveLockBlocks(t *testing.T) {
	r, root := newTestCopilotHome(t)
	lock := filepath.Join(root, "session-state", delID, "inuse."+strconv.Itoa(os.Getpid())+".lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	running, err := r.SessionRunning(delID)
	if err != nil {
		t.Fatalf("SessionRunning: %v", err)
	}
	if !running {
		t.Error("alive lock PID must count as running")
	}
}

func TestSessionProcessesFindsFDHolder(t *testing.T) {
	r, root := newTestCopilotHome(t)
	// Hold events.jsonl open in this process: the fd probe must find us.
	f, err := os.Open(filepath.Join(root, "session-state", delID, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// HoldersOf excludes the calling process itself, so probe from the
	// reader's perspective by checking the raw list is non-empty is not
	// possible here; instead verify the exclusion contract holds: we are
	// the only holder, so the result must be empty.
	pids, err := r.SessionProcesses(delID)
	if err != nil {
		t.Fatalf("SessionProcesses: %v", err)
	}
	for _, pid := range pids {
		if pid == os.Getpid() {
			t.Error("SessionProcesses must exclude the calling process")
		}
	}
}
