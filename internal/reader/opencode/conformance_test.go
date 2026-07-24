package opencode

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/reader/adaptertest"

	_ "github.com/mattn/go-sqlite3"
)

// writeOpenCodeBasicFixture creates a test-owned SQLite DB with one session
// and a user/assistant message pair. Never opens the real OpenCode database.
func writeOpenCodeBasicFixture(t *testing.T) (dbPath, sessionID string) {
	t.Helper()
	dir := t.TempDir()
	dbPath = filepath.Join(dir, "opencode.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, stmt := range []string{
		`CREATE TABLE session (id text PRIMARY KEY, directory text NOT NULL DEFAULT '', title text NOT NULL DEFAULT '', time_created integer NOT NULL DEFAULT 0, time_updated integer NOT NULL DEFAULT 0, time_archived integer, model text, agent text)`,
		`CREATE TABLE message (id text PRIMARY KEY, session_id text NOT NULL, time_created integer NOT NULL DEFAULT 0, time_updated integer NOT NULL DEFAULT 0, data text NOT NULL)`,
		`CREATE TABLE part (id text PRIMARY KEY, message_id text NOT NULL, session_id text NOT NULL, time_created integer NOT NULL DEFAULT 0, time_updated integer NOT NULL DEFAULT 0, data text NOT NULL)`,
		`CREATE TABLE todo (session_id text NOT NULL, content text NOT NULL DEFAULT '', status text NOT NULL DEFAULT '', priority text NOT NULL DEFAULT '', position integer NOT NULL DEFAULT 0, time_created integer NOT NULL DEFAULT 0, time_updated integer NOT NULL DEFAULT 0, PRIMARY KEY(session_id, position))`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}

	sessionID = "ses_conformance_1"
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	modelJSON, _ := json.Marshal(map[string]string{"id": "test-model", "providerID": "test"})
	if _, err := db.Exec(
		`INSERT INTO session (id, directory, title, time_created, time_updated, model) VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, "/tmp/proj", "Conformance", now, now+1000, string(modelJSON),
	); err != nil {
		t.Fatal(err)
	}
	userData := `{"role":"user","content":"hello conformance"}`
	if _, err := db.Exec(
		`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		"msg_u1", sessionID, now, now, userData,
	); err != nil {
		t.Fatal(err)
	}
	asstData := `{"role":"assistant","parentID":"msg_u1","modelID":"test-model","providerID":"test","time":{"created":` + itoa(now+500) + `,"completed":` + itoa(now+900) + `}}`
	if _, err := db.Exec(
		`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		"msg_a1", sessionID, now+500, now+900, asstData,
	); err != nil {
		t.Fatal(err)
	}
	// Text part for assistant so replay has content.
	partData := `{"type":"text","text":"hi"}`
	if _, err := db.Exec(
		`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)`,
		"part_1", "msg_a1", sessionID, now+500, now+500, partData,
	); err != nil {
		t.Fatal(err)
	}
	return dbPath, sessionID
}

func itoa(n int64) string {
	return jsonNumber(n)
}

func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestOpenCodeConformance(t *testing.T) {
	dbPath, sessionID := writeOpenCodeBasicFixture(t)
	adaptertest.Run(t, adaptertest.Config{
		Capabilities: Capabilities(),
		NewReader: func(t *testing.T) adaptertest.Reader {
			r, err := New(dbPath)
			if err != nil {
				t.Fatalf("opencode.New: %v", err)
			}
			t.Cleanup(func() { _ = r.db.Close() })
			return r
		},
		Expect: adaptertest.Expectations{
			SessionCount: 1,
			SessionIDs:   []string{sessionID},
		},
	})
}
