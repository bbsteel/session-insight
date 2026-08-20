package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSnippetStorePersistsSourceAndContent(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	turnIndex := 3
	created, err := database.AddSnippet(Snippet{
		Content: "Keep this useful conclusion.", AgentType: "codex", SessionID: "session-1",
		SessionName: "Snippet design", Project: "session-insight", SourceKind: "assistant", TurnIndex: &turnIndex,
	})
	if err != nil {
		t.Fatalf("AddSnippet: %v", err)
	}
	if created.ID == 0 || created.Content != "Keep this useful conclusion." || created.Project != "session-insight" || created.TurnIndex == nil || *created.TurnIndex != 3 {
		t.Fatalf("unexpected created snippet: %+v", created)
	}

	list, err := database.ListSnippets()
	if err != nil {
		t.Fatalf("ListSnippets: %v", err)
	}
	if len(list) != 1 || list[0].SessionName != "Snippet design" || list[0].Project != "session-insight" {
		t.Fatalf("unexpected snippets: %+v", list)
	}
	deleted, err := database.DeleteSnippet(created.ID)
	if err != nil || !deleted {
		t.Fatalf("DeleteSnippet: deleted=%v err=%v", deleted, err)
	}
	deleted, err = database.DeleteSnippet(created.ID)
	if err != nil || deleted {
		t.Fatalf("second DeleteSnippet: deleted=%v err=%v", deleted, err)
	}
}

func TestV42BackfillsSnippetProjectFromSourceSession(t *testing.T) {
	dir := t.TempDir()
	connection, err := sql.Open("sqlite3", filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	_, err = connection.Exec(`
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		INSERT INTO schema_migrations(version) VALUES (41);
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			agent_type TEXT NOT NULL DEFAULT 'codex',
			cwd TEXT NOT NULL DEFAULT '',
			repository TEXT NOT NULL DEFAULT '',
			branch TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			model_name TEXT NOT NULL DEFAULT '',
			model_provider TEXT NOT NULL DEFAULT '',
			project TEXT NOT NULL DEFAULT '',
			turn_count INTEGER NOT NULL DEFAULT 0,
			message_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		INSERT INTO sessions(id, agent_type, project) VALUES ('session-1', 'codex', 'session-insight');
		CREATE TABLE snippets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			content TEXT NOT NULL,
			agent_type TEXT NOT NULL,
			session_id TEXT NOT NULL,
			session_name TEXT NOT NULL DEFAULT '',
			source_kind TEXT NOT NULL CHECK (source_kind IN ('selection', 'assistant')),
			turn_index INTEGER,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		INSERT INTO snippets(content, agent_type, session_id, source_kind)
		VALUES ('legacy excerpt', 'codex', 'session-1', 'selection');
	`)
	if err != nil {
		connection.Close()
		t.Fatalf("seed legacy database: %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	database, err := Open(dir)
	if err != nil {
		t.Fatalf("open/migrate legacy database: %v", err)
	}
	defer database.Close()

	snippet, err := database.GetSnippet(1)
	if err != nil {
		t.Fatalf("read migrated snippet: %v", err)
	}
	if snippet.Project != "session-insight" {
		t.Fatalf("migrated snippet project = %q, want session-insight", snippet.Project)
	}
}
