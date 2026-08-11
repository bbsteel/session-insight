package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestOpen(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer database.Close()

	dbPath := filepath.Join(dir, "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatal("index.db was not created")
	}
}

func TestUpdateSessionResumeID(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer database.Close()

	if _, err := database.Conn().Exec(`
		INSERT INTO sessions(agent_type, id, resume_id)
		VALUES ('codex', 'rollout-file', 'parent-id');
	`); err != nil {
		t.Fatalf("prepare session: %v", err)
	}
	changed, err := database.UpdateSessionResumeID("codex", "rollout-file", "child-id")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected resume id update")
	}
	var got string
	if err := database.Conn().QueryRow(`SELECT resume_id FROM sessions WHERE agent_type='codex' AND id='rollout-file'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "child-id" {
		t.Fatalf("resume id = %q, want child-id", got)
	}
	changed, err = database.UpdateSessionResumeID("codex", "rollout-file", "child-id")
	if err != nil || changed {
		t.Fatalf("idempotent update: changed=%v err=%v", changed, err)
	}
}

func TestV33NormalizesPathFormProjects(t *testing.T) {
	// Drive v33 through Open: pin schema_migrations at 32 (do not DELETE only
	// the current version row — that resets maxVersion to 0 and re-runs v8
	// DROP sessions). Insert path-form projects, then reopen.
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO sessions(agent_type, id, project) VALUES
			('grok', 's1', '/home/deck/projects/session-insight/'),
			('grok', 's2', '/home/deck/projects/lego-lookup/'),
			('claude', 's3', 'session-insight'),
			('copilot', 's4', 'owner/repo');
		DELETE FROM schema_migrations;
		INSERT INTO schema_migrations(version) VALUES (32);
	`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	var ver int
	if err := reopened.Conn().QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&ver); err != nil {
		t.Fatal(err)
	}
	if ver != 33 {
		t.Fatalf("schema version = %d, want 33", ver)
	}

	want := map[string]string{
		"s1": "session-insight",
		"s2": "lego-lookup",
		"s3": "session-insight",
		"s4": "owner/repo",
	}
	for id, project := range want {
		var got string
		if err := reopened.Conn().QueryRow(
			`SELECT project FROM sessions WHERE id = ?`, id,
		).Scan(&got); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if got != project {
			t.Fatalf("%s project = %q, want %q", id, got, project)
		}
	}
}

func TestPathFormProjectBasename(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/home/deck/projects/session-insight/", "session-insight"},
		{"/home/deck/projects/lego-lookup", "lego-lookup"},
		{`C:\Users\me\projects\foo\`, "foo"},
		{"C:/Users/me/projects/bar", "bar"},
		{"~/projects/x", "x"},
		{"~", "~"},
		{"owner/repo", "repo"}, // only used when the row already matched path-form filters
		{"", ""},
	}
	for _, c := range cases {
		if got := pathFormProjectBasename(c.in); got != c.want {
			t.Errorf("pathFormProjectBasename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRefreshSessionListMetadata(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer database.Close()

	if _, err := database.Conn().Exec(`
		INSERT INTO sessions(agent_type, id, project, resume_id)
		VALUES ('grok', 's1', '/home/deck/projects/session-insight/', 'old-resume');
	`); err != nil {
		t.Fatalf("prepare session: %v", err)
	}

	// Normalize full-path project and refresh resume_id in one lightweight pass.
	changed, err := database.RefreshSessionListMetadata("grok", model.Session{
		ID:       "s1",
		Project:  "session-insight",
		ResumeID: "native-id",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected list metadata update")
	}
	var project, resumeID string
	if err := database.Conn().QueryRow(
		`SELECT project, resume_id FROM sessions WHERE agent_type='grok' AND id='s1'`,
	).Scan(&project, &resumeID); err != nil {
		t.Fatal(err)
	}
	if project != "session-insight" || resumeID != "native-id" {
		t.Fatalf("got project=%q resume_id=%q", project, resumeID)
	}

	// Empty resumeID must not wipe the stored native ID.
	changed, err = database.RefreshSessionListMetadata("grok", model.Session{
		ID:      "s1",
		Project: "session-insight",
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("expected no-op when project matches and resume is empty")
	}
	if err := database.Conn().QueryRow(
		`SELECT resume_id FROM sessions WHERE agent_type='grok' AND id='s1'`,
	).Scan(&resumeID); err != nil {
		t.Fatal(err)
	}
	if resumeID != "native-id" {
		t.Fatalf("empty resume wiped stored id: %q", resumeID)
	}

	// Empty project (imported list projection) must not wipe a stored project.
	changed, err = database.RefreshSessionListMetadata("grok", model.Session{
		ID:       "s1",
		Project:  "",
		ResumeID: "native-id",
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("expected no-op when list project is empty")
	}
	if err := database.Conn().QueryRow(
		`SELECT project FROM sessions WHERE agent_type='grok' AND id='s1'`,
	).Scan(&project); err != nil {
		t.Fatal(err)
	}
	if project != "session-insight" {
		t.Fatalf("empty list project wiped stored project: %q", project)
	}

	// Idempotent when already normalized.
	changed, err = database.RefreshSessionListMetadata("grok", model.Session{
		ID:       "s1",
		Project:  "session-insight",
		ResumeID: "native-id",
	})
	if err != nil || changed {
		t.Fatalf("idempotent refresh: changed=%v err=%v", changed, err)
	}
}

func TestV30MigrationInvalidatesExistingWatermarks(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO index_watermarks(agent_type, session_id, revision, indexed_at)
		VALUES ('claude', 'legacy', 42, '2026-08-05T00:00:00Z')`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.Conn().Exec(`DELETE FROM schema_migrations WHERE version = 30`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.Conn().Exec(`DROP TABLE session_provenance`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var count int
	if err := reopened.Conn().QueryRow(`SELECT COUNT(*) FROM index_watermarks`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("v30 migration retained %d watermark(s)", count)
	}
}
