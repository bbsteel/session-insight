package opencode

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
	"github.com/bbsteel/session-insight/internal/reader/capability"

	_ "github.com/mattn/go-sqlite3"
)

func TestOpenCodeCapabilityEvidence(t *testing.T) {
	dbPath, id := writeOpenCodeBasicFixture(t)
	adaptertest.RunFull(t, adaptertest.FullConfig{
		Config: adaptertest.Config{
			Capabilities: Capabilities(),
			NewReader: func(t *testing.T) adaptertest.Reader {
				r, err := New(dbPath)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = r.db.Close() })
				return r
			},
			Expect: adaptertest.Expectations{SessionCount: 1, SessionIDs: []string{id}},
		},
		Evidence: openCodeEvidenceCases(),
	})
}

func TestCapabilityEvidenceMatrix(t *testing.T) {
	rows := adaptertest.RunCapabilityEvidence(t, Capabilities(), openCodeEvidenceCases(), adaptertest.CoverageOptions{
		BasicSatisfied: adaptertest.DefaultBasicSatisfied(),
	})
	if len(rows) < 10 {
		t.Fatalf("matrix rows=%d", len(rows))
	}
}

func openCodeEvidenceCases() []adaptertest.EvidenceCase {
	return []adaptertest.EvidenceCase{
		{
			Scenario: "data-tokens-tools-diff-subtasks-resume", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{
				capability.CapabilityTokens, capability.CapabilityToolResults, capability.CapabilityDiff,
				capability.CapabilitySubtasks, capability.CapabilityResume,
			},
			NewReader: func(t *testing.T) adaptertest.Reader {
				path, _ := writeOpenCodeRichFixture(t)
				r, err := New(path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = r.db.Close() })
				return r
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				id := "ses_rich_1"
				adaptertest.AssertTokens(t, r, adaptertest.TokenExpect{
					SessionID: id, RequireNonNilBilling: true, RequireExactPrecision: true,
					ExactPrompt: adaptertest.Int64(100), ExactCompletion: adaptertest.Int64(50),
				})
				adaptertest.AssertToolResults(t, r, id, 1)
				adaptertest.AssertDiff(t, r, adaptertest.DiffExpect{
					SessionID: id, FilePathSub: "a.go", OldSub: "old", NewSub: "new",
				})
				adaptertest.AssertSubtasks(t, r, adaptertest.SubtaskExpect{SessionID: id, MinSubagents: 1})
				adaptertest.AssertResume(t, r, adaptertest.ResumeExpect{SessionID: id, ExactID: id})
			},
		},
		{
			Scenario: "realtime-mutation", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{capability.CapabilityRealtime},
			NewReader: func(t *testing.T) adaptertest.Reader {
				path, _ := writeOpenCodeBasicFixture(t)
				r, err := New(path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = r.db.Close() })
				return r
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				or := r.(*OpenCodeReader)
				id := "ses_conformance_1"
				adaptertest.AssertRealtimeStableThenMutate(t, r, id, func(t *testing.T) {
					// Touch DB by inserting a no-op message via writable connection.
					w, err := sql.Open("sqlite3", or.dbPath)
					if err != nil {
						t.Fatal(err)
					}
					defer w.Close()
					now := time.Now().UnixMilli()
					if _, err := w.Exec(
						`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
						"msg_extra", id, now, now, `{"role":"user","content":"x"}`,
					); err != nil {
						t.Fatal(err)
					}
				})
			},
		},
		{
			Scenario: "delete-sandbox", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{capability.CapabilityDelete},
			NewReader: func(t *testing.T) adaptertest.Reader {
				// Use delete-capable writer schema from opencode_delete_test
				r, db, _ := setupRealSchemaDB(t)
				seedFullSession(t, db, "ses_del", "")
				seedFullSession(t, db, "ses_keep", "")
				t.Cleanup(func() { _ = r.db.Close() })
				return r
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				adaptertest.AssertDeleteSandbox(t, r, "ses_del", "ses_keep")
			},
		},
	}
}

func writeOpenCodeRichFixture(t *testing.T) (dbPath, sessionID string) {
	t.Helper()
	dir := t.TempDir()
	dbPath = filepath.Join(dir, "opencode.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`CREATE TABLE session (id text PRIMARY KEY, directory text NOT NULL DEFAULT '', title text NOT NULL DEFAULT '', time_created integer NOT NULL DEFAULT 0, time_updated integer NOT NULL DEFAULT 0, time_archived integer, model text, agent text, parent_id text)`,
		`CREATE TABLE message (id text PRIMARY KEY, session_id text NOT NULL, time_created integer NOT NULL DEFAULT 0, time_updated integer NOT NULL DEFAULT 0, data text NOT NULL)`,
		`CREATE TABLE part (id text PRIMARY KEY, message_id text NOT NULL, session_id text NOT NULL, time_created integer NOT NULL DEFAULT 0, time_updated integer NOT NULL DEFAULT 0, data text NOT NULL)`,
		`CREATE TABLE todo (session_id text NOT NULL, content text NOT NULL DEFAULT '', status text NOT NULL DEFAULT '', priority text NOT NULL DEFAULT '', position integer NOT NULL DEFAULT 0, time_created integer NOT NULL DEFAULT 0, time_updated integer NOT NULL DEFAULT 0, PRIMARY KEY(session_id, position))`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	sessionID = "ses_rich_1"
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	modelJSON, _ := json.Marshal(map[string]string{"id": "test-model", "providerID": "test"})
	mustExec(t, db, `INSERT INTO session (id, directory, title, time_created, time_updated, model) VALUES (?,?,?,?,?,?)`,
		sessionID, "/tmp/proj", "Rich", now, now+5000, string(modelJSON))
	mustExec(t, db, `INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?,?,?,?,?)`,
		"msg_u1", sessionID, now, now, `{"role":"user","content":"edit"}`)
	asst := `{"role":"assistant","parentID":"msg_u1","modelID":"test-model","providerID":"test","agent":"build","tokens":{"input":100,"output":50,"reasoning":7,"cache":{"read":11,"write":3}},"time":{"created":` + jsonNumber(now+1) + `,"completed":` + jsonNumber(now+2) + `}}`
	mustExec(t, db, `INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?,?,?,?,?)`,
		"msg_a1", sessionID, now+1, now+2, asst)
	// tool + edit parts
	mustExec(t, db, `INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?,?,?,?,?,?)`,
		"p1", "msg_a1", sessionID, now+1, now+1, `{"type":"text","text":"working"}`)
	mustExec(t, db, `INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?,?,?,?,?,?)`,
		"p2", "msg_a1", sessionID, now+1, now+1, `{"type":"tool","tool":"edit","callID":"call1","state":{"status":"completed","input":{"filePath":"a.go","oldString":"old","newString":"new"},"output":"ok"}}`)
	// agent name for subtasks
	mustExec(t, db, `INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?,?,?,?,?,?)`,
		"p3", "msg_a1", sessionID, now+1, now+1, `{"type":"agent","name":"explore"}`)
	// child session for parent_id tree
	mustExec(t, db, `INSERT INTO session (id, parent_id, directory, title, time_created, time_updated) VALUES (?,?,?,?,?,?)`,
		"ses_child_1", sessionID, "/tmp/proj", "child", now, now)
	return dbPath, sessionID
}
