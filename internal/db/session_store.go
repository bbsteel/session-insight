package db

import (
	"fmt"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// SessionMeta is the minimal metadata needed for search result enrichment.
type SessionMeta struct {
	Project         string
	Name            string
	UpdatedAt       time.Time
	ResumeID        string
	ParentSessionID string
	IsSubagent      bool
}

// RootSessionRef identifies the root ancestor of a subagent session for
// search-result landing: the sidebar lists roots only, so a subagent hit
// redirects its focus target here.
type RootSessionRef struct {
	AgentType string
	SessionID string
	Name      string
}

// maxLineageHops bounds parent-chain walks so a malformed lineage cycle
// terminates instead of looping forever.
const maxLineageHops = 8

func (db *DB) UpsertSessionMeta(agentType, id, cwd, repository, branch, project, name, modelName, resumeID string, turnCount, messageCount int, createdAt, updatedAt time.Time) error {
	return db.UpsertSessionMetaWithHistoryAndProvider(agentType, id, cwd, repository, branch, project, name, modelName, "", resumeID,
		turnCount, turnCount, 0, messageCount, createdAt, updatedAt)
}

func (db *DB) UpsertSessionMetaWithHistory(agentType, id, cwd, repository, branch, project, name, modelName, resumeID string, turnCount, historicalTurnCount, rolledBackTurnCount, messageCount int, createdAt, updatedAt time.Time) error {
	return db.UpsertSessionMetaWithHistoryAndProvider(agentType, id, cwd, repository, branch, project, name, modelName, "", resumeID,
		turnCount, historicalTurnCount, rolledBackTurnCount, messageCount, createdAt, updatedAt)
}

func (db *DB) UpsertSessionMetaWithHistoryAndProvider(agentType, id, cwd, repository, branch, project, name, modelName, modelProvider, resumeID string, turnCount, historicalTurnCount, rolledBackTurnCount, messageCount int, createdAt, updatedAt time.Time) error {
	return db.UpsertSessionMetaWithHistoryLineageAndProvider(agentType, id, cwd, repository, branch, project, name, modelName, modelProvider, resumeID,
		"", "", false, turnCount, historicalTurnCount, rolledBackTurnCount, messageCount, createdAt, updatedAt)
}

func (db *DB) UpsertSessionMetaWithHistoryAndLineage(agentType, id, cwd, repository, branch, project, name, modelName, resumeID, parentSessionID, agentPath string, isSubagent bool, turnCount, historicalTurnCount, rolledBackTurnCount, messageCount int, createdAt, updatedAt time.Time) error {
	return db.UpsertSessionMetaWithHistoryLineageAndProvider(agentType, id, cwd, repository, branch, project, name, modelName, "", resumeID,
		parentSessionID, agentPath, isSubagent, turnCount, historicalTurnCount, rolledBackTurnCount, messageCount, createdAt, updatedAt)
}

func (db *DB) UpsertSessionMetaWithHistoryLineageAndProvider(agentType, id, cwd, repository, branch, project, name, modelName, modelProvider, resumeID, parentSessionID, agentPath string, isSubagent bool, turnCount, historicalTurnCount, rolledBackTurnCount, messageCount int, createdAt, updatedAt time.Time) error {
	_, err := db.conn.Exec(
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
		agentType, id, cwd, repository, branch, project, name, modelName, modelProvider, resumeID, parentSessionID, agentPath, isSubagent,
		turnCount, historicalTurnCount, rolledBackTurnCount, messageCount,
		model.FormatTime(createdAt),
		model.FormatTime(updatedAt),
	)
	return err
}

// UpdateSessionResumeID synchronizes a reader-provided native resume ID
// without rebuilding the session's turn index. It repairs historical empty
// IDs and Codex subagent rows that previously stored their parent thread ID.
func (db *DB) UpdateSessionResumeID(agentType, sessionID, resumeID string) (bool, error) {
	if resumeID == "" {
		return false, nil
	}
	result, err := db.conn.Exec(
		`UPDATE sessions SET resume_id = ?
		 WHERE agent_type = ? AND id = ? AND resume_id <> ?`,
		resumeID, agentType, sessionID, resumeID,
	)
	if err != nil {
		return false, fmt.Errorf("update session resume id: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("resume id rows affected: %w", err)
	}
	return rows > 0, nil
}

// RefreshSessionListMetadata synchronizes list-derived display fields without
// rebuilding the turn index. Call this when the session content revision is
// unchanged but adapter logic may have improved metadata — for example
// project-name normalization (Grok full paths → basenames) or Codex resume_id
// backfill.
//
// Empty project and empty resumeID leave the stored values alone so partial
// list projections (notably imported sessions, which omit Project on list)
// cannot wipe previously known metadata.
func (db *DB) RefreshSessionListMetadata(agentType string, sess model.Session) (bool, error) {
	project := sess.Project
	resumeID := sess.ResumeID
	result, err := db.conn.Exec(
		`UPDATE sessions SET
		     project = CASE WHEN ? != '' THEN ? ELSE project END,
		     resume_id = CASE WHEN ? != '' THEN ? ELSE resume_id END
		 WHERE agent_type = ? AND id = ?
		   AND (
		     (? != '' AND project <> ?)
		     OR (? != '' AND resume_id <> ?)
		   )`,
		project, project,
		resumeID, resumeID,
		agentType, sess.ID,
		project, project,
		resumeID, resumeID,
	)
	if err != nil {
		return false, fmt.Errorf("refresh session list metadata: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("list metadata rows affected: %w", err)
	}
	return rows > 0, nil
}

// rootSessionPredicate is the single canonical root-list filter (frozen
// collaboration contract decision): adapters report child lineage faithfully
// and never pre-filter children, and every root-only surface — list queries,
// count queries, collaboration summary joins — shares this one predicate.
// Backing child Sessions stay indexed and searchable; they are simply not
// roots.
const rootSessionPredicate = `is_subagent = 0`

// CountRootSessionsByAgent returns the number of non-subagent sessions per
// agent type from the index DB. Used by GET /api/agents so the catalog does
// not re-scan every Agent's session files on disk (ListSessions can take tens
// of seconds with large trees).
func (db *DB) CountRootSessionsByAgent() (map[string]int, error) {
	rows, err := db.conn.Query(`
		SELECT agent_type, COUNT(*)
		FROM sessions
		WHERE ` + rootSessionPredicate + `
		GROUP BY agent_type`)
	if err != nil {
		return nil, fmt.Errorf("count root sessions by agent: %w", err)
	}
	defer rows.Close()

	out := make(map[string]int)
	for rows.Next() {
		var agentType string
		var n int
		if err := rows.Scan(&agentType, &n); err != nil {
			return nil, fmt.Errorf("scan root session count: %w", err)
		}
		out[agentType] = n
	}
	return out, rows.Err()
}

// ListSessionSummaries returns every indexed session (optionally filtered by
// agent type) ordered by updated_at descending — the sidebar list is served
// straight from this query instead of re-scanning session files on disk.
func (db *DB) ListSessionSummaries(agentType string) ([]model.Session, error) {
	return db.listSessionSummaries(agentType, false)
}

// ListRootSessionSummaries is ListSessionSummaries restricted to root
// Sessions through the shared rootSessionPredicate. This is the query behind
// GET /api/sessions; the handler must not re-apply lineage filtering.
func (db *DB) ListRootSessionSummaries(agentType string) ([]model.Session, error) {
	return db.listSessionSummaries(agentType, true)
}

func (db *DB) listSessionSummaries(agentType string, rootsOnly bool) ([]model.Session, error) {
	query := `SELECT agent_type, id, cwd, repository, branch, project, name, model_name, model_provider, resume_id, parent_session_id, agent_path, is_subagent,
		                 turn_count, historical_turn_count, rolled_back_turn_count, message_count, created_at, updated_at
	          FROM sessions`
	var args []any
	switch {
	case rootsOnly && agentType != "":
		query += ` WHERE agent_type = ? AND ` + rootSessionPredicate
		args = append(args, agentType)
	case rootsOnly:
		query += ` WHERE ` + rootSessionPredicate
	case agentType != "":
		query += ` WHERE agent_type = ?`
		args = append(args, agentType)
	}
	query += ` ORDER BY updated_at DESC`

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list session summaries: %w", err)
	}
	defer rows.Close()

	var sessions []model.Session
	for rows.Next() {
		var s model.Session
		var isSubagent int
		var createdStr, updatedStr string
		if err := rows.Scan(&s.AgentType, &s.ID, &s.CWD, &s.Repository, &s.Branch, &s.Project,
			&s.Name, &s.ModelName, &s.ModelProvider, &s.ResumeID, &s.ParentSessionID, &s.AgentPath, &isSubagent,
			&s.TurnCount, &s.HistoricalTurnCount, &s.RolledBackTurnCount, &s.MessageCount,
			&createdStr, &updatedStr); err != nil {
			return nil, fmt.Errorf("scan session summary: %w", err)
		}
		s.IsSubagent = isSubagent != 0
		s.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		s.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// DeleteSessionData removes every trace of a session from the index DB in one
// transaction: search index, watermark, session row, position caches,
// bookmark, AI generations and title override. Called after the reader has
// deleted the session's source files, so a stale row can't resurrect it.
func (db *DB) DeleteSessionData(agentType, sessionID string) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("delete session data: %w", err)
	}
	defer tx.Rollback()

	// Resolve the bounded set of content hashes owned by this Session before
	// cascading its snapshot/evidence rows. Shared hosted or other-Session
	// references keep the corresponding blobs alive.
	rows, err := tx.Query(`
		SELECT DISTINCT refs.blob_sha
		FROM source_content_blob_refs refs
		LEFT JOIN session_git_snapshots snapshots
		  ON snapshots.snapshot_id = refs.local_snapshot_id
		LEFT JOIN session_git_bindings snapshot_bindings
		  ON snapshot_bindings.binding_id = snapshots.binding_id
		LEFT JOIN session_git_evidence evidence
		  ON evidence.evidence_id = refs.evidence_id
		LEFT JOIN session_git_bindings evidence_bindings
		  ON evidence_bindings.binding_id = evidence.binding_id
		WHERE (snapshot_bindings.agent_type = ? AND snapshot_bindings.session_id = ?)
		   OR (evidence_bindings.agent_type = ? AND evidence_bindings.session_id = ?)`,
		agentType, sessionID, agentType, sessionID,
	)
	if err != nil {
		return fmt.Errorf("list session source content: %w", err)
	}
	var ownedBlobSHAs []string
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			rows.Close()
			return fmt.Errorf("scan session source content: %w", err)
		}
		ownedBlobSHAs = append(ownedBlobSHAs, sha)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate session source content: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close session source content rows: %w", err)
	}

	stmts := []string{
		`DELETE FROM turn_texts WHERE agent_type = ? AND session_id = ?`,
		`DELETE FROM index_watermarks WHERE agent_type = ? AND session_id = ?`,
		`DELETE FROM session_positions WHERE agent_type = ? AND session_id = ?`,
		`DELETE FROM session_position_caches WHERE agent_type = ? AND session_id = ?`,
		`DELETE FROM bookmarked_sessions WHERE agent_type = ? AND session_id = ?`,
		`DELETE FROM ai_generations WHERE agent_type = ? AND session_id = ?`,
		`DELETE FROM session_title_overrides WHERE agent_type = ? AND session_id = ?`,
		`DELETE FROM terminal_bindings WHERE agent_type = ? AND session_id = ?`,
		// Explicit SI delete removes provenance; do not leave a tombstone.
		`DELETE FROM session_provenance WHERE agent_type = ? AND session_id = ?`,
		// Imported copies carry an import record tied to the session row.
		`DELETE FROM import_records WHERE agent_type = ? AND session_id = ?`,
		// Cascades to the root's collaboration invocations and delegations.
		`DELETE FROM collaboration_roots WHERE root_agent_type = ? AND root_session_id = ?`,
		// v34 Session Git rows and their content references cascade from the
		// session row. Shared hosted snapshots remain pinned independently.
		`DELETE FROM sessions WHERE agent_type = ? AND id = ?`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt, agentType, sessionID); err != nil {
			return fmt.Errorf("delete session data: %w", err)
		}
	}
	for _, sha := range ownedBlobSHAs {
		if _, err := tx.Exec(`
			DELETE FROM source_content_blobs
			WHERE sha256 = ? AND NOT EXISTS (
				SELECT 1 FROM source_content_blob_refs
				WHERE source_content_blob_refs.blob_sha = source_content_blobs.sha256
			)`, sha); err != nil {
			return fmt.Errorf("delete session source content: %w", err)
		}
	}
	return tx.Commit()
}

func (db *DB) GetSessionMetas(keys []struct{ AgentType, SessionID string }) (map[string]SessionMeta, error) {
	if len(keys) == 0 {
		return map[string]SessionMeta{}, nil
	}

	placeholders := make([]string, len(keys))
	args := make([]any, 0, len(keys)*2)
	for i, k := range keys {
		placeholders[i] = "(?, ?)"
		args = append(args, k.AgentType, k.SessionID)
	}

	query := fmt.Sprintf(
		`SELECT agent_type, id, project, name, updated_at, resume_id, parent_session_id, is_subagent FROM sessions WHERE (agent_type, id) IN (%s)`,
		strings.Join(placeholders, ", "),
	)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("get session metas: %w", err)
	}
	defer rows.Close()

	result := make(map[string]SessionMeta, len(keys))
	for rows.Next() {
		var agentType, sessionID, project, name, updatedStr, resumeID, parentSessionID string
		var isSubagent int
		if err := rows.Scan(&agentType, &sessionID, &project, &name, &updatedStr, &resumeID, &parentSessionID, &isSubagent); err != nil {
			return nil, fmt.Errorf("scan session meta: %w", err)
		}
		updatedAt, _ := time.Parse(time.RFC3339, updatedStr)
		result[agentType+"\x00"+sessionID] = SessionMeta{
			Project:         project,
			Name:            name,
			UpdatedAt:       updatedAt,
			ResumeID:        resumeID,
			ParentSessionID: parentSessionID,
			IsSubagent:      isSubagent != 0,
		}
	}
	return result, rows.Err()
}

// lineageRow is the minimal sessions-row projection needed to walk a
// parent chain.
type lineageRow struct {
	agentType       string
	id              string
	resumeID        string
	parentSessionID string
	isSubagent      bool
	name            string
}

// ResolveRootSessions maps subagent session keys to their root ancestor.
// The sidebar lists root sessions only, so a search hit on a subagent
// session redirects its landing target to the root returned here.
//
// Parent linkage is adapter-specific: Codex records the parent's native
// rollout UUID (the parent row's resume_id), while Grok records the parent
// row's id. Both shapes are matched. Roots, unknown sessions, and broken
// chains are absent from the result — callers keep their current behavior.
func (db *DB) ResolveRootSessions(keys []struct{ AgentType, SessionID string }) (map[string]RootSessionRef, error) {
	roots := make(map[string]RootSessionRef)
	if len(keys) == 0 {
		return roots, nil
	}

	// cache holds every fetched row, indexed by both id and resume_id so
	// either parent-linkage shape resolves in O(1).
	cache := make(map[string]lineageRow)
	cacheRow := func(row lineageRow) {
		cache[row.agentType+"\x00"+row.id] = row
		if row.resumeID != "" {
			cache[row.agentType+"\x00"+row.resumeID] = row
		}
	}

	initial := make([]struct{ AgentType, SessionID string }, 0, len(keys))
	initial = append(initial, keys...)
	if err := db.fetchLineageRows(initial, cacheRow); err != nil {
		return nil, err
	}

	for _, key := range keys {
		mapKey := key.AgentType + "\x00" + key.SessionID
		row, ok := cache[mapKey]
		if !ok || !row.isSubagent || row.parentSessionID == "" {
			continue
		}
		root, found := db.walkLineageToRoot(row, cache, cacheRow)
		if found {
			roots[mapKey] = root
		}
	}
	return roots, nil
}

// walkLineageToRoot follows parent_session_id hops until a root row. Each
// unresolved parent reference is batch-fetched on demand; cycles, chains
// longer than maxLineageHops, and partial lineage (a row still flagged
// is_subagent but with an empty parent_session_id) report not-found — the
// sidebar lists is_subagent = 0 rows only, so returning such a row as
// "root" would silently strand the landing target again.
func (db *DB) walkLineageToRoot(start lineageRow, cache map[string]lineageRow, cacheRow func(lineageRow)) (RootSessionRef, bool) {
	current := start
	for hop := 0; hop < maxLineageHops; hop++ {
		if !current.isSubagent {
			return RootSessionRef{AgentType: current.agentType, SessionID: current.id, Name: current.name}, true
		}
		if current.parentSessionID == "" {
			return RootSessionRef{}, false
		}
		parentKey := current.agentType + "\x00" + current.parentSessionID
		parent, ok := cache[parentKey]
		if !ok {
			if err := db.fetchLineageRows([]struct{ AgentType, SessionID string }{
				{AgentType: current.agentType, SessionID: current.parentSessionID},
			}, cacheRow); err != nil {
				return RootSessionRef{}, false
			}
			parent, ok = cache[parentKey]
			if !ok {
				return RootSessionRef{}, false
			}
		}
		if parent.id == current.id {
			return RootSessionRef{}, false // self-loop guard
		}
		current = parent
	}
	return RootSessionRef{}, false
}

// fetchLineageRows loads lineage projections for the given references,
// matching each reference against either the id or the resume_id column.
func (db *DB) fetchLineageRows(refs []struct{ AgentType, SessionID string }, visit func(lineageRow)) error {
	if len(refs) == 0 {
		return nil
	}
	conditions := make([]string, len(refs))
	args := make([]any, 0, len(refs)*3)
	for i, ref := range refs {
		conditions[i] = "(agent_type = ? AND (id = ? OR resume_id = ?))"
		args = append(args, ref.AgentType, ref.SessionID, ref.SessionID)
	}
	rows, err := db.conn.Query(
		`SELECT agent_type, id, resume_id, parent_session_id, is_subagent, name FROM sessions WHERE `+strings.Join(conditions, " OR "),
		args...,
	)
	if err != nil {
		return fmt.Errorf("fetch lineage rows: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row lineageRow
		var isSubagent int
		if err := rows.Scan(&row.agentType, &row.id, &row.resumeID, &row.parentSessionID, &isSubagent, &row.name); err != nil {
			return fmt.Errorf("scan lineage row: %w", err)
		}
		row.isSubagent = isSubagent != 0
		visit(row)
	}
	return rows.Err()
}
