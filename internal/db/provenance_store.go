package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// GetProvenance loads a stored provenance snapshot. ok is false when no row
// exists (legacy session never snapshotted) — callers must report unavailable,
// never invent complete.
func (db *DB) GetProvenance(agentType, sessionID string) (p *model.SessionProvenance, ok bool, err error) {
	var (
		state, reason, capturedAt string
		sourceUpdated, lastOK, missingSince sql.NullString
		adapterRev, revision                int
		sourcesJSON, warningsJSON, summaryJSON string
	)
	err = db.conn.QueryRow(`
		SELECT state, reason_code, captured_at, source_updated_at, adapter_revision,
		       sources_json, warnings_json, warning_summary_json,
		       last_successful_at, missing_since, revision
		FROM session_provenance
		WHERE agent_type = ? AND session_id = ?`,
		agentType, sessionID,
	).Scan(
		&state, &reason, &capturedAt, &sourceUpdated, &adapterRev,
		&sourcesJSON, &warningsJSON, &summaryJSON,
		&lastOK, &missingSince, &revision,
	)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get provenance: %w", err)
	}
	prov, err := decodeProvenance(state, reason, capturedAt, sourceUpdated, adapterRev,
		sourcesJSON, warningsJSON, summaryJSON, lastOK, missingSince)
	if err != nil {
		return nil, false, err
	}
	return prov, true, nil
}

// ListProvenanceStatus returns compact record status for many sessions.
// Missing rows are omitted from the map (caller treats as unavailable).
func (db *DB) ListProvenanceStatus(agentType string) (map[string]*model.RecordStatus, error) {
	query := `
		SELECT session_id, state, captured_at, warning_summary_json
		FROM session_provenance`
	var args []any
	if agentType != "" {
		query += ` WHERE agent_type = ?`
		args = append(args, agentType)
	}
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list provenance status: %w", err)
	}
	defer rows.Close()

	out := make(map[string]*model.RecordStatus)
	for rows.Next() {
		var sessionID, state, capturedAt, summaryJSON string
		if err := rows.Scan(&sessionID, &state, &capturedAt, &summaryJSON); err != nil {
			return nil, err
		}
		var summary model.WarningSummary
		if summaryJSON != "" && summaryJSON != "{}" {
			if err := json.Unmarshal([]byte(summaryJSON), &summary); err != nil {
				// Corrupt summary must not white-screen the list.
				summary = model.WarningSummary{}
			}
		}
		captured, _ := time.Parse(time.RFC3339, capturedAt)
		keyAgent := agentType
		if keyAgent == "" {
			// When listing all agents we need agent_type in the key — re-query pattern below.
		}
		_ = keyAgent
		out[sessionID] = &model.RecordStatus{
			State:        model.RecordCompletenessState(state),
			WarningCount: summary.Total,
			CapturedAt:   captured,
		}
	}
	return out, rows.Err()
}

// ListProvenanceStatusByAgent returns compact status keyed by agent_type\x00session_id.
func (db *DB) ListProvenanceStatusByAgent(agentType string) (map[string]*model.RecordStatus, error) {
	query := `
		SELECT agent_type, session_id, state, captured_at, warning_summary_json
		FROM session_provenance`
	var args []any
	if agentType != "" {
		query += ` WHERE agent_type = ?`
		args = append(args, agentType)
	}
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list provenance status: %w", err)
	}
	defer rows.Close()

	out := make(map[string]*model.RecordStatus)
	for rows.Next() {
		var aType, sessionID, state, capturedAt, summaryJSON string
		if err := rows.Scan(&aType, &sessionID, &state, &capturedAt, &summaryJSON); err != nil {
			return nil, err
		}
		var summary model.WarningSummary
		if summaryJSON != "" && summaryJSON != "{}" {
			_ = json.Unmarshal([]byte(summaryJSON), &summary)
		}
		captured, _ := time.Parse(time.RFC3339, capturedAt)
		out[aType+"\x00"+sessionID] = &model.RecordStatus{
			State:        model.RecordCompletenessState(state),
			WarningCount: summary.Total,
			CapturedAt:   captured,
		}
	}
	return out, rows.Err()
}

// UpsertProvenance writes or replaces a provenance snapshot (standalone).
// Prefer ReplaceSessionSnapshot for atomic meta+turns+provenance writes.
func (db *DB) UpsertProvenance(agentType, sessionID string, p model.SessionProvenance, revision int64) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := upsertProvenanceTx(tx, agentType, sessionID, p, revision); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertProvenanceTx(tx *sql.Tx, agentType, sessionID string, p model.SessionProvenance, revision int64) error {
	sourcesJSON, err := json.Marshal(p.Sources)
	if err != nil {
		return fmt.Errorf("marshal sources: %w", err)
	}
	if sourcesJSON == nil {
		sourcesJSON = []byte("[]")
	}
	warningsJSON, err := json.Marshal(p.Warnings)
	if err != nil {
		return fmt.Errorf("marshal warnings: %w", err)
	}
	if warningsJSON == nil {
		warningsJSON = []byte("[]")
	}
	summaryJSON, err := json.Marshal(p.WarningSummary)
	if err != nil {
		return fmt.Errorf("marshal warning summary: %w", err)
	}
	if summaryJSON == nil {
		summaryJSON = []byte("{}")
	}

	var sourceUpdated, lastOK, missing *string
	if p.SourceUpdatedAt != nil {
		s := model.FormatTime(*p.SourceUpdatedAt)
		sourceUpdated = &s
	}
	if p.LastSuccessfulAt != nil {
		s := model.FormatTime(*p.LastSuccessfulAt)
		lastOK = &s
	}
	if p.MissingSince != nil {
		s := model.FormatTime(*p.MissingSince)
		missing = &s
	}

	_, err = tx.Exec(`
		INSERT INTO session_provenance(
			agent_type, session_id, state, reason_code, captured_at, source_updated_at,
			adapter_revision, sources_json, warnings_json, warning_summary_json,
			last_successful_at, missing_since, revision
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_type, session_id) DO UPDATE SET
			state = excluded.state,
			reason_code = excluded.reason_code,
			captured_at = excluded.captured_at,
			source_updated_at = excluded.source_updated_at,
			adapter_revision = excluded.adapter_revision,
			sources_json = excluded.sources_json,
			warnings_json = excluded.warnings_json,
			warning_summary_json = excluded.warning_summary_json,
			last_successful_at = excluded.last_successful_at,
			missing_since = excluded.missing_since,
			revision = excluded.revision`,
		agentType, sessionID,
		string(p.State), p.ReasonCode, model.FormatTime(p.CapturedAt), sourceUpdated,
		p.AdapterRevision, string(sourcesJSON), string(warningsJSON), string(summaryJSON),
		lastOK, missing, revision,
	)
	if err != nil {
		return fmt.Errorf("upsert provenance: %w", err)
	}
	return nil
}

// MarkSessionsSourceMissing sets source_missing tombstones for sessions that
// disappeared from a successful agent discovery pass. Keeps sessions, FTS, and
// watermarks. Only call when ListSessions for that agent succeeded.
// Returns the number of sessions newly or re-marked missing.
func (db *DB) MarkSessionsSourceMissing(agentType string, missingIDs []string, now time.Time) (int, error) {
	if len(missingIDs) == 0 {
		return 0, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := db.conn.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	changed := 0
	for _, id := range missingIDs {
		// Only tombstone sessions that exist in the sessions table.
		var exists int
		err := tx.QueryRow(
			`SELECT 1 FROM sessions WHERE agent_type = ? AND id = ?`,
			agentType, id,
		).Scan(&exists)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return 0, err
		}

		var prevState, prevMissing, prevLastOK, sourcesJSON, warningsJSON, summaryJSON sql.NullString
		var adapterRev sql.NullInt64
		err = tx.QueryRow(`
			SELECT state, missing_since, last_successful_at, adapter_revision,
			       sources_json, warnings_json, warning_summary_json
			FROM session_provenance WHERE agent_type = ? AND session_id = ?`,
			agentType, id,
		).Scan(&prevState, &prevMissing, &prevLastOK, &adapterRev, &sourcesJSON, &warningsJSON, &summaryJSON)

		missingSince := model.FormatTime(now)
		if err == nil && prevState.Valid && prevState.String == string(model.RecordSourceMissing) && prevMissing.Valid && prevMissing.String != "" {
			// Already tombstoned — keep original missing_since.
			missingSince = prevMissing.String
		}

		rev := 1
		if adapterRev.Valid && adapterRev.Int64 > 0 {
			rev = int(adapterRev.Int64)
		}
		srcJSON := "[]"
		if sourcesJSON.Valid && sourcesJSON.String != "" {
			srcJSON = sourcesJSON.String
			// Mark all sources missing in inventory when possible.
			var sources []model.SessionSourceFile
			if json.Unmarshal([]byte(srcJSON), &sources) == nil {
				for i := range sources {
					sources[i].State = model.SourceMissing
				}
				if b, e := json.Marshal(sources); e == nil {
					srcJSON = string(b)
				}
			}
		}
		warnJSON := "[]"
		if warningsJSON.Valid && warningsJSON.String != "" {
			warnJSON = warningsJSON.String
		}
		sumJSON := "{}"
		if summaryJSON.Valid && summaryJSON.String != "" {
			sumJSON = summaryJSON.String
		}
		lastOK := prevLastOK
		// If we never stored last_successful_at but had a non-missing state, use now-ish leave null.

		_, err = tx.Exec(`
			INSERT INTO session_provenance(
				agent_type, session_id, state, reason_code, captured_at, source_updated_at,
				adapter_revision, sources_json, warnings_json, warning_summary_json,
				last_successful_at, missing_since, revision
			) VALUES (?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, 0)
			ON CONFLICT(agent_type, session_id) DO UPDATE SET
				state = excluded.state,
				reason_code = excluded.reason_code,
				captured_at = excluded.captured_at,
				sources_json = excluded.sources_json,
				missing_since = excluded.missing_since,
				last_successful_at = COALESCE(session_provenance.last_successful_at, excluded.last_successful_at)`,
			agentType, id,
			string(model.RecordSourceMissing), "source_missing", model.FormatTime(now),
			rev, srcJSON, warnJSON, sumJSON,
			nullStringValue(lastOK), missingSince,
		)
		if err != nil {
			return 0, fmt.Errorf("mark source missing %s: %w", id, err)
		}
		changed++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return changed, nil
}

func nullStringValue(ns sql.NullString) any {
	if ns.Valid {
		return ns.String
	}
	return nil
}

// SessionIDsByAgent returns all indexed session IDs for an agent.
func (db *DB) SessionIDsByAgent(agentType string) ([]string, error) {
	rows, err := db.conn.Query(
		`SELECT id FROM sessions WHERE agent_type = ?`,
		agentType,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// IsSourceMissing reports whether the stored provenance is source_missing.
func (db *DB) IsSourceMissing(agentType, sessionID string) (bool, error) {
	var state string
	err := db.conn.QueryRow(
		`SELECT state FROM session_provenance WHERE agent_type = ? AND session_id = ?`,
		agentType, sessionID,
	).Scan(&state)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return state == string(model.RecordSourceMissing), nil
}

// GetSessionRow returns stored session metadata for fallback envelopes.
func (db *DB) GetSessionRow(agentType, sessionID string) (model.Session, bool, error) {
	row := db.conn.QueryRow(`
		SELECT agent_type, id, cwd, repository, branch, project, name, model_name, model_provider,
		       resume_id, parent_session_id, agent_path, is_subagent,
		       turn_count, historical_turn_count, rolled_back_turn_count, message_count,
		       created_at, updated_at
		FROM sessions WHERE agent_type = ? AND id = ?`,
		agentType, sessionID,
	)
	var s model.Session
	var createdStr, updatedStr string
	var isSub int
	err := row.Scan(
		&s.AgentType, &s.ID, &s.CWD, &s.Repository, &s.Branch, &s.Project, &s.Name,
		&s.ModelName, &s.ModelProvider, &s.ResumeID, &s.ParentSessionID, &s.AgentPath, &isSub,
		&s.TurnCount, &s.HistoricalTurnCount, &s.RolledBackTurnCount, &s.MessageCount,
		&createdStr, &updatedStr,
	)
	if err == sql.ErrNoRows {
		return model.Session{}, false, nil
	}
	if err != nil {
		return model.Session{}, false, err
	}
	s.IsSubagent = isSub != 0
	s.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	s.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
	return s, true, nil
}

// FindSessionRowByID looks up a session by id across agents (detail fallback).
func (db *DB) FindSessionRowByID(sessionID string) (model.Session, bool, error) {
	row := db.conn.QueryRow(`
		SELECT agent_type, id, cwd, repository, branch, project, name, model_name, model_provider,
		       resume_id, parent_session_id, agent_path, is_subagent,
		       turn_count, historical_turn_count, rolled_back_turn_count, message_count,
		       created_at, updated_at
		FROM sessions WHERE id = ?
		ORDER BY updated_at DESC LIMIT 1`,
		sessionID,
	)
	var s model.Session
	var createdStr, updatedStr string
	var isSub int
	err := row.Scan(
		&s.AgentType, &s.ID, &s.CWD, &s.Repository, &s.Branch, &s.Project, &s.Name,
		&s.ModelName, &s.ModelProvider, &s.ResumeID, &s.ParentSessionID, &s.AgentPath, &isSub,
		&s.TurnCount, &s.HistoricalTurnCount, &s.RolledBackTurnCount, &s.MessageCount,
		&createdStr, &updatedStr,
	)
	if err == sql.ErrNoRows {
		return model.Session{}, false, nil
	}
	if err != nil {
		return model.Session{}, false, err
	}
	s.IsSubagent = isSub != 0
	s.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	s.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
	return s, true, nil
}

func decodeProvenance(
	state, reason, capturedAt string,
	sourceUpdated sql.NullString,
	adapterRev int,
	sourcesJSON, warningsJSON, summaryJSON string,
	lastOK, missingSince sql.NullString,
) (*model.SessionProvenance, error) {
	p := &model.SessionProvenance{
		State:           model.RecordCompletenessState(state),
		ReasonCode:      reason,
		AdapterRevision: adapterRev,
		Sources:         []model.SessionSourceFile{},
		Warnings:        []model.ParseWarning{},
	}
	if t, err := time.Parse(time.RFC3339, capturedAt); err == nil {
		p.CapturedAt = t
	}
	if sourceUpdated.Valid && sourceUpdated.String != "" {
		if t, err := time.Parse(time.RFC3339, sourceUpdated.String); err == nil {
			p.SourceUpdatedAt = &t
		}
	}
	if lastOK.Valid && lastOK.String != "" {
		if t, err := time.Parse(time.RFC3339, lastOK.String); err == nil {
			p.LastSuccessfulAt = &t
		}
	}
	if missingSince.Valid && missingSince.String != "" {
		if t, err := time.Parse(time.RFC3339, missingSince.String); err == nil {
			p.MissingSince = &t
		}
	}
	// Corrupt JSON must degrade safely.
	if sourcesJSON != "" && sourcesJSON != "[]" {
		var sources []model.SessionSourceFile
		if err := json.Unmarshal([]byte(sourcesJSON), &sources); err == nil {
			p.Sources = sources
		}
	}
	if warningsJSON != "" && warningsJSON != "[]" {
		var warnings []model.ParseWarning
		if err := json.Unmarshal([]byte(warningsJSON), &warnings); err == nil {
			p.Warnings = warnings
		}
	}
	if summaryJSON != "" && summaryJSON != "{}" {
		var summary model.WarningSummary
		if err := json.Unmarshal([]byte(summaryJSON), &summary); err == nil {
			p.WarningSummary = summary
		}
	}
	return p, nil
}
