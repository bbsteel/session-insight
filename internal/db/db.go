package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

const currentSchemaVersion = 16

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

	-- MiniMap 位点缓存 header（每个 agent_type+session+revision+cols 一条）
	CREATE TABLE IF NOT EXISTS session_position_caches (
	    agent_type   TEXT    NOT NULL,
	    session_id   TEXT    NOT NULL,
	    revision     INTEGER NOT NULL,
	    cols         INTEGER NOT NULL,
	    total_lines  INTEGER NOT NULL,
	    generated_at TEXT    NOT NULL DEFAULT (datetime('now')),
	    PRIMARY KEY (agent_type, session_id, revision, cols)
	);

	-- MiniMap 关键位点（通过 FK 级联依赖 header）
	CREATE TABLE IF NOT EXISTS session_positions (
	    agent_type   TEXT    NOT NULL,
	    session_id   TEXT    NOT NULL,
	    revision     INTEGER NOT NULL,
	    cols         INTEGER NOT NULL,
	    position_key TEXT    NOT NULL,
	    kind         TEXT    NOT NULL CHECK (kind IN ('turn', 'user', 'compaction', 'error', 'edit', 'fold', 'trunc', 'tool')),
	    turn_index   INTEGER NOT NULL,
	    line_start   INTEGER NOT NULL,
	    line_end     INTEGER,
	    label        TEXT    NOT NULL DEFAULT '',
	    severity     TEXT    NOT NULL DEFAULT '',
	    payload_json TEXT    NOT NULL DEFAULT '{}',
	    PRIMARY KEY (agent_type, session_id, revision, cols, position_key),
	    FOREIGN KEY (agent_type, session_id, revision, cols)
	        REFERENCES session_position_caches(agent_type, session_id, revision, cols)
	        ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_session_positions_lookup
	    ON session_positions(agent_type, session_id, revision, cols, line_start);

	CREATE TABLE IF NOT EXISTS bookmarked_sessions (
	    agent_type TEXT NOT NULL,
	    session_id TEXT NOT NULL,
	    created_at TEXT NOT NULL DEFAULT (datetime('now')),
	    PRIMARY KEY (agent_type, session_id)
	);

	CREATE TABLE IF NOT EXISTS app_settings (
	    key TEXT PRIMARY KEY,
	    value TEXT NOT NULL
	);

	-- LLM provider 配置（api 型 = OpenAI 兼容 HTTP；acp 型 = 本地 CLI 走 ACP 协议）
	CREATE TABLE IF NOT EXISTS llm_providers (
	    id          INTEGER PRIMARY KEY AUTOINCREMENT,
	    name        TEXT NOT NULL,
	    kind        TEXT NOT NULL CHECK (kind IN ('api', 'acp')),
	    base_url    TEXT NOT NULL DEFAULT '',
	    api_key     TEXT NOT NULL DEFAULT '',
	    agent       TEXT NOT NULL DEFAULT '',
	    model_id    TEXT NOT NULL,
	    model_label TEXT NOT NULL DEFAULT '',
	    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
	);

	-- AI 生成历史（summary / title / handoff 共用一张表）
	CREATE TABLE IF NOT EXISTS ai_generations (
	    id            INTEGER PRIMARY KEY AUTOINCREMENT,
	    kind          TEXT NOT NULL CHECK (kind IN ('summary', 'title', 'handoff')),
	    agent_type    TEXT NOT NULL,
	    session_id    TEXT NOT NULL,
	    provider_name TEXT NOT NULL DEFAULT '',
	    model_id      TEXT NOT NULL DEFAULT '',
	    content       TEXT NOT NULL,
	    metadata      TEXT NOT NULL DEFAULT '',
	    created_at    TEXT NOT NULL DEFAULT (datetime('now'))
	);
	CREATE INDEX IF NOT EXISTS idx_ai_generations_session
	    ON ai_generations(agent_type, session_id, kind);

	-- LLM 生成的标题覆盖：只影响本应用显示，不碰 agent 原始日志文件
	CREATE TABLE IF NOT EXISTS session_title_overrides (
	    agent_type TEXT NOT NULL,
	    session_id TEXT NOT NULL,
	    title      TEXT NOT NULL,
	    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
	    PRIMARY KEY (agent_type, session_id)
	);
	`
	_, err := conn.Exec(query)
	if err != nil {
		return err
	}

	// Version 4: 'edit' kind added to position constraint.
	// Drop position cache tables so they're recreated with the new schema
	// on next positions request (they're pure caches, safe to discard).
	var maxVersion int
	conn.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&maxVersion)
	if maxVersion < 4 {
		conn.Exec(`DROP TABLE IF EXISTS session_positions`)
		conn.Exec(`DROP TABLE IF EXISTS session_position_caches`)
		conn.Exec(`
		CREATE TABLE IF NOT EXISTS session_position_caches (
		    agent_type   TEXT    NOT NULL,
		    session_id   TEXT    NOT NULL,
		    revision     INTEGER NOT NULL,
		    cols         INTEGER NOT NULL,
		    total_lines  INTEGER NOT NULL,
		    generated_at TEXT    NOT NULL DEFAULT (datetime('now')),
		    PRIMARY KEY (agent_type, session_id, revision, cols)
		)`)
		conn.Exec(`
		CREATE TABLE IF NOT EXISTS session_positions (
		    agent_type   TEXT    NOT NULL,
		    session_id   TEXT    NOT NULL,
		    revision     INTEGER NOT NULL,
		    cols         INTEGER NOT NULL,
		    position_key TEXT    NOT NULL,
		    kind         TEXT    NOT NULL CHECK (kind IN ('turn', 'user', 'compaction', 'error', 'edit')),
		    turn_index   INTEGER NOT NULL,
		    line_start   INTEGER NOT NULL,
		    line_end     INTEGER,
		    label        TEXT    NOT NULL DEFAULT '',
		    severity     TEXT    NOT NULL DEFAULT '',
		    payload_json TEXT    NOT NULL DEFAULT '{}',
		    PRIMARY KEY (agent_type, session_id, revision, cols, position_key),
		    FOREIGN KEY (agent_type, session_id, revision, cols)
		        REFERENCES session_position_caches(agent_type, session_id, revision, cols)
		        ON DELETE CASCADE
		)`)
		conn.Exec(`
		CREATE INDEX IF NOT EXISTS idx_session_positions_lookup
		    ON session_positions(agent_type, session_id, revision, cols, line_start)`)
	}

	// Version 6: 'fold' / 'trunc' kinds added to the position constraint
	// (collapsible tool groups + truncated-output expansion). Same pattern:
	// the tables are pure caches, drop and let the next request rebuild them
	// with the widened CHECK.
	if maxVersion < 6 {
		conn.Exec(`DROP TABLE IF EXISTS session_positions`)
		conn.Exec(`DROP TABLE IF EXISTS session_position_caches`)
		conn.Exec(`
		CREATE TABLE IF NOT EXISTS session_position_caches (
		    agent_type   TEXT    NOT NULL,
		    session_id   TEXT    NOT NULL,
		    revision     INTEGER NOT NULL,
		    cols         INTEGER NOT NULL,
		    total_lines  INTEGER NOT NULL,
		    generated_at TEXT    NOT NULL DEFAULT (datetime('now')),
		    PRIMARY KEY (agent_type, session_id, revision, cols)
		)`)
		conn.Exec(`
		CREATE TABLE IF NOT EXISTS session_positions (
		    agent_type   TEXT    NOT NULL,
		    session_id   TEXT    NOT NULL,
		    revision     INTEGER NOT NULL,
		    cols         INTEGER NOT NULL,
		    position_key TEXT    NOT NULL,
		    kind         TEXT    NOT NULL CHECK (kind IN ('turn', 'user', 'compaction', 'error', 'edit', 'fold', 'trunc')),
		    turn_index   INTEGER NOT NULL,
		    line_start   INTEGER NOT NULL,
		    line_end     INTEGER,
		    label        TEXT    NOT NULL DEFAULT '',
		    severity     TEXT    NOT NULL DEFAULT '',
		    payload_json TEXT    NOT NULL DEFAULT '{}',
		    PRIMARY KEY (agent_type, session_id, revision, cols, position_key),
		    FOREIGN KEY (agent_type, session_id, revision, cols)
		        REFERENCES session_position_caches(agent_type, session_id, revision, cols)
		        ON DELETE CASCADE
		)`)
		conn.Exec(`
		CREATE INDEX IF NOT EXISTS idx_session_positions_lookup
		    ON session_positions(agent_type, session_id, revision, cols, line_start)`)
	}

	// Version 9: 'tool' kind added to the position constraint (tool-call
	// panel entries). Same pattern: pure caches, drop and rebuild on the
	// next positions request with the widened CHECK. Runs after the v6
	// block so any older DB (including fresh ones the v4/v6 blocks just
	// rebuilt with the narrower CHECK) ends up with the current schema.
	if maxVersion < 9 {
		conn.Exec(`DROP TABLE IF EXISTS session_positions`)
		conn.Exec(`DROP TABLE IF EXISTS session_position_caches`)
		conn.Exec(`
		CREATE TABLE IF NOT EXISTS session_position_caches (
		    agent_type   TEXT    NOT NULL,
		    session_id   TEXT    NOT NULL,
		    revision     INTEGER NOT NULL,
		    cols         INTEGER NOT NULL,
		    total_lines  INTEGER NOT NULL,
		    generated_at TEXT    NOT NULL DEFAULT (datetime('now')),
		    PRIMARY KEY (agent_type, session_id, revision, cols)
		)`)
		conn.Exec(`
		CREATE TABLE IF NOT EXISTS session_positions (
		    agent_type   TEXT    NOT NULL,
		    session_id   TEXT    NOT NULL,
		    revision     INTEGER NOT NULL,
		    cols         INTEGER NOT NULL,
		    position_key TEXT    NOT NULL,
		    kind         TEXT    NOT NULL CHECK (kind IN ('turn', 'user', 'compaction', 'error', 'edit', 'fold', 'trunc', 'tool')),
		    turn_index   INTEGER NOT NULL,
		    line_start   INTEGER NOT NULL,
		    line_end     INTEGER,
		    label        TEXT    NOT NULL DEFAULT '',
		    severity     TEXT    NOT NULL DEFAULT '',
		    payload_json TEXT    NOT NULL DEFAULT '{}',
		    PRIMARY KEY (agent_type, session_id, revision, cols, position_key),
		    FOREIGN KEY (agent_type, session_id, revision, cols)
		        REFERENCES session_position_caches(agent_type, session_id, revision, cols)
		        ON DELETE CASCADE
		)`)
		conn.Exec(`
		CREATE INDEX IF NOT EXISTS idx_session_positions_lookup
		    ON session_positions(agent_type, session_id, revision, cols, line_start)`)
	}

	// Version 7: expand bookmarked_sessions with session metadata so
	// listing bookmarks is a pure SQL query (no per-session disk reads).
	if maxVersion < 7 {
		for _, col := range []string{
			`name TEXT NOT NULL DEFAULT ''`,
			`model_name TEXT NOT NULL DEFAULT ''`,
			`repository TEXT NOT NULL DEFAULT ''`,
			`project TEXT NOT NULL DEFAULT ''`,
			`cwd TEXT NOT NULL DEFAULT ''`,
			`preview_text TEXT NOT NULL DEFAULT ''`,
			`turn_count INTEGER NOT NULL DEFAULT 0`,
			`message_count INTEGER NOT NULL DEFAULT 0`,
			`branch TEXT NOT NULL DEFAULT ''`,
			`session_updated_at TEXT NOT NULL DEFAULT ''`,
		} {
			conn.Exec(`ALTER TABLE bookmarked_sessions ADD COLUMN ` + col)
		}
	}

	// Version 14: structured metadata for AI generations (handoff difficulty
	// assessment + recommended executor list, JSON text). The CREATE TABLE
	// above already includes the column for fresh DBs. Gate on the actual
	// column, not schema_migrations: the DB is shared with other running
	// instances (main + worktree validation), and an ALTER can lose the lock
	// race on one startup while the version row still gets written — checking
	// the real schema makes the migration self-healing on the next start.
	hasMetadata := false
	if rows, err := conn.Query(`PRAGMA table_info(ai_generations)`); err == nil {
		for rows.Next() {
			var cid int
			var name, typ string
			var notnull, pk int
			var dflt any
			if rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk) == nil && name == "metadata" {
				hasMetadata = true
			}
		}
		rows.Close()
	}
	if !hasMetadata {
		if _, err := conn.Exec(`ALTER TABLE ai_generations ADD COLUMN metadata TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add ai_generations.metadata column: %w", err)
		}
	}

	// Version 8: rebuild sessions table with correct composite key and project
	// column. The old table was never populated (no code path wrote to it), so
	// DROP + CREATE is safe. Also clear index watermarks so the indexer
	// repopulates the sessions table on next startup.
	if maxVersion < 8 {
		conn.Exec(`DROP TABLE IF EXISTS sessions`)
		conn.Exec(`
		CREATE TABLE sessions (
		    agent_type TEXT NOT NULL DEFAULT 'copilot',
		    id TEXT NOT NULL,
		    cwd TEXT NOT NULL DEFAULT '',
		    repository TEXT NOT NULL DEFAULT '',
		    branch TEXT NOT NULL DEFAULT '',
		    project TEXT NOT NULL DEFAULT '',
		    name TEXT NOT NULL DEFAULT '',
		    model_name TEXT NOT NULL DEFAULT '',
		    turn_count INTEGER NOT NULL DEFAULT 0,
		    message_count INTEGER NOT NULL DEFAULT 0,
		    created_at TEXT NOT NULL DEFAULT (datetime('now')),
		    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
		    PRIMARY KEY (agent_type, id)
		)`)
		conn.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_agent ON sessions(agent_type)`)
		conn.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_created ON sessions(created_at DESC)`)
		// Clear watermarks so the indexer backfills session metadata.
		conn.Exec(`DELETE FROM index_watermarks`)
	}

	// Version 10: sessions gains resume_id so /api/sessions can be served
	// entirely from SQLite. Clear codex watermarks to backfill the column
	// (codex is the only reader that populates ResumeID today).
	if maxVersion < 10 {
		conn.Exec(`ALTER TABLE sessions ADD COLUMN resume_id TEXT NOT NULL DEFAULT ''`)
		conn.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_updated ON sessions(updated_at DESC)`)
		conn.Exec(`DELETE FROM index_watermarks WHERE agent_type = 'codex'`)
	}

	// Version 15: distinguish the active resumable turn count from turns that
	// remain in an append-only transcript after an explicit rollback.
	if maxVersion < 15 {
		for _, col := range []string{
			`historical_turn_count INTEGER NOT NULL DEFAULT 0`,
			`rolled_back_turn_count INTEGER NOT NULL DEFAULT 0`,
		} {
			if _, err := conn.Exec(`ALTER TABLE sessions ADD COLUMN ` + col); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
				return fmt.Errorf("add sessions rollback count: %w", err)
			}
		}
		conn.Exec(`DELETE FROM index_watermarks WHERE agent_type = 'codex'`)
	}

	// Version 16: preserve Codex collaborative-agent lineage. Child rollout
	// files remain indexed/searchable but no longer masquerade as duplicate
	// root sessions in the sidebar.
	if maxVersion < 16 {
		for _, col := range []string{
			`parent_session_id TEXT NOT NULL DEFAULT ''`,
			`agent_path TEXT NOT NULL DEFAULT ''`,
			`is_subagent INTEGER NOT NULL DEFAULT 0`,
		} {
			if _, err := conn.Exec(`ALTER TABLE sessions ADD COLUMN ` + col); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
				return fmt.Errorf("add sessions lineage column: %w", err)
			}
		}
		conn.Exec(`DELETE FROM index_watermarks WHERE agent_type = 'codex'`)
	}

	_, err = conn.Exec(
		`INSERT OR IGNORE INTO schema_migrations(version) VALUES (?)`,
		currentSchemaVersion,
	)
	return err
}
