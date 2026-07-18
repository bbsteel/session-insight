package grok

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestDeleteSessionCleansFootprints(t *testing.T) {
	root := t.TempDir()
	// Simulate ~/.grok layout: sessions under root, active_sessions sibling.
	// New(sessionsDir) uses Dir(sessionsDir) as grokHome.
	sessions := filepath.Join(root, "sessions")
	home := root
	_ = home
	id := "del11111-bbbb-cccc-dddd-eeeeeeeeeeee"
	neighbor := "nbr11111-bbbb-cccc-dddd-eeeeeeeeeeee"
	writeSession(t, sessions, "proj", id, summaryFile{GeneratedTitle: "delete me"}, sampleUpdatesClosed(), "")
	writeSession(t, sessions, "proj", neighbor, summaryFile{GeneratedTitle: "keep me"}, sampleUpdatesClosed(), "")

	// prompt_history
	phPath := filepath.Join(sessions, "proj", "prompt_history.jsonl")
	ph := `{"session_id":"` + id + `","prompt":"a"}
{"session_id":"` + neighbor + `","prompt":"b"}
`
	if err := os.WriteFile(phPath, []byte(ph), 0o644); err != nil {
		t.Fatal(err)
	}

	// active_sessions.json at grok home (parent of sessions)
	act := []activeSession{
		{SessionID: id, PID: 1, CWD: "/tmp"},
		{SessionID: neighbor, PID: 2, CWD: "/tmp"},
	}
	ab, _ := json.Marshal(act)
	if err := os.WriteFile(filepath.Join(root, "active_sessions.json"), ab, 0o644); err != nil {
		t.Fatal(err)
	}

	// session_search.sqlite
	dbPath := filepath.Join(sessions, "session_search.sqlite")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// Real Grok DB also has FTS5 + DELETE triggers; purge only touches
	// session_docs (FTS follows via agent-owned triggers). Fixture skips FTS
	// so the test builds without the sqlite_fts5 CGO feature.
	_, err = db.Exec(`
CREATE TABLE session_docs (
  session_id TEXT PRIMARY KEY,
  cwd TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  title TEXT NOT NULL,
  content TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  last_indexed_offset INTEGER NOT NULL DEFAULT 0
);
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO session_docs(session_id,cwd,updated_at,title,content,content_hash)
VALUES (?,?,1,'t','c','h'),(?,?,1,'t2','c2','h2')`, id, "/tmp", neighbor, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	r := New(sessions)
	if err := r.DeleteSession(id); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	// Target gone, neighbor intact
	if _, err := os.Stat(filepath.Join(sessions, "proj", id)); !os.IsNotExist(err) {
		t.Error("target session dir should be gone")
	}
	if _, err := os.Stat(filepath.Join(sessions, "proj", neighbor, "summary.json")); err != nil {
		t.Error("neighbor session must remain")
	}

	// prompt_history
	data, _ := os.ReadFile(phPath)
	if string(data) != `{"session_id":"`+neighbor+`","prompt":"b"}`+"\n" {
		t.Errorf("prompt_history=%q", data)
	}

	// active_sessions
	adata, _ := os.ReadFile(filepath.Join(root, "active_sessions.json"))
	var left []activeSession
	if err := json.Unmarshal(adata, &left); err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].SessionID != neighbor {
		t.Errorf("active_sessions=%v", left)
	}

	// sqlite
	db, _ = sql.Open("sqlite3", dbPath)
	defer db.Close()
	var n int
	_ = db.QueryRow(`SELECT count(*) FROM session_docs WHERE session_id=?`, id).Scan(&n)
	if n != 0 {
		t.Error("session_docs row should be deleted")
	}
	_ = db.QueryRow(`SELECT count(*) FROM session_docs WHERE session_id=?`, neighbor).Scan(&n)
	if n != 1 {
		t.Error("neighbor session_docs must remain")
	}
}

func TestDeleteBadIDs(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "sessions")
	os.MkdirAll(sessions, 0o755)
	r := New(sessions)
	for _, id := range []string{"..", "../x", "a/b", "does-not-exist"} {
		if err := r.DeleteSession(id); err == nil {
			t.Errorf("DeleteSession(%q) should error", id)
		}
	}
}

func TestSessionProcessesDeadActiveEntryIgnored(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "sessions")
	id := "pid11111-bbbb-cccc-dddd-eeeeeeeeeeee"
	writeSession(t, sessions, "proj", id, summaryFile{}, sampleUpdatesClosed(), "")
	// Unlikely-to-be-alive PID
	act := []activeSession{{SessionID: id, PID: 999999999, CWD: "/tmp"}}
	ab, _ := json.Marshal(act)
	os.WriteFile(filepath.Join(root, "active_sessions.json"), ab, 0o644)

	r := New(sessions)
	pids, err := r.SessionProcesses(id)
	if err != nil {
		t.Fatalf("SessionProcesses: %v", err)
	}
	if len(pids) != 0 {
		t.Errorf("dead PID must be ignored, got %v", pids)
	}
}

func TestSessionLiveUsesActiveRegistry(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, "sessions")
	id := "live-session"
	writeSession(t, sessionsDir, "proj", id, summaryFile{}, sampleUpdatesClosed(), "")
	r := New(sessionsDir)

	entries := []activeSession{{SessionID: id, PID: os.Getpid()}}
	data, _ := json.Marshal(entries)
	if err := os.WriteFile(filepath.Join(root, "active_sessions.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	live, err := r.SessionLive(id)
	if err != nil || !live {
		t.Fatalf("SessionLive()=(%v, %v), want (true, nil)", live, err)
	}

	entries[0].PID = 99999999
	data, _ = json.Marshal(entries)
	if err := os.WriteFile(filepath.Join(root, "active_sessions.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	live, err = r.SessionLive(id)
	if err != nil || live {
		t.Fatalf("SessionLive()=(%v, %v), want (false, nil)", live, err)
	}
}
