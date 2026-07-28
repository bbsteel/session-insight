//go:build sqlite_fts5

package db

import (
	"fmt"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/collaboration"
)

// Scale dataset for the collaboration list gates (design §12.2): ~10,000
// indexed Sessions with collaboration graphs on a meaningful subset
// (2,000 roots × 5 children = 12,000 invocations, 10,000 delegations).
// Seeding is direct SQL inside one transaction; the measured paths are the
// exact queries behind GET /api/sessions.
//
// Run with:
//
//	go test -tags sqlite_fts5 -bench Collaboration -benchtime 20x -run '^$' ./internal/db/
const (
	collabBenchSessions = 10000
	collabBenchRoots    = 2000
	collabBenchChildren = 5
)

func seedCollaborationBench(tb testing.TB) *DB {
	tb.Helper()
	database, err := Open(tb.TempDir())
	if err != nil {
		tb.Fatalf("Open: %v", err)
	}
	tb.Cleanup(func() { database.Close() })

	tx, err := database.Conn().Begin()
	if err != nil {
		tb.Fatal(err)
	}
	sessStmt, err := tx.Prepare(
		`INSERT INTO sessions(agent_type, id, cwd, repository, branch, project, name, model_name, model_provider,
		     resume_id, parent_session_id, agent_path, is_subagent, turn_count, historical_turn_count,
		     rolled_back_turn_count, message_count, created_at, updated_at)
		 VALUES (?, ?, '', '', '', '', ?, '', '', '', '', '', ?, 0, 0, 0, 0, ?, ?)`)
	if err != nil {
		tb.Fatal(err)
	}
	rootStmt, err := tx.Prepare(
		`INSERT INTO collaboration_roots(root_agent_type, root_session_id, revision,
		     completeness_state, completeness_reason, graph_status, status_detail, issues_json,
		     child_count, active_count, problem_count)
		 VALUES (?, ?, ?, 'exact', '', 'ok', '', '[]', ?, ?, ?)`)
	if err != nil {
		tb.Fatal(err)
	}
	invStmt, err := tx.Prepare(
		`INSERT INTO collaboration_invocations(root_agent_type, root_session_id, invocation_id,
		     ordinal, is_root, display_name, agent_type, role_label, status,
		     time_precision_state, content_precision_state, source_identity_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, '', ?, 'exact', 'exact', '{}')`)
	if err != nil {
		tb.Fatal(err)
	}
	delStmt, err := tx.Prepare(
		`INSERT INTO collaboration_delegations(root_agent_type, root_session_id, delegation_id,
		     ordinal, parent_invocation_id, child_invocation_id, execution_mode, evidence_json)
		 VALUES (?, ?, ?, ?, ?, ?, 'unknown', '{}')`)
	if err != nil {
		tb.Fatal(err)
	}

	statuses := []string{"pending", "running", "waiting", "failed", "orphaned", "completed", "unknown", "cancelled"}
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	for s := 0; s < collabBenchSessions; s++ {
		sid := fmt.Sprintf("sess-%05d", s)
		// 10% backing children: present in the sessions table, never roots.
		isSub := 0
		if s%10 == 9 {
			isSub = 1
		}
		ts := base.Add(time.Duration(s) * time.Minute).UTC().Format(time.RFC3339)
		if _, err := sessStmt.Exec("bench", sid, "Session "+sid, isSub, ts, ts); err != nil {
			tb.Fatal(err)
		}

		if s >= collabBenchRoots || isSub == 1 {
			continue
		}
		active, problem := 0, 0
		for c := 0; c < collabBenchChildren; c++ {
			switch statuses[(s+c)%len(statuses)] {
			case "pending", "running", "waiting":
				active++
			case "failed", "orphaned":
				problem++
			}
		}
		if _, err := rootStmt.Exec("bench", sid, int64(s+1), collabBenchChildren, active, problem); err != nil {
			tb.Fatal(err)
		}
		rootInvID := collaboration.RootInvocationID("bench", sid)
		if _, err := invStmt.Exec("bench", sid, rootInvID, 0, 1, "bench main agent", "bench", "completed"); err != nil {
			tb.Fatal(err)
		}
		for c := 0; c < collabBenchChildren; c++ {
			nativeID := fmt.Sprintf("child-%d", c)
			childInvID := collaboration.ChildInvocationID("bench", sid, nativeID)
			if _, err := invStmt.Exec("bench", sid, childInvID, c+1, 0, nativeID, "bench", statuses[(s+c)%len(statuses)]); err != nil {
				tb.Fatal(err)
			}
			if _, err := delStmt.Exec("bench", sid, collaboration.DelegationIDFor(rootInvID, childInvID), c, rootInvID, childInvID); err != nil {
				tb.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
	return database
}

// Baseline: the root-list query exactly as before the collaboration work.
func BenchmarkListRootSessions10k(b *testing.B) {
	database := seedCollaborationBench(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sessions, err := database.ListRootSessionSummaries("")
		if err != nil {
			b.Fatal(err)
		}
		if len(sessions) != collabBenchSessions-collabBenchSessions/10 {
			b.Fatalf("roots = %d", len(sessions))
		}
	}
}

// The grouped collaboration aggregate query (one query for the whole list).
func BenchmarkCollaborationSummaries10k(b *testing.B) {
	database := seedCollaborationBench(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		summaries, err := database.CollaborationSummaries("")
		if err != nil {
			b.Fatal(err)
		}
		if len(summaries) == 0 {
			b.Fatal("no summaries")
		}
	}
}

// Handler-shaped path: root list + one grouped aggregate query.
func BenchmarkCollaborationListPath10k(b *testing.B) {
	database := seedCollaborationBench(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sessions, err := database.ListRootSessionSummaries("")
		if err != nil {
			b.Fatal(err)
		}
		summaries, err := database.CollaborationSummaries("")
		if err != nil {
			b.Fatal(err)
		}
		attached := 0
		for _, sess := range sessions {
			if _, ok := summaries[sess.AgentType+"\x00"+sess.ID]; ok {
				attached++
			}
		}
		if attached == 0 {
			b.Fatal("no summaries attached")
		}
	}
}

// Detail path: reconstruct one stored graph (the collaboration detail
// endpoint serves metadata only, straight from the index).
func BenchmarkGetCollaboration(b *testing.B) {
	database := seedCollaborationBench(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stored, err := database.GetCollaboration("bench", "sess-00042")
		if err != nil || stored == nil {
			b.Fatalf("get: %v", err)
		}
	}
}
