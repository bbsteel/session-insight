package db

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	conn *sql.DB
}

func Open(dataDir string) (*DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}

	dbPath := filepath.Join(dataDir, "index.db")
	conn, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, err
	}

	if err := migrate(conn); err != nil {
		conn.Close()
		return nil, err
	}

	log.Printf("SQLite database opened at %s", dbPath)
	return &DB{conn: conn}, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) Conn() *sql.DB {
	return db.conn
}

func migrate(conn *sql.DB) error {
	query := `
	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		agent_type TEXT NOT NULL DEFAULT 'copilot',
		cwd TEXT NOT NULL DEFAULT '',
		repository TEXT NOT NULL DEFAULT '',
		branch TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL DEFAULT '',
		model_name TEXT NOT NULL DEFAULT '',
		turn_count INTEGER NOT NULL DEFAULT 0,
		message_count INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL DEFAULT ''
	);

	CREATE INDEX IF NOT EXISTS idx_sessions_agent ON sessions(agent_type);
	CREATE INDEX IF NOT EXISTS idx_sessions_created ON sessions(created_at DESC);
	`
	_, err := conn.Exec(query)
	return err
}
