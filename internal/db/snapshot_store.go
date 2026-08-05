package db

import (
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/bbsteel/session-insight/internal/model"
)

// SessionSnapshotWrite is one atomic index write: metadata + turns + provenance.
type SessionSnapshotWrite struct {
	AgentType           string
	Session             model.Session
	TurnCount           int
	HistoricalTurnCount int
	RolledBackTurnCount int
	MessageCount        int
	Turns               []TurnText
	Provenance          *model.SessionProvenance
	Revision            int64
}

// ReplaceSessionSnapshot commits session metadata, turn texts, watermark, and
// provenance in a single transaction so list/detail never see mixed revisions.
func (db *DB) ReplaceSessionSnapshot(w SessionSnapshotWrite) error {
	if w.AgentType == "" {
		w.AgentType = w.Session.AgentType
	}
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin snapshot tx: %w", err)
	}
	defer tx.Rollback()

	sess := w.Session
	_, err = tx.Exec(
		`INSERT INTO sessions(agent_type, id, cwd, repository, branch, project, name, model_name, model_provider, resume_id, parent_session_id, agent_path, is_subagent, turn_count, historical_turn_count, rolled_back_turn_count, message_count, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(agent_type, id) DO UPDATE SET
		     cwd = excluded.cwd,
		     repository = excluded.repository,
		     branch = excluded.branch,
		     project = excluded.project,
		     name = excluded.name,
		     model_name = excluded.model_name,
		     model_provider = excluded.model_provider,
		     resume_id = excluded.resume_id,
		     parent_session_id = excluded.parent_session_id,
		     agent_path = excluded.agent_path,
		     is_subagent = excluded.is_subagent,
		     turn_count = excluded.turn_count,
		     historical_turn_count = excluded.historical_turn_count,
		     rolled_back_turn_count = excluded.rolled_back_turn_count,
		     message_count = excluded.message_count,
		     created_at = excluded.created_at,
		     updated_at = excluded.updated_at`,
		w.AgentType, sess.ID, sess.CWD, sess.Repository, sess.Branch, sess.Project, sess.Name,
		sess.ModelName, sess.ModelProvider, sess.ResumeID, sess.ParentSessionID, sess.AgentPath, sess.IsSubagent,
		w.TurnCount, w.HistoricalTurnCount, w.RolledBackTurnCount, w.MessageCount,
		model.FormatTime(sess.CreatedAt),
		model.FormatTime(sess.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("upsert session meta: %w", err)
	}

	// Turn sync (same logic as UpsertTurns, in this tx).
	type rowKey struct {
		turnIndex int
		role      string
	}
	existing := make(map[rowKey]string)
	rows, err := tx.Query(
		`SELECT turn_index, role, content FROM turn_texts WHERE agent_type = ? AND session_id = ?`,
		w.AgentType, sess.ID,
	)
	if err != nil {
		return fmt.Errorf("query existing turns: %w", err)
	}
	for rows.Next() {
		var key rowKey
		var content string
		if err := rows.Scan(&key.turnIndex, &key.role, &content); err != nil {
			rows.Close()
			return fmt.Errorf("scan existing turns: %w", err)
		}
		existing[key] = content
	}
	if err := rows.Close(); err != nil {
		return err
	}

	desired := make(map[rowKey]string, len(w.Turns))
	for _, turn := range w.Turns {
		// Guard against invalid UTF-8 breaking FTS.
		content := turn.Content
		if !utf8.ValidString(content) {
			content = stringsToValidUTF8(content)
		}
		desired[rowKey{turn.TurnIndex, turn.Role}] = content
	}

	for key := range existing {
		if _, ok := desired[key]; !ok {
			if _, err := tx.Exec(
				`DELETE FROM turn_texts WHERE agent_type = ? AND session_id = ? AND turn_index = ? AND role = ?`,
				w.AgentType, sess.ID, key.turnIndex, key.role,
			); err != nil {
				return fmt.Errorf("delete removed turn: %w", err)
			}
		}
	}
	for key, content := range desired {
		if old, exists := existing[key]; exists {
			if old == content {
				continue
			}
			if _, err := tx.Exec(
				`UPDATE turn_texts SET content = ? WHERE agent_type = ? AND session_id = ? AND turn_index = ? AND role = ?`,
				content, w.AgentType, sess.ID, key.turnIndex, key.role,
			); err != nil {
				return fmt.Errorf("update changed turn: %w", err)
			}
			continue
		}
		if _, err := tx.Exec(
			`INSERT INTO turn_texts(agent_type, session_id, turn_index, role, content) VALUES (?, ?, ?, ?, ?)`,
			w.AgentType, sess.ID, key.turnIndex, key.role, content,
		); err != nil {
			return fmt.Errorf("insert new turn: %w", err)
		}
	}

	if _, err := tx.Exec(
		`INSERT INTO index_watermarks(agent_type, session_id, revision, indexed_at)
		 VALUES (?, ?, ?, datetime('now'))
		 ON CONFLICT(agent_type, session_id) DO UPDATE SET
		     revision   = excluded.revision,
		     indexed_at = excluded.indexed_at`,
		w.AgentType, sess.ID, w.Revision,
	); err != nil {
		return fmt.Errorf("set watermark: %w", err)
	}

	if w.Provenance != nil {
		p := *w.Provenance
		// Any successful snapshot write means the adapter read the source; clear
		// the tombstone clock so a prior source_missing does not stick forever.
		p.MissingSince = nil
		// Replayable captures also refresh last_successful_at.
		if p.State == model.RecordComplete || p.State == model.RecordDegraded {
			if p.LastSuccessfulAt == nil {
				t := p.CapturedAt
				if t.IsZero() {
					t = time.Now().UTC()
				}
				p.LastSuccessfulAt = &t
			}
		}
		if err := upsertProvenanceTx(tx, w.AgentType, sess.ID, p, w.Revision); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func stringsToValidUTF8(s string) string {
	return string([]rune(s))
}
