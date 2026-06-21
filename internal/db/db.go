package db

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

const currentSchemaVersion = 2

type DB struct {
	conn *sql.DB
}

func Open(dataDir string) (*DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}

	dbPath := filepath.Join(dataDir, "index.db")
	conn, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
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

// RebuildFTS 重建 FTS5 内容索引，用于 tokenizer/schema 变更后强制同步。
func (db *DB) RebuildFTS() error {
	_, err := db.conn.Exec(`INSERT INTO turn_texts_fts(turn_texts_fts) VALUES ('rebuild')`)
	return err
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
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE INDEX IF NOT EXISTS idx_sessions_agent ON sessions(agent_type);
	CREATE INDEX IF NOT EXISTS idx_sessions_created ON sessions(created_at DESC);

	-- Schema version tracking（简单应用记录，非完整迁移框架）
	-- 当前行为：记录最近一次 schema 版本；不判断、不拒绝、不事务化回滚。
	CREATE TABLE IF NOT EXISTS schema_migrations (
	    version    INTEGER PRIMARY KEY,
	    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	-- Turn 内容表（含 role='meta' 用于会话名称搜索）
	CREATE TABLE IF NOT EXISTS turn_texts (
	    id         INTEGER PRIMARY KEY AUTOINCREMENT,
	    agent_type TEXT    NOT NULL,
	    session_id TEXT    NOT NULL,
	    turn_index INTEGER NOT NULL,
	    role       TEXT    NOT NULL,   -- 'user' | 'meta' ('assistant' reserved, not currently indexed)
	    content    TEXT    NOT NULL,
	    UNIQUE(agent_type, session_id, turn_index, role)
	);
	CREATE INDEX IF NOT EXISTS idx_turn_texts_agent_session
	    ON turn_texts(agent_type, session_id);

	-- FTS5 虚拟表（trigram tokenizer，与 LIKE 子串语义一致，中文友好）
	CREATE VIRTUAL TABLE IF NOT EXISTS turn_texts_fts
	    USING fts5(
	        content,
	        content="turn_texts",
	        content_rowid="id",
	        tokenize="trigram"
	    );

	-- 同步触发器
	CREATE TRIGGER IF NOT EXISTS turn_texts_ai
	    AFTER INSERT ON turn_texts BEGIN
	        INSERT INTO turn_texts_fts(rowid, content)
	            VALUES (new.id, new.content);
	    END;

	CREATE TRIGGER IF NOT EXISTS turn_texts_au
	    AFTER UPDATE ON turn_texts BEGIN
	        INSERT INTO turn_texts_fts(turn_texts_fts, rowid, content)
	            VALUES ('delete', old.id, old.content);
	        INSERT INTO turn_texts_fts(rowid, content)
	            VALUES (new.id, new.content);
	    END;

	CREATE TRIGGER IF NOT EXISTS turn_texts_ad
	    AFTER DELETE ON turn_texts BEGIN
	        INSERT INTO turn_texts_fts(turn_texts_fts, rowid, content)
	            VALUES ('delete', old.id, old.content);
	    END;

	-- Watermark：记录已索引会话的 revision（UpdatedAt.UnixNano）
	CREATE TABLE IF NOT EXISTS index_watermarks (
	    agent_type  TEXT    NOT NULL,
	    session_id  TEXT    NOT NULL,
	    revision    INTEGER NOT NULL DEFAULT 0,
	    indexed_at  TEXT    NOT NULL DEFAULT (datetime('now')),
	    PRIMARY KEY (agent_type, session_id)
	);
	`
	_, err := conn.Exec(query)
	if err != nil {
		return err
	}

	_, err = conn.Exec(
		`INSERT OR IGNORE INTO schema_migrations(version) VALUES (?)`,
		currentSchemaVersion,
	)
	return err
}



