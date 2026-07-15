package db

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
)

// oldAIGenerationsSchema is the pre-v17 table: three kinds, no freshness
// columns. Tests build this to simulate upgrading a real old database.
const oldAIGenerationsSchema = `
CREATE TABLE ai_generations (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    kind          TEXT NOT NULL CHECK (kind IN ('summary', 'title', 'handoff')),
    agent_type    TEXT NOT NULL,
    session_id    TEXT NOT NULL,
    provider_name TEXT NOT NULL DEFAULT '',
    model_id      TEXT NOT NULL DEFAULT '',
    content       TEXT NOT NULL,
    metadata      TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);`

// makeV16DB creates a raw index.db carrying the old ai_generations table with
// one seeded row and schema_migrations pinned at 16, then closes it. Returns
// the data dir so a subsequent db.Open runs the v17 migration against it.
func makeV16DB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	conn, err := sql.Open("sqlite3", filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(oldAIGenerationsSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO schema_migrations(version) VALUES (16)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(
		`INSERT INTO ai_generations(id, kind, agent_type, session_id, content, metadata, created_at)
		 VALUES (7, 'summary', 'copilot', 'sess-1', 'old content', '{"k":1}', '2026-01-01 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestV17FreshDBAcceptsInsight(t *testing.T) {
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	id, err := d.AddAIGeneration(AIGeneration{
		Kind: "insight", AgentType: "copilot", SessionID: "s", Content: "md",
		SourceRevision: 42, PromptVersion: "findings-insight-v1", SourceFingerprint: "fp",
	})
	if err != nil {
		t.Fatalf("insert insight into fresh db: %v", err)
	}
	got, err := d.LatestAIGeneration("insight", "copilot", "s")
	if err != nil || got == nil {
		t.Fatalf("read back insight: %v", err)
	}
	if got.ID != id || got.SourceRevision != 42 || got.PromptVersion != "findings-insight-v1" || got.SourceFingerprint != "fp" {
		t.Errorf("freshness fields not persisted: %+v", got)
	}
}

func TestV17UpgradeFromV16(t *testing.T) {
	dir := makeV16DB(t)
	d, err := Open(dir)
	if err != nil {
		t.Fatalf("open/migrate v16 db: %v", err)
	}
	defer d.Close()

	// Old row preserved verbatim: id, metadata, created_at.
	old, err := d.LatestAIGeneration("summary", "copilot", "sess-1")
	if err != nil || old == nil {
		t.Fatalf("old row lost after migration: %v", err)
	}
	if old.ID != 7 || old.Metadata != `{"k":1}` || old.CreatedAt != "2026-01-01 00:00:00" {
		t.Errorf("old row mutated by migration: %+v", old)
	}

	// insight kind now accepted.
	if _, err := d.AddAIGeneration(AIGeneration{Kind: "insight", AgentType: "copilot", SessionID: "s2", Content: "x"}); err != nil {
		t.Fatalf("insight kind rejected after migration: %v", err)
	}
	// Autoincrement continues past the preserved id (7).
	newID, err := d.AddAIGeneration(AIGeneration{Kind: "summary", AgentType: "copilot", SessionID: "s3", Content: "y"})
	if err != nil {
		t.Fatal(err)
	}
	if newID <= 7 {
		t.Errorf("autoincrement did not continue past preserved id: got %d", newID)
	}
}

func TestV17RollbackOnFailure(t *testing.T) {
	dir := makeV16DB(t)
	conn, err := sql.Open("sqlite3", filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Poison the rebuild: a conflicting ai_generations_new makes the first
	// CREATE fail, so the whole transaction must roll back the old table intact.
	if _, err := conn.Exec(`CREATE TABLE ai_generations_new (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if err := migrateAIGenerationsV17(conn); err == nil {
		t.Fatal("expected rebuild to fail on the conflicting table")
	}
	// Old table still usable and still rejecting the insight kind (unchanged).
	if _, err := conn.Exec(`INSERT INTO ai_generations(kind, agent_type, session_id, content) VALUES ('summary','copilot','s','ok')`); err != nil {
		t.Errorf("old table unusable after rollback: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO ai_generations(kind, agent_type, session_id, content) VALUES ('insight','copilot','s','no')`); err == nil {
		t.Error("old CHECK constraint should still reject insight after rollback")
	}
}

func TestV17ConcurrentOpenSelfHeals(t *testing.T) {
	dir := makeV16DB(t)
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d, err := Open(dir)
			if err != nil {
				errs[i] = err
				return
			}
			defer d.Close()
			_, errs[i] = d.AddAIGeneration(AIGeneration{Kind: "insight", AgentType: "c", SessionID: "s", Content: "x"})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent open %d failed: %v", i, err)
		}
	}
}
