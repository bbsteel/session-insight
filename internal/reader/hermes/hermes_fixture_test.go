package hermes

import (
	"database/sql"
	"embed"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// These fixtures are synthetic reductions of Hermes' state.db schema. The
// files contain no real paths, IDs, prompts, provider secrets, or user data.
//
//go:embed testdata/*.sql
var fixtureFiles embed.FS

func fixtureReader(t *testing.T, name string) *HermesReader {
	t.Helper()
	dbPath := fixtureDB(t, name)
	r, err := New(dbPath)
	if err != nil {
		t.Fatalf("New(%q): %v", name, err)
	}
	t.Cleanup(func() { _ = r.db.Close() })
	return r
}

func fixtureDB(t *testing.T, name string) string {
	t.Helper()
	data, err := fixtureFiles.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %q: %v", name, err)
	}
	dbPath := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	if _, err := db.Exec(string(data)); err != nil {
		_ = db.Close()
		t.Fatalf("load fixture %q: %v", name, err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close fixture db: %v", err)
	}
	return dbPath
}

func writableFixtureDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", sqliteDSN(path, "_busy_timeout=5000&_foreign_keys=on"))
	if err != nil {
		t.Fatalf("open writable fixture db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}
