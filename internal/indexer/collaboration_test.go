//go:build sqlite_fts5

package indexer

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
)

// collabMockReader extends mockReader with the optional
// reader.CollaborationReader interface so backend tests never depend on the
// parallel concrete-adapter branch.
type collabMockReader struct {
	mockReader
	graphs      map[string]collaboration.CollaborationGraph
	collabErr   error
	blockOnCtx  bool
	onRead      func()
	collabCalls *int32
}

func (m *collabMockReader) ReadCollaboration(ctx context.Context, root model.Session) (collaboration.CollaborationGraph, error) {
	if m.collabCalls != nil {
		atomic.AddInt32(m.collabCalls, 1)
	}
	if m.onRead != nil {
		m.onRead()
	}
	if m.blockOnCtx {
		<-ctx.Done()
		return collaboration.CollaborationGraph{}, ctx.Err()
	}
	if m.collabErr != nil {
		return collaboration.CollaborationGraph{}, m.collabErr
	}
	graph, ok := m.graphs[root.ID]
	if !ok {
		// A real adapter always returns a graph for its roots (possibly
		// zero-child); mirror that instead of fabricating one in shared code.
		graph = collaboration.CollaborationGraph{
			RootAgentType: m.agentType,
			RootSessionID: root.ID,
			Completeness:  collaboration.ExactFact(),
			Invocations: []collaboration.AgentInvocation{{
				ID:               collaboration.RootInvocationID(m.agentType, root.ID),
				DisplayName:      m.agentType + " main agent",
				AgentType:        m.agentType,
				Status:           collaboration.StatusCompleted,
				TimePrecision:    collaboration.ExactFact(),
				ContentPrecision: collaboration.ExactFact(),
				SourceIdentity:   collaboration.SourceIdentity{Kind: collaboration.IdentityRootSession, NativeID: root.ID},
			}},
		}
	}
	return graph, nil
}

// collabGraph builds a valid root+children graph for indexer tests.
func collabGraph(agentType, sessionID string, children ...string) collaboration.CollaborationGraph {
	rootID := collaboration.RootInvocationID(agentType, sessionID)
	g := collaboration.CollaborationGraph{
		RootAgentType: agentType,
		RootSessionID: sessionID,
		Completeness:  collaboration.ExactFact(),
		Invocations: []collaboration.AgentInvocation{{
			ID:               rootID,
			DisplayName:      agentType + " main agent",
			AgentType:        agentType,
			Status:           collaboration.StatusCompleted,
			TimePrecision:    collaboration.ExactFact(),
			ContentPrecision: collaboration.ExactFact(),
			SourceIdentity:   collaboration.SourceIdentity{Kind: collaboration.IdentityRootSession, NativeID: sessionID},
		}},
	}
	for _, nativeID := range children {
		childID := collaboration.ChildInvocationID(agentType, sessionID, nativeID)
		g.Invocations = append(g.Invocations, collaboration.AgentInvocation{
			ID:               childID,
			DisplayName:      nativeID,
			AgentType:        agentType,
			Status:           collaboration.StatusRunning,
			TimePrecision:    collaboration.ExactFact(),
			ContentPrecision: collaboration.ExactFact(),
			SourceIdentity:   collaboration.SourceIdentity{Kind: collaboration.IdentityPayloadID, NativeID: nativeID},
		})
		g.Delegations = append(g.Delegations, collaboration.Delegation{
			ID:                 collaboration.DelegationIDFor(rootID, childID),
			ParentInvocationID: rootID,
			ChildInvocationID:  childID,
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

func openCollabIndexerDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func collabSessionDetail(id string, rev int64) *model.SessionDetail {
	return &model.SessionDetail{
		Session: model.Session{ID: id, UpdatedAt: time.Unix(0, rev)},
		Turns:   []model.TurnVM{{TurnIndex: 0, UserMessage: "work for " + id}},
	}
}

func TestIndexerCollaborationIndexedForRoot(t *testing.T) {
	database := openCollabIndexerDB(t)
	mr := &collabMockReader{
		mockReader: mockReader{
			agentType: "codex",
			sessions:  []model.Session{{ID: "root", UpdatedAt: time.Unix(0, 100)}},
			details:   map[string]*model.SessionDetail{"root": collabSessionDetail("root", 100)},
		},
		graphs: map[string]collaboration.CollaborationGraph{"root": collabGraph("codex", "root", "c1", "c2")},
	}

	ix := New(database, []reader.BaseSessionReader{mr})
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	stored, err := database.GetCollaboration("codex", "root")
	if err != nil || stored == nil {
		t.Fatalf("GetCollaboration: %v", err)
	}
	if stored.Graph.Revision != 100 {
		t.Fatalf("collaboration revision = %d, want the shared session revision 100", stored.Graph.Revision)
	}
	if len(stored.Graph.Invocations) != 3 || len(stored.Graph.Delegations) != 2 {
		t.Fatalf("stored graph = %d invocations / %d delegations", len(stored.Graph.Invocations), len(stored.Graph.Delegations))
	}
	summaries, err := database.CollaborationSummaries("codex")
	if err != nil {
		t.Fatal(err)
	}
	if s := summaries["codex\x00root"]; s.ChildCount != 2 || s.ActiveCount != 2 {
		t.Fatalf("summary = %+v, want 2 children / 2 active", s)
	}
}

func TestIndexerCollaborationSkipsBackingChildAndUnsupportedReader(t *testing.T) {
	database := openCollabIndexerDB(t)
	var calls int32
	mr := &collabMockReader{
		mockReader: mockReader{
			agentType: "codex",
			sessions: []model.Session{
				{ID: "root", UpdatedAt: time.Unix(0, 100)},
				{ID: "child", UpdatedAt: time.Unix(0, 100), IsSubagent: true, ParentSessionID: "root"},
			},
			details: map[string]*model.SessionDetail{
				"root":  collabSessionDetail("root", 100),
				"child": collabSessionDetail("child", 100),
			},
		},
		graphs:      map[string]collaboration.CollaborationGraph{"root": collabGraph("codex", "root", "c1")},
		collabCalls: &calls,
	}
	plain := &mockReader{
		agentType: "plain",
		sessions:  []model.Session{{ID: "p1", UpdatedAt: time.Unix(0, 100)}},
		details:   map[string]*model.SessionDetail{"p1": collabSessionDetail("p1", 100)},
	}

	ix := New(database, []reader.BaseSessionReader{mr, plain})
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("ReadCollaboration calls = %d, want exactly 1 (root only)", n)
	}
	stored, err := database.GetCollaboration("codex", "child")
	if err != nil || stored != nil {
		t.Fatalf("backing child must not be a second collaboration root: %+v err=%v", stored, err)
	}
	stored, err = database.GetCollaboration("plain", "p1")
	if err != nil || stored != nil {
		t.Fatalf("reader without the interface must not fabricate a graph: %+v err=%v", stored, err)
	}
}

func TestIndexerCollaborationUnchangedRevisionSkipsParse(t *testing.T) {
	database := openCollabIndexerDB(t)
	var calls int32
	mr := &collabMockReader{
		mockReader: mockReader{
			agentType: "codex",
			sessions:  []model.Session{{ID: "root", UpdatedAt: time.Unix(0, 100)}},
			details:   map[string]*model.SessionDetail{"root": collabSessionDetail("root", 100)},
		},
		graphs:      map[string]collaboration.CollaborationGraph{"root": collabGraph("codex", "root", "c1")},
		collabCalls: &calls,
	}

	ix := New(database, []reader.BaseSessionReader{mr})
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("calls after first run = %d, want 1", n)
	}
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("unchanged revision must skip the collaboration parse, calls = %d", n)
	}
}

// TestIndexerCollaborationBackfillAfterPartialWrite pins the failure-ordering
// guarantee: a session whose turn watermark is current but whose
// collaboration revision is missing/older (v28 migration, manual cleanup, or
// a crash between the two writes) is re-indexed instead of staying
// permanently "unchanged".
func TestIndexerCollaborationBackfillAfterPartialWrite(t *testing.T) {
	database := openCollabIndexerDB(t)
	var getSessionCalls int32
	mr := &collabMockReader{
		mockReader: mockReader{
			agentType:       "codex",
			sessions:        []model.Session{{ID: "root", UpdatedAt: time.Unix(0, 100)}},
			details:         map[string]*model.SessionDetail{"root": collabSessionDetail("root", 100)},
			getSessionCalls: &getSessionCalls,
		},
		graphs: map[string]collaboration.CollaborationGraph{"root": collabGraph("codex", "root", "c1")},
	}

	// Simulate the partial state directly: turn watermark current, no
	// collaboration rows at all.
	if err := database.UpsertTurns("codex", "root", []db.TurnText{{TurnIndex: 0, Role: "user", Content: "work"}}, 100); err != nil {
		t.Fatal(err)
	}

	ix := New(database, []reader.BaseSessionReader{mr})
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n := atomic.LoadInt32(&getSessionCalls); n != 1 {
		t.Fatalf("partial state must trigger a full pass, GetSession calls = %d", n)
	}
	stored, err := database.GetCollaboration("codex", "root")
	if err != nil || stored == nil || stored.Graph.Revision != 100 {
		t.Fatalf("collaboration not backfilled: %+v err=%v", stored, err)
	}
}

func TestIndexerCollaborationFailurePreservesGraphAndWatermark(t *testing.T) {
	database := openCollabIndexerDB(t)
	mr := &collabMockReader{
		mockReader: mockReader{
			agentType: "codex",
			sessions:  []model.Session{{ID: "root", UpdatedAt: time.Unix(0, 100)}},
			details:   map[string]*model.SessionDetail{"root": collabSessionDetail("root", 100)},
		},
		graphs: map[string]collaboration.CollaborationGraph{"root": collabGraph("codex", "root", "c1", "c2")},
	}

	ix := New(database, []reader.BaseSessionReader{mr})
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}

	// Revision advances but the parse now fails transiently.
	mr.sessions[0].UpdatedAt = time.Unix(0, 200)
	mr.details["root"] = collabSessionDetail("root", 200)
	mr.collabErr = errors.New("transient parse failure")

	if err := ix.RunOnce(context.Background()); err == nil {
		t.Fatal("expected the cycle to surface the collaboration failure")
	}

	stored, err := database.GetCollaboration("codex", "root")
	if err != nil || stored == nil {
		t.Fatalf("GetCollaboration: %v", err)
	}
	if stored.Graph.Revision != 100 || len(stored.Graph.Invocations) != 3 {
		t.Fatalf("previous complete graph not preserved: rev=%d invocations=%d",
			stored.Graph.Revision, len(stored.Graph.Invocations))
	}
	if stored.GraphStatus != db.CollaborationGraphStale {
		t.Fatalf("graph status = %q, want stale after an interrupted parse", stored.GraphStatus)
	}
	rev, _, err := database.GetWatermark("codex", "root")
	if err != nil || rev != 100 {
		t.Fatalf("turn watermark advanced past a failed collaboration write: rev=%d err=%v", rev, err)
	}

	// Recovery at the original session revision must still replace the stale
	// graph: a source can revert UpdatedAt after a failed newer revision.
	mr.collabErr = nil
	mr.graphs["root"] = collabGraph("codex", "root", "c3")
	mr.sessions[0].UpdatedAt = time.Unix(0, 100)
	mr.details["root"] = collabSessionDetail("root", 100)
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatalf("recovery RunOnce: %v", err)
	}
	stored, err = database.GetCollaboration("codex", "root")
	if err != nil || stored == nil {
		t.Fatalf("GetCollaboration after recovery: %v", err)
	}
	if stored.GraphStatus != db.CollaborationGraphOK || stored.Graph.Revision != 100 || len(stored.Graph.Invocations) != 2 {
		t.Fatalf("recovery state: status=%q rev=%d invocations=%d",
			stored.GraphStatus, stored.Graph.Revision, len(stored.Graph.Invocations))
	}
}

func TestIndexerCollaborationInvalidGraphRejected(t *testing.T) {
	database := openCollabIndexerDB(t)
	mr := &collabMockReader{
		mockReader: mockReader{
			agentType: "codex",
			sessions:  []model.Session{{ID: "root", UpdatedAt: time.Unix(0, 100)}},
			details:   map[string]*model.SessionDetail{"root": collabSessionDetail("root", 100)},
		},
		graphs: map[string]collaboration.CollaborationGraph{"root": collabGraph("codex", "root", "c1")},
	}

	ix := New(database, []reader.BaseSessionReader{mr})
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}

	// A graph whose root coordinates do not match the requested session is a
	// reader contract violation: reject, mark stale, keep the old graph.
	bad := collabGraph("codex", "root", "c9")
	bad.RootSessionID = "someone-else"
	mr.sessions[0].UpdatedAt = time.Unix(0, 200)
	mr.details["root"] = collabSessionDetail("root", 200)
	mr.graphs["root"] = bad

	if err := ix.RunOnce(context.Background()); err == nil {
		t.Fatal("expected the mismatched-root graph to be rejected")
	}
	stored, err := database.GetCollaboration("codex", "root")
	if err != nil || stored == nil || stored.Graph.Revision != 100 {
		t.Fatalf("invalid graph replaced the stored one: %+v err=%v", stored, err)
	}
	if stored.GraphStatus != db.CollaborationGraphStale {
		t.Fatalf("graph status = %q, want stale", stored.GraphStatus)
	}
}

func TestIndexerCollaborationContextCancel(t *testing.T) {
	database := openCollabIndexerDB(t)
	mr := &collabMockReader{
		mockReader: mockReader{
			agentType: "codex",
			sessions:  []model.Session{{ID: "root", UpdatedAt: time.Unix(0, 100)}},
			details:   map[string]*model.SessionDetail{"root": collabSessionDetail("root", 100)},
		},
		graphs: map[string]collaboration.CollaborationGraph{"root": collabGraph("codex", "root", "c1")},
	}

	ix := New(database, []reader.BaseSessionReader{mr})
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}

	mr.sessions[0].UpdatedAt = time.Unix(0, 200)
	mr.details["root"] = collabSessionDetail("root", 200)
	mr.blockOnCtx = true

	// Cancel from inside the reader so the cancellation propagates through
	// ReadCollaboration rather than short-circuiting the cycle up front.
	ctx, cancel := context.WithCancel(context.Background())
	mr.onRead = cancel
	err := ix.RunOnce(ctx)
	if err == nil {
		t.Fatal("expected context cancellation to surface")
	}
	stored, derr := database.GetCollaboration("codex", "root")
	if derr != nil || stored == nil || stored.Graph.Revision != 100 {
		t.Fatalf("cancellation must not replace the stored graph: %+v err=%v", stored, derr)
	}
	rev, _, _ := database.GetWatermark("codex", "root")
	if rev != 100 {
		t.Fatalf("cancellation advanced the turn watermark: rev=%d", rev)
	}
}
