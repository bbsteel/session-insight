package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bbsteel/session-insight/internal/collaboration"
)

// Collaboration index lifecycle (frozen contract, internal/collaboration):
//
//   - One collaboration_roots row per indexed root Session carries the graph
//     revision, completeness evidence, and the ok/stale index state.
//   - Invocations and delegations are replaced transactionally per root
//     revision; the root row (and with it the revision) becomes visible only
//     when the transaction commits.
//   - A transient parse/validation/transaction failure never replaces the
//     stored graph with an empty one and never advances its revision: the
//     previous complete graph is preserved and the root row is marked stale
//     (contract evidence semantics: stale_graph_retained).
//   - Fields needed for filtering/aggregation stay relational (root identity,
//     status, timing, backing session, delegation endpoints); sparse source
//     identity, anchors, and per-fact evidence live in deterministic JSON.
//   - Full delegated prompts, returned results, and transcript bodies are
//     never copied into these tables.

// Collaboration graph index states stored on the root row.
const (
	CollaborationGraphOK    = "ok"
	CollaborationGraphStale = "stale"
)

// CollaborationSummary is the set-based aggregate attached to one root
// Session list row. Precision mirrors the stored graph's completeness
// evidence; ReasonCode explains any non-exact precision.
type CollaborationSummary struct {
	ChildCount   int
	ActiveCount  int
	ProblemCount int
	Precision    string
	ReasonCode   string
}

// StoredCollaboration is one root Session's persisted graph plus its index
// state, reconstructed from the collaboration tables.
type StoredCollaboration struct {
	Graph        collaboration.CollaborationGraph
	GraphStatus  string // CollaborationGraphOK | CollaborationGraphStale
	StatusDetail string
	IndexedAt    string
}

// fatalCollaborationIssue reports whether a validation finding is a reader
// contract violation that must reject the whole write (preserving any
// previously stored graph). Quarantine findings (self-link, cycle, duplicate
// relation, missing parent, ...) describe malformed source data and persist
// with the graph, exactly as the golden fixtures do.
func fatalCollaborationIssue(code collaboration.IssueCode) bool {
	switch code {
	case collaboration.IssueMissingField,
		collaboration.IssueInvalidStatus,
		collaboration.IssueInvalidExecutionMode,
		collaboration.IssueInvalidEvidence,
		collaboration.IssueNoRoot,
		collaboration.IssueDuplicateInvocation:
		return true
	default:
		return false
	}
}

// ReplaceCollaborationGraph validates g and atomically replaces the stored
// graph for its root: previous invocations and delegations are removed (via
// the root-row cascade) and the new revision becomes visible only when the
// transaction commits. A fatal validation finding or any write failure
// leaves the previously stored graph and revision untouched.
func (db *DB) ReplaceCollaborationGraph(g collaboration.CollaborationGraph) error {
	v := collaboration.Validate(&g)
	for _, issue := range v.Issues {
		if fatalCollaborationIssue(issue.Code) {
			return fmt.Errorf("collaboration graph invalid [%s]: %s", issue.Code, issue.Detail)
		}
	}

	issuesJSON := "[]"
	if len(v.Issues) > 0 {
		raw, err := json.Marshal(v.Issues)
		if err != nil {
			return fmt.Errorf("marshal collaboration issues: %w", err)
		}
		issuesJSON = string(raw)
	}

	// Transactionally maintained list aggregate: counts are derived from the
	// validated graph and committed in the same transaction as the rows they
	// describe, so the Session list never recomputes them.
	rootID := collaboration.RootInvocationID(g.RootAgentType, g.RootSessionID)
	var childCount, activeCount, problemCount int
	for _, inv := range g.Invocations {
		if inv.ID == rootID {
			continue
		}
		childCount++
		switch inv.Status {
		case collaboration.StatusPending, collaboration.StatusRunning, collaboration.StatusWaiting:
			activeCount++
		case collaboration.StatusFailed, collaboration.StatusOrphaned:
			problemCount++
		}
	}

	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin collaboration replace: %w", err)
	}
	defer tx.Rollback()

	// Deleting the root row cascades to the prior revision's invocations and
	// delegations; the fresh root row carries the new revision.
	if _, err := tx.Exec(
		`DELETE FROM collaboration_roots WHERE root_agent_type = ? AND root_session_id = ?`,
		g.RootAgentType, g.RootSessionID,
	); err != nil {
		return fmt.Errorf("clear prior collaboration graph: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO collaboration_roots(
		    root_agent_type, root_session_id, revision,
		    completeness_state, completeness_reason,
		    graph_status, status_detail, issues_json,
		    child_count, active_count, problem_count, indexed_at)
		 VALUES (?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?, datetime('now'))`,
		g.RootAgentType, g.RootSessionID, g.Revision,
		string(g.Completeness.State), string(g.Completeness.ReasonCode),
		CollaborationGraphOK, issuesJSON,
		childCount, activeCount, problemCount,
	); err != nil {
		return fmt.Errorf("insert collaboration root: %w", err)
	}

	for i, inv := range g.Invocations {
		identityJSON, err := json.Marshal(inv.SourceIdentity)
		if err != nil {
			return fmt.Errorf("marshal source identity for %q: %w", inv.ID, err)
		}
		var backingAgent, backingSession string
		if inv.BackingSession != nil {
			backingAgent = inv.BackingSession.AgentType
			backingSession = inv.BackingSession.SessionID
		}
		isRoot := 0
		if inv.ID == rootID {
			isRoot = 1
		}
		if _, err := tx.Exec(
			`INSERT INTO collaboration_invocations(
			    root_agent_type, root_session_id, invocation_id, ordinal, is_root,
			    display_name, agent_type, role_label, status,
			    started_at, ended_at,
			    time_precision_state, time_precision_reason,
			    content_precision_state, content_precision_reason,
			    backing_agent_type, backing_session_id, source_identity_json)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			g.RootAgentType, g.RootSessionID, inv.ID, i, isRoot,
			inv.DisplayName, inv.AgentType, inv.RoleLabel, string(inv.Status),
			collabTimeString(inv.StartedAt), collabTimeString(inv.EndedAt),
			string(inv.TimePrecision.State), string(inv.TimePrecision.ReasonCode),
			string(inv.ContentPrecision.State), string(inv.ContentPrecision.ReasonCode),
			backingAgent, backingSession, string(identityJSON),
		); err != nil {
			return fmt.Errorf("insert collaboration invocation %q: %w", inv.ID, err)
		}
	}

	seenDelegation := map[string]bool{}
	ordinal := 0
	for _, d := range g.Delegations {
		// Validation keeps the first occurrence of a duplicated delegation ID;
		// persistence follows the same rule so the primary key holds.
		if seenDelegation[d.ID] {
			continue
		}
		seenDelegation[d.ID] = true
		triggerJSON, err := collabAnchorJSON(d.Trigger)
		if err != nil {
			return fmt.Errorf("marshal trigger anchor for %q: %w", d.ID, err)
		}
		resultJSON, err := collabAnchorJSON(d.Result)
		if err != nil {
			return fmt.Errorf("marshal result anchor for %q: %w", d.ID, err)
		}
		evidenceJSON, err := json.Marshal(d.Evidence)
		if err != nil {
			return fmt.Errorf("marshal delegation evidence for %q: %w", d.ID, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO collaboration_delegations(
			    root_agent_type, root_session_id, delegation_id, ordinal,
			    parent_invocation_id, child_invocation_id,
			    task_summary, execution_mode,
			    trigger_json, result_json, evidence_json)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			g.RootAgentType, g.RootSessionID, d.ID, ordinal,
			d.ParentInvocationID, d.ChildInvocationID,
			d.TaskSummary, string(d.ExecutionMode),
			triggerJSON, resultJSON, string(evidenceJSON),
		); err != nil {
			return fmt.Errorf("insert collaboration delegation %q: %w", d.ID, err)
		}
		ordinal++
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit collaboration replace: %w", err)
	}
	return nil
}

// collabTimeString serializes an optional timestamp with the same
// RFC3339Nano trimming time.Time.MarshalJSON uses, so a stored graph
// round-trips byte-identically through the detail API.
func collabTimeString(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func collabAnchorJSON(a *collaboration.SourceAnchor) (any, error) {
	if a == nil {
		return nil, nil
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	return string(raw), nil
}

// CollaborationRevision returns the stored graph revision for one root.
// exists is false when the root was never collaboration-indexed (or its
// reader does not support collaboration).
func (db *DB) CollaborationRevision(agentType, sessionID string) (revision int64, exists bool, err error) {
	err = db.conn.QueryRow(
		`SELECT revision FROM collaboration_roots WHERE root_agent_type = ? AND root_session_id = ?`,
		agentType, sessionID,
	).Scan(&revision)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("collaboration revision: %w", err)
	}
	return revision, true, nil
}

// MarkCollaborationStale flags the stored graph as the last complete indexed
// revision after an interrupted or failed re-index. It never touches the
// stored invocations/delegations and never advances the revision, so the
// retained graph keeps serving until a valid replacement commits.
func (db *DB) MarkCollaborationStale(agentType, sessionID, detail string) error {
	if len([]rune(detail)) > 500 {
		detail = string([]rune(detail)[:500])
	}
	// No root row yet (never successfully indexed): nothing to mark; the API
	// reports not-indexed instead.
	if _, err := db.conn.Exec(
		`UPDATE collaboration_roots
		 SET graph_status = ?, status_detail = ?
		 WHERE root_agent_type = ? AND root_session_id = ?`,
		CollaborationGraphStale, detail, agentType, sessionID,
	); err != nil {
		return fmt.Errorf("mark collaboration stale: %w", err)
	}
	return nil
}

// GetCollaboration reconstructs the stored graph and index state for one
// root Session. Returns (nil, nil) when the root was never
// collaboration-indexed.
func (db *DB) GetCollaboration(agentType, sessionID string) (*StoredCollaboration, error) {
	var stored StoredCollaboration
	var completenessState, completenessReason string
	err := db.conn.QueryRow(
		`SELECT revision, completeness_state, completeness_reason,
		        graph_status, status_detail, indexed_at
		 FROM collaboration_roots
		 WHERE root_agent_type = ? AND root_session_id = ?`,
		agentType, sessionID,
	).Scan(&stored.Graph.Revision, &completenessState, &completenessReason,
		&stored.GraphStatus, &stored.StatusDetail, &stored.IndexedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get collaboration root: %w", err)
	}
	stored.Graph.RootAgentType = agentType
	stored.Graph.RootSessionID = sessionID
	stored.Graph.Completeness = collaboration.FactEvidence{
		State:      collaboration.EvidenceState(completenessState),
		ReasonCode: collaboration.ReasonCode(completenessReason),
	}

	invRows, err := db.conn.Query(
		`SELECT invocation_id, display_name, agent_type, role_label, status,
		        started_at, ended_at,
		        time_precision_state, time_precision_reason,
		        content_precision_state, content_precision_reason,
		        backing_agent_type, backing_session_id, source_identity_json
		 FROM collaboration_invocations
		 WHERE root_agent_type = ? AND root_session_id = ?
		 ORDER BY ordinal`,
		agentType, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list collaboration invocations: %w", err)
	}
	defer invRows.Close()

	stored.Graph.Invocations = []collaboration.AgentInvocation{}
	for invRows.Next() {
		var inv collaboration.AgentInvocation
		var startedAt, endedAt sql.NullString
		var timeState, timeReason, contentState, contentReason string
		var backingAgent, backingSession, identityJSON string
		var status string
		if err := invRows.Scan(&inv.ID, &inv.DisplayName, &inv.AgentType, &inv.RoleLabel, &status,
			&startedAt, &endedAt,
			&timeState, &timeReason, &contentState, &contentReason,
			&backingAgent, &backingSession, &identityJSON); err != nil {
			return nil, fmt.Errorf("scan collaboration invocation: %w", err)
		}
		inv.Status = collaboration.InvocationStatus(status)
		inv.TimePrecision = collaboration.FactEvidence{
			State:      collaboration.EvidenceState(timeState),
			ReasonCode: collaboration.ReasonCode(timeReason),
		}
		inv.ContentPrecision = collaboration.FactEvidence{
			State:      collaboration.EvidenceState(contentState),
			ReasonCode: collaboration.ReasonCode(contentReason),
		}
		if startedAt.Valid {
			t, perr := time.Parse(time.RFC3339Nano, startedAt.String)
			if perr != nil {
				return nil, fmt.Errorf("parse started_at for %q: %w", inv.ID, perr)
			}
			inv.StartedAt = &t
		}
		if endedAt.Valid {
			t, perr := time.Parse(time.RFC3339Nano, endedAt.String)
			if perr != nil {
				return nil, fmt.Errorf("parse ended_at for %q: %w", inv.ID, perr)
			}
			inv.EndedAt = &t
		}
		if backingAgent != "" || backingSession != "" {
			inv.BackingSession = &collaboration.BackingSessionRef{
				AgentType: backingAgent,
				SessionID: backingSession,
			}
		}
		if err := json.Unmarshal([]byte(identityJSON), &inv.SourceIdentity); err != nil {
			return nil, fmt.Errorf("unmarshal source identity for %q: %w", inv.ID, err)
		}
		stored.Graph.Invocations = append(stored.Graph.Invocations, inv)
	}
	if err := invRows.Err(); err != nil {
		return nil, err
	}

	delRows, err := db.conn.Query(
		`SELECT delegation_id, parent_invocation_id, child_invocation_id,
		        task_summary, execution_mode, trigger_json, result_json, evidence_json
		 FROM collaboration_delegations
		 WHERE root_agent_type = ? AND root_session_id = ?
		 ORDER BY ordinal`,
		agentType, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list collaboration delegations: %w", err)
	}
	defer delRows.Close()

	stored.Graph.Delegations = []collaboration.Delegation{}
	for delRows.Next() {
		var d collaboration.Delegation
		var execMode string
		var triggerJSON, resultJSON sql.NullString
		var evidenceJSON string
		if err := delRows.Scan(&d.ID, &d.ParentInvocationID, &d.ChildInvocationID,
			&d.TaskSummary, &execMode, &triggerJSON, &resultJSON, &evidenceJSON); err != nil {
			return nil, fmt.Errorf("scan collaboration delegation: %w", err)
		}
		d.ExecutionMode = collaboration.ExecutionMode(execMode)
		if triggerJSON.Valid {
			var anchor collaboration.SourceAnchor
			if err := json.Unmarshal([]byte(triggerJSON.String), &anchor); err != nil {
				return nil, fmt.Errorf("unmarshal trigger anchor for %q: %w", d.ID, err)
			}
			d.Trigger = &anchor
		}
		if resultJSON.Valid {
			var anchor collaboration.SourceAnchor
			if err := json.Unmarshal([]byte(resultJSON.String), &anchor); err != nil {
				return nil, fmt.Errorf("unmarshal result anchor for %q: %w", d.ID, err)
			}
			d.Result = &anchor
		}
		if err := json.Unmarshal([]byte(evidenceJSON), &d.Evidence); err != nil {
			return nil, fmt.Errorf("unmarshal delegation evidence for %q: %w", d.ID, err)
		}
		stored.Graph.Delegations = append(stored.Graph.Delegations, d)
	}
	if err := delRows.Err(); err != nil {
		return nil, err
	}

	return &stored, nil
}

// CollaborationSummaries returns the per-root collaboration aggregate for
// every collaboration-indexed root (optionally filtered by agent type) from
// the transactionally maintained counts on the root rows — one small
// set-based scan, never a per-row query or filesystem read. A root present
// here with ChildCount 0 is an exact, reader-confirmed zero-child graph;
// roots without a row are simply absent (unsupported or not yet indexed),
// which the API reports by omitting the summary object.
func (db *DB) CollaborationSummaries(agentType string) (map[string]CollaborationSummary, error) {
	query := `SELECT root_agent_type, root_session_id,
		         child_count, active_count, problem_count,
		         completeness_state, completeness_reason, graph_status
		  FROM collaboration_roots`
	var args []any
	if agentType != "" {
		query += ` WHERE root_agent_type = ?`
		args = append(args, agentType)
	}

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("collaboration summaries: %w", err)
	}
	defer rows.Close()

	out := make(map[string]CollaborationSummary)
	for rows.Next() {
		var agent, sessionID, state, reason, graphStatus string
		var summary CollaborationSummary
		if err := rows.Scan(&agent, &sessionID,
			&summary.ChildCount, &summary.ActiveCount, &summary.ProblemCount,
			&state, &reason, &graphStatus); err != nil {
			return nil, fmt.Errorf("scan collaboration summary: %w", err)
		}
		summary.Precision = state
		summary.ReasonCode = reason
		// A stale retained graph cannot claim exact counts for the current
		// revision: report the retention evidence instead.
		if graphStatus == CollaborationGraphStale {
			summary.Precision = string(collaboration.EvidenceEstimated)
			summary.ReasonCode = string(collaboration.ReasonStaleGraphRetained)
		}
		out[agent+"\x00"+sessionID] = summary
	}
	return out, rows.Err()
}

// SessionIndexed reports whether the composite (agent type, session ID)
// exists in the index-backed sessions table. Collaboration endpoints resolve
// session identity through this predicate instead of guessing from readers.
func (db *DB) SessionIndexed(agentType, sessionID string) (bool, error) {
	var n int
	if err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM sessions WHERE agent_type = ? AND id = ?`,
		agentType, sessionID,
	).Scan(&n); err != nil {
		return false, fmt.Errorf("session indexed check: %w", err)
	}
	return n > 0, nil
}
