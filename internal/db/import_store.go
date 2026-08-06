package db

import (
	"fmt"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// ImportRecord is one imported session's provenance row: which bundle
// brought it, from which host, and the original agent identity. The bundle
// directory under the imports root remains the source of truth; this table
// makes list/detail surfaces cheap (no per-session manifest reads).
type ImportRecord struct {
	AgentType         string
	SessionID         string
	BundleID          string
	OriginHost        string
	OriginalAgentType string
	OriginalSessionID string
	CaseLabel         string
	Redacted          bool
	ImportedAt        time.Time
}

// BundleSummary aggregates one imported bundle for the management API.
type BundleSummary struct {
	BundleID     string    `json:"bundle_id"`
	OriginHost   string    `json:"origin_host,omitempty"`
	CaseLabel    string    `json:"case_label,omitempty"`
	SessionCount int       `json:"session_count"`
	ImportedAt   time.Time `json:"imported_at"`
}

// UpsertImportRecord records (or refreshes on re-import) one bundle session.
func (db *DB) UpsertImportRecord(rec ImportRecord) error {
	importedAt := rec.ImportedAt
	if importedAt.IsZero() {
		importedAt = time.Now().UTC()
	}
	redacted := 0
	if rec.Redacted {
		redacted = 1
	}
	_, err := db.conn.Exec(
		`INSERT INTO import_records(agent_type, session_id, bundle_id, origin_host, original_agent_type, original_session_id, case_label, redacted, imported_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(agent_type, session_id) DO UPDATE SET
		     bundle_id           = excluded.bundle_id,
		     origin_host         = excluded.origin_host,
		     original_agent_type = excluded.original_agent_type,
		     original_session_id = excluded.original_session_id,
		     case_label          = excluded.case_label,
		     redacted            = excluded.redacted,
		     imported_at         = excluded.imported_at`,
		rec.AgentType, rec.SessionID, rec.BundleID, rec.OriginHost,
		rec.OriginalAgentType, rec.OriginalSessionID, rec.CaseLabel,
		redacted, model.FormatTime(importedAt),
	)
	if err != nil {
		return fmt.Errorf("upsert import record: %w", err)
	}
	return nil
}

// ImportSummaries returns every import record keyed the same way as
// recordStatuses in the session list handler: agent_type+"\x00"+session_id.
func (db *DB) ImportSummaries() (map[string]ImportRecord, error) {
	rows, err := db.conn.Query(
		`SELECT agent_type, session_id, bundle_id, origin_host, original_agent_type, original_session_id, case_label, redacted, imported_at
		 FROM import_records`)
	if err != nil {
		return nil, fmt.Errorf("list import records: %w", err)
	}
	defer rows.Close()

	out := make(map[string]ImportRecord)
	for rows.Next() {
		var rec ImportRecord
		var redacted int
		var importedStr string
		if err := rows.Scan(&rec.AgentType, &rec.SessionID, &rec.BundleID, &rec.OriginHost,
			&rec.OriginalAgentType, &rec.OriginalSessionID, &rec.CaseLabel, &redacted, &importedStr); err != nil {
			return nil, fmt.Errorf("scan import record: %w", err)
		}
		rec.Redacted = redacted != 0
		rec.ImportedAt, _ = time.Parse(time.RFC3339, importedStr)
		out[rec.AgentType+"\x00"+rec.SessionID] = rec
	}
	return out, rows.Err()
}

// DeleteImportRecordsByBundle removes every record of one bundle and returns
// the affected session IDs so the caller can clear their index data too.
func (db *DB) DeleteImportRecordsByBundle(bundleID string) ([]string, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return nil, fmt.Errorf("delete import records: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query(
		`SELECT session_id FROM import_records WHERE bundle_id = ?`, bundleID)
	if err != nil {
		return nil, fmt.Errorf("list bundle sessions: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan bundle session: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("list bundle sessions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(`DELETE FROM import_records WHERE bundle_id = ?`, bundleID); err != nil {
		return nil, fmt.Errorf("delete import records: %w", err)
	}
	return ids, tx.Commit()
}

// ListImportBundles aggregates import records per bundle for GET /api/imports.
func (db *DB) ListImportBundles() ([]BundleSummary, error) {
	rows, err := db.conn.Query(
		`SELECT bundle_id, MAX(origin_host), MAX(case_label), COUNT(*), MAX(imported_at)
		 FROM import_records
		 GROUP BY bundle_id
		 ORDER BY MAX(imported_at) DESC`)
	if err != nil {
		return nil, fmt.Errorf("list import bundles: %w", err)
	}
	defer rows.Close()

	out := []BundleSummary{}
	for rows.Next() {
		var s BundleSummary
		var importedStr string
		if err := rows.Scan(&s.BundleID, &s.OriginHost, &s.CaseLabel, &s.SessionCount, &importedStr); err != nil {
			return nil, fmt.Errorf("scan import bundle: %w", err)
		}
		s.ImportedAt, _ = time.Parse(time.RFC3339, importedStr)
		out = append(out, s)
	}
	return out, rows.Err()
}
