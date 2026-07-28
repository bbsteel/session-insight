//go:build sqlite_fts5

package db

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/model"
)

// collabTestChild describes one child invocation for test graph builders.
type collabTestChild struct {
	nativeID string
	status   collaboration.InvocationStatus
	backing  bool
}

// collabTestGraph builds a contract-valid graph: one deterministic root
// invocation, one delegation per child, exact evidence everywhere.
func collabTestGraph(agentType, sessionID string, revision int64, children ...collabTestChild) collaboration.CollaborationGraph {
	g := collaboration.CollaborationGraph{
		RootAgentType: agentType,
		RootSessionID: sessionID,
		Revision:      revision,
		Completeness:  collaboration.ExactFact(),
	}
	rootID := collaboration.RootInvocationID(agentType, sessionID)
	g.Invocations = append(g.Invocations, collaboration.AgentInvocation{
		ID:               rootID,
		DisplayName:      agentType + " main agent",
		AgentType:        agentType,
		Status:           collaboration.StatusCompleted,
		TimePrecision:    collaboration.ExactFact(),
		ContentPrecision: collaboration.ExactFact(),
		SourceIdentity:   collaboration.SourceIdentity{Kind: collaboration.IdentityRootSession, NativeID: sessionID},
	})
	for _, c := range children {
		inv := collaboration.AgentInvocation{
			ID:               collaboration.ChildInvocationID(agentType, sessionID, c.nativeID),
			DisplayName:      c.nativeID,
			AgentType:        agentType,
			Status:           c.status,
			TimePrecision:    collaboration.ExactFact(),
			ContentPrecision: collaboration.ExactFact(),
			SourceIdentity:   collaboration.SourceIdentity{Kind: collaboration.IdentityPayloadID, NativeID: c.nativeID},
		}
		if c.backing {
			inv.BackingSession = &collaboration.BackingSessionRef{AgentType: agentType, SessionID: "backing-" + c.nativeID}
		}
		g.Invocations = append(g.Invocations, inv)
		g.Delegations = append(g.Delegations, collaboration.Delegation{
			ID:                 collaboration.DelegationIDFor(rootID, inv.ID),
			ParentInvocationID: rootID,
			ChildInvocationID:  inv.ID,
			ExecutionMode:      collaboration.ExecutionUnknown,
			Evidence: collaboration.DelegationEvidence{
				Trigger: collaboration.ExactFact(),
				Timing:  collaboration.ExactFact(),
				Task:    collaboration.ExactFact(),
				Result:  collaboration.ExactFact(),
			},
		})
	}
	return g
}

func openCollabTestDB(t *testing.T) *DB {
	t.Helper()
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// TestCollaborationMigrationFromPriorSchema simulates a v27 database (the
// collaboration tables dropped, the version row rewound) and verifies Open
// recreates the schema and clears watermarks for the one-time backfill.
func TestCollaborationMigrationFromPriorSchema(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := database.UpsertTurns("codex", "s1", []TurnText{{TurnIndex: 0, Role: "user", Content: "x"}}, 42); err != nil {
		t.Fatalf("seed watermark: %v", err)
	}
	for _, stmt := range []string{
		`DROP TABLE collaboration_delegations`,
		`DROP TABLE collaboration_invocations`,
		`DROP TABLE collaboration_roots`,
		`DELETE FROM schema_migrations WHERE version = 28`,
	} {
		if _, err := database.Conn().Exec(stmt); err != nil {
			t.Fatalf("rewind schema: %v", err)
		}
	}
	database.Close()

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen after migration: %v", err)
	}
	defer reopened.Close()

	graph := collabTestGraph("codex", "s1", 43, collabTestChild{nativeID: "c1", status: collaboration.StatusRunning})
	if err := reopened.ReplaceCollaborationGraph(graph); err != nil {
		t.Fatalf("replace after migration: %v", err)
	}
	if _, exists, err := reopened.GetWatermark("codex", "s1"); err != nil || exists {
		t.Fatalf("watermarks must be cleared by v28 backfill, exists=%v err=%v", exists, err)
	}
}

func TestCollaborationCleanDatabaseCreation(t *testing.T) {
	database := openCollabTestDB(t)
	for _, table := range []string{"collaboration_roots", "collaboration_invocations", "collaboration_delegations"} {
		var name string
		if err := database.Conn().QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&name); err != nil {
			t.Fatalf("table %s missing on clean database: %v", table, err)
		}
	}
}

// TestReplaceCollaborationGraphGoldenRoundTrip stores golden contract
// fixtures and verifies the reconstructed graph serializes identically.
func TestReplaceCollaborationGraphGoldenRoundTrip(t *testing.T) {
	database := openCollabTestDB(t)
	goldenDir := filepath.Join("..", "collaboration", "testdata", "golden")
	for _, name := range []string{"standalone-child.json", "embedded-child.json", "lifecycle-only.json", "malformed-cycle.json"} {
		raw, err := os.ReadFile(filepath.Join(goldenDir, name))
		if err != nil {
			t.Fatalf("read golden %s: %v", name, err)
		}
		var graph collaboration.CollaborationGraph
		if err := json.Unmarshal(raw, &graph); err != nil {
			t.Fatalf("parse golden %s: %v", name, err)
		}
		if err := database.ReplaceCollaborationGraph(graph); err != nil {
			t.Fatalf("replace %s: %v", name, err)
		}
		stored, err := database.GetCollaboration(graph.RootAgentType, graph.RootSessionID)
		if err != nil || stored == nil {
			t.Fatalf("get %s: stored=%v err=%v", name, stored != nil, err)
		}
		gotRaw, err := json.Marshal(stored.Graph)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		var wantNorm, gotNorm any
		if err := json.Unmarshal(raw, &wantNorm); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(gotRaw, &gotNorm); err != nil {
			t.Fatal(err)
		}
		wantCanon, _ := json.Marshal(wantNorm)
		gotCanon, _ := json.Marshal(gotNorm)
		if string(wantCanon) != string(gotCanon) {
			t.Fatalf("%s round trip drift:\nwant: %s\ngot:  %s", name, wantCanon, gotCanon)
		}
		if stored.GraphStatus != CollaborationGraphOK {
			t.Fatalf("%s graph status = %q, want ok", name, stored.GraphStatus)
		}
	}
}

func TestReplaceCollaborationGraphRemovesStaleRows(t *testing.T) {
	database := openCollabTestDB(t)
	graphV1 := collabTestGraph("codex", "s1", 100,
		collabTestChild{nativeID: "c1", status: collaboration.StatusRunning},
		collabTestChild{nativeID: "c2", status: collaboration.StatusCompleted})
	if err := database.ReplaceCollaborationGraph(graphV1); err != nil {
		t.Fatal(err)
	}

	graphV2 := collabTestGraph("codex", "s1", 200,
		collabTestChild{nativeID: "c3", status: collaboration.StatusFailed})
	if err := database.ReplaceCollaborationGraph(graphV2); err != nil {
		t.Fatal(err)
	}

	stored, err := database.GetCollaboration("codex", "s1")
	if err != nil || stored == nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Graph.Revision != 200 {
		t.Fatalf("revision = %d, want 200", stored.Graph.Revision)
	}
	if len(stored.Graph.Invocations) != 2 || len(stored.Graph.Delegations) != 1 {
		t.Fatalf("stale rows survived replace: %d invocations, %d delegations",
			len(stored.Graph.Invocations), len(stored.Graph.Delegations))
	}
	for _, inv := range stored.Graph.Invocations {
		if strings.HasSuffix(inv.ID, ":child:c1") || strings.HasSuffix(inv.ID, ":child:c2") {
			t.Fatalf("prior revision invocation %q not removed", inv.ID)
		}
	}
}

func TestReplaceCollaborationGraphValidationFailurePreserves(t *testing.T) {
	database := openCollabTestDB(t)
	valid := collabTestGraph("codex", "s1", 100, collabTestChild{nativeID: "c1", status: collaboration.StatusRunning})
	if err := database.ReplaceCollaborationGraph(valid); err != nil {
		t.Fatal(err)
	}

	// No root invocation → fatal validation finding.
	broken := collabTestGraph("codex", "s1", 200, collabTestChild{nativeID: "c2", status: collaboration.StatusRunning})
	broken.Invocations = broken.Invocations[1:]
	if err := database.ReplaceCollaborationGraph(broken); err == nil {
		t.Fatal("expected validation failure")
	}

	rev, exists, err := database.CollaborationRevision("codex", "s1")
	if err != nil || !exists || rev != 100 {
		t.Fatalf("revision after rejected write = %d/%v/%v, want 100/true/nil", rev, exists, err)
	}
	stored, err := database.GetCollaboration("codex", "s1")
	if err != nil || stored == nil || len(stored.Graph.Invocations) != 2 {
		t.Fatalf("previous graph not preserved: %+v err=%v", stored, err)
	}
	if stored.GraphStatus != CollaborationGraphOK {
		t.Fatalf("rejected write must not mark the stored graph stale, got %q", stored.GraphStatus)
	}
}

// TestReplaceCollaborationGraphRollback simulates a mid-transaction failure
// (the invocations table vanishes between two replaces) and verifies the
// prior committed graph survives untouched.
func TestReplaceCollaborationGraphRollback(t *testing.T) {
	database := openCollabTestDB(t)
	valid := collabTestGraph("codex", "s1", 100, collabTestChild{nativeID: "c1", status: collaboration.StatusRunning})
	if err := database.ReplaceCollaborationGraph(valid); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn().Exec(`ALTER TABLE collaboration_invocations RENAME TO collaboration_invocations_gone`); err != nil {
		t.Fatal(err)
	}

	next := collabTestGraph("codex", "s1", 200, collabTestChild{nativeID: "c2", status: collaboration.StatusFailed})
	if err := database.ReplaceCollaborationGraph(next); err == nil {
		t.Fatal("expected transaction failure")
	}

	rev, exists, err := database.CollaborationRevision("codex", "s1")
	if err != nil || !exists || rev != 100 {
		t.Fatalf("rolled-back replace advanced revision: %d/%v/%v", rev, exists, err)
	}
	// The old root row must still be there (delete rolled back too).
	var status string
	if err := database.Conn().QueryRow(
		`SELECT graph_status FROM collaboration_roots WHERE root_agent_type = 'codex' AND root_session_id = 's1'`,
	).Scan(&status); err != nil || status != CollaborationGraphOK {
		t.Fatalf("root row after rollback: status=%q err=%v", status, err)
	}
}

func TestMarkCollaborationStaleRetainsGraph(t *testing.T) {
	database := openCollabTestDB(t)
	graph := collabTestGraph("codex", "s1", 100, collabTestChild{nativeID: "c1", status: collaboration.StatusRunning})
	if err := database.ReplaceCollaborationGraph(graph); err != nil {
		t.Fatal(err)
	}
	if err := database.MarkCollaborationStale("codex", "s1", "read collaboration: disk timeout"); err != nil {
		t.Fatal(err)
	}

	stored, err := database.GetCollaboration("codex", "s1")
	if err != nil || stored == nil {
		t.Fatalf("get: %v", err)
	}
	if stored.GraphStatus != CollaborationGraphStale {
		t.Fatalf("status = %q, want stale", stored.GraphStatus)
	}
	if stored.Graph.Revision != 100 || len(stored.Graph.Invocations) != 2 {
		t.Fatalf("stale mark mutated the retained graph: rev=%d invocations=%d",
			stored.Graph.Revision, len(stored.Graph.Invocations))
	}
	// Marking a never-indexed root is a no-op (API reports not-indexed).
	if err := database.MarkCollaborationStale("codex", "never-indexed", "x"); err != nil {
		t.Fatal(err)
	}
	stored, err = database.GetCollaboration("codex", "never-indexed")
	if err != nil || stored != nil {
		t.Fatalf("never-indexed root must stay absent: %+v err=%v", stored, err)
	}
}

func TestCollaborationForeignKeyCascade(t *testing.T) {
	database := openCollabTestDB(t)
	graph := collabTestGraph("codex", "s1", 100, collabTestChild{nativeID: "c1", status: collaboration.StatusRunning})
	if err := database.ReplaceCollaborationGraph(graph); err != nil {
		t.Fatal(err)
	}

	// Orphan invocation without a root row must be rejected.
	if _, err := database.Conn().Exec(
		`INSERT INTO collaboration_invocations(root_agent_type, root_session_id, invocation_id) VALUES ('ghost', 'ghost', 'ghost:x:1')`,
	); err == nil {
		t.Fatal("foreign key must reject an invocation without a root row")
	}

	if _, err := database.Conn().Exec(
		`DELETE FROM collaboration_roots WHERE root_agent_type = 'codex' AND root_session_id = 's1'`,
	); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"collaboration_invocations", "collaboration_delegations"} {
		var n int
		if err := database.Conn().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("cascade left %d rows in %s (err=%v)", n, table, err)
		}
	}
}

func TestDeleteSessionDataCleansCollaboration(t *testing.T) {
	database := openCollabTestDB(t)
	now := time.Now()
	if err := database.UpsertSessionMeta("codex", "s1", "", "", "", "", "s1", "", "", 0, 0, now, now); err != nil {
		t.Fatal(err)
	}
	graph := collabTestGraph("codex", "s1", 100, collabTestChild{nativeID: "c1", status: collaboration.StatusRunning})
	if err := database.ReplaceCollaborationGraph(graph); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteSessionData("codex", "s1"); err != nil {
		t.Fatal(err)
	}
	stored, err := database.GetCollaboration("codex", "s1")
	if err != nil || stored != nil {
		t.Fatalf("collaboration rows survived session delete: %+v err=%v", stored, err)
	}
}

func TestCollaborationOrphanCleanup(t *testing.T) {
	database := openCollabTestDB(t)
	now := time.Now()
	for _, id := range []string{"keep", "drop"} {
		if err := database.UpsertSessionMeta("codex", id, "", "", "", "", id, "", "", 0, 0, now, now); err != nil {
			t.Fatal(err)
		}
		if err := database.ReplaceCollaborationGraph(collabTestGraph("codex", id, 100)); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := database.DeleteOrphansByAgent("codex", []string{"keep"})
	if err != nil || removed != 1 {
		t.Fatalf("orphan cleanup removed=%d err=%v", removed, err)
	}
	stored, err := database.GetCollaboration("codex", "drop")
	if err != nil || stored != nil {
		t.Fatalf("orphan collaboration graph survived: %+v err=%v", stored, err)
	}
	stored, err = database.GetCollaboration("codex", "keep")
	if err != nil || stored == nil {
		t.Fatalf("kept collaboration graph lost: %v", err)
	}
}

func TestCollaborationSummariesStatusGrouping(t *testing.T) {
	database := openCollabTestDB(t)
	graph := collabTestGraph("codex", "busy", 100,
		collabTestChild{nativeID: "p", status: collaboration.StatusPending},
		collabTestChild{nativeID: "r", status: collaboration.StatusRunning},
		collabTestChild{nativeID: "w", status: collaboration.StatusWaiting},
		collabTestChild{nativeID: "f", status: collaboration.StatusFailed},
		collabTestChild{nativeID: "o", status: collaboration.StatusOrphaned},
		collabTestChild{nativeID: "c", status: collaboration.StatusCompleted},
		collabTestChild{nativeID: "x", status: collaboration.StatusCancelled},
		collabTestChild{nativeID: "u", status: collaboration.StatusUnknown},
	)
	if err := database.ReplaceCollaborationGraph(graph); err != nil {
		t.Fatal(err)
	}
	// A root whose graph is exactly zero-child: the summary must exist with
	// exact zero counts (distinguishable from an absent summary).
	if err := database.ReplaceCollaborationGraph(collabTestGraph("codex", "solo", 100)); err != nil {
		t.Fatal(err)
	}
	// Composite identity: the same session ID under another agent type is an
	// independent root.
	if err := database.ReplaceCollaborationGraph(collabTestGraph("claude", "busy", 100,
		collabTestChild{nativeID: "p", status: collaboration.StatusRunning})); err != nil {
		t.Fatal(err)
	}

	all, err := database.CollaborationSummaries("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("summaries = %d, want 3: %v", len(all), all)
	}
	busy := all["codex\x00busy"]
	if busy.ChildCount != 8 || busy.ActiveCount != 3 || busy.ProblemCount != 2 {
		t.Fatalf("busy summary = %+v, want 8 children / 3 active / 2 problem", busy)
	}
	if busy.Precision != string(collaboration.EvidenceExact) {
		t.Fatalf("busy precision = %q, want exact", busy.Precision)
	}
	solo, ok := all["codex\x00solo"]
	if !ok {
		t.Fatal("zero-child root must still produce an (exact zero) summary")
	}
	if solo.ChildCount != 0 || solo.ActiveCount != 0 || solo.ProblemCount != 0 || solo.Precision != "exact" {
		t.Fatalf("solo summary = %+v, want exact zero", solo)
	}
	claudeBusy := all["claude\x00busy"]
	if claudeBusy.ChildCount != 1 {
		t.Fatalf("composite identity leak: claude/busy = %+v", claudeBusy)
	}

	filtered, err := database.CollaborationSummaries("codex")
	if err != nil || len(filtered) != 2 {
		t.Fatalf("agent filter: %d err=%v", len(filtered), err)
	}

	// A stale retained graph reports estimated precision with the contract
	// stale reason instead of claiming exact counts.
	if err := database.MarkCollaborationStale("codex", "busy", "parse failed"); err != nil {
		t.Fatal(err)
	}
	all, err = database.CollaborationSummaries("")
	if err != nil {
		t.Fatal(err)
	}
	busy = all["codex\x00busy"]
	if busy.Precision != string(collaboration.EvidenceEstimated) || busy.ReasonCode != string(collaboration.ReasonStaleGraphRetained) {
		t.Fatalf("stale summary = %+v, want estimated/stale_graph_retained", busy)
	}
}

// TestCollaborationSummariesMaintainedConsistency verifies the
// transactionally maintained counts on the root rows match an independent
// grouped recomputation over the invocation rows.
func TestCollaborationSummariesMaintainedConsistency(t *testing.T) {
	database := openCollabTestDB(t)
	if err := database.ReplaceCollaborationGraph(collabTestGraph("codex", "busy", 100,
		collabTestChild{nativeID: "p", status: collaboration.StatusPending},
		collabTestChild{nativeID: "r", status: collaboration.StatusRunning},
		collabTestChild{nativeID: "w", status: collaboration.StatusWaiting},
		collabTestChild{nativeID: "f", status: collaboration.StatusFailed},
		collabTestChild{nativeID: "o", status: collaboration.StatusOrphaned},
		collabTestChild{nativeID: "c", status: collaboration.StatusCompleted})); err != nil {
		t.Fatal(err)
	}

	var child, active, problem int
	if err := database.Conn().QueryRow(
		`SELECT SUM(CASE WHEN is_root = 0 THEN 1 ELSE 0 END),
		        SUM(CASE WHEN is_root = 0 AND status IN ('pending','running','waiting') THEN 1 ELSE 0 END),
		        SUM(CASE WHEN is_root = 0 AND status IN ('failed','orphaned') THEN 1 ELSE 0 END)
		 FROM collaboration_invocations
		 WHERE root_agent_type = 'codex' AND root_session_id = 'busy'`,
	).Scan(&child, &active, &problem); err != nil {
		t.Fatal(err)
	}
	summaries, err := database.CollaborationSummaries("codex")
	if err != nil {
		t.Fatal(err)
	}
	summary := summaries["codex\x00busy"]
	if summary.ChildCount != child || summary.ActiveCount != active || summary.ProblemCount != problem {
		t.Fatalf("maintained counts %+v diverge from grouped recomputation %d/%d/%d",
			summary, child, active, problem)
	}
}

func TestSessionIndexed(t *testing.T) {
	database := openCollabTestDB(t)
	now := time.Now()
	if err := database.UpsertSessionMeta("codex", "s1", "", "", "", "", "s1", "", "", 0, 0, now, now); err != nil {
		t.Fatal(err)
	}
	ok, err := database.SessionIndexed("codex", "s1")
	if err != nil || !ok {
		t.Fatalf("codex/s1 = %v, %v", ok, err)
	}
	ok, err = database.SessionIndexed("claude", "s1")
	if err != nil || ok {
		t.Fatalf("composite identity must scope to agent type: %v, %v", ok, err)
	}
	ok, err = database.SessionIndexed("codex", "missing")
	if err != nil || ok {
		t.Fatalf("missing session = %v, %v", ok, err)
	}
}

// TestListRootSessionSummariesPredicate pins the shared root predicate: the
// store-level list hides backing children, the unfiltered list keeps them.
func TestListRootSessionSummariesPredicate(t *testing.T) {
	database := openCollabTestDB(t)
	now := time.Now()
	root := model.Session{ID: "root", AgentType: "codex", CreatedAt: now, UpdatedAt: now}
	child := model.Session{ID: "child", AgentType: "codex", ParentSessionID: "root", IsSubagent: true, CreatedAt: now, UpdatedAt: now}
	for _, sess := range []model.Session{root, child} {
		if err := database.UpsertSessionMetaWithHistoryAndLineage(
			sess.AgentType, sess.ID, "", "", "", "", sess.ID, "", "",
			sess.ParentSessionID, "", sess.IsSubagent, 0, 0, 0, 0, sess.CreatedAt, sess.UpdatedAt,
		); err != nil {
			t.Fatal(err)
		}
	}

	roots, err := database.ListRootSessionSummaries("codex")
	if err != nil || len(roots) != 1 || roots[0].ID != "root" {
		t.Fatalf("roots = %+v err=%v", roots, err)
	}
	all, err := database.ListSessionSummaries("codex")
	if err != nil || len(all) != 2 {
		t.Fatalf("unfiltered list = %d err=%v", len(all), err)
	}
	counts, err := database.CountRootSessionsByAgent()
	if err != nil || counts["codex"] != 1 {
		t.Fatalf("root counts = %v err=%v", counts, err)
	}
}
