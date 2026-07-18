//go:build sqlite_fts5

package indexer

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
)

type mockReader struct {
	agentType       string
	sessions        []model.Session
	details         map[string]*model.SessionDetail
	listErr         error
	getSessionErr   error
	getSessionCalls *int32
}

func (m *mockReader) AgentType() string { return m.agentType }

func (m *mockReader) DisplayName() string { return m.agentType }

func (m *mockReader) ListSessions() ([]model.Session, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	result := make([]model.Session, len(m.sessions))
	copy(result, m.sessions)
	return result, nil
}

func (m *mockReader) GetSession(id string) (*model.SessionDetail, error) {
	if m.getSessionCalls != nil {
		atomic.AddInt32(m.getSessionCalls, 1)
	}
	if m.getSessionErr != nil {
		return nil, m.getSessionErr
	}
	return m.details[id], nil
}

func (m *mockReader) RenderANSI(id string, cols int) (string, error) { return "", nil }

func (m *mockReader) GetRenderEvents(id string) ([]model.RenderEvent, error) { return nil, nil }

func TestIndexer_FirstRun(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	mr := &mockReader{
		agentType: "test",
		sessions: []model.Session{
			{ID: "s1", UpdatedAt: time.Unix(0, 100)},
		},
		details: map[string]*model.SessionDetail{
			"s1": {
				Session: model.Session{ID: "s1", UpdatedAt: time.Unix(0, 100)},
				Turns: []model.TurnVM{
					{TurnIndex: 0, UserMessage: "hello world"},
				},
			},
		},
	}

	ix := New(database, []reader.BaseSessionReader{mr})
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	prog := ix.SnapshotProgress()
	if prog.State != "idle" || prog.Percent != 100 || prog.Done != 1 || prog.Total != 1 {
		t.Fatalf("progress after run: %+v", prog)
	}

	results, err := database.SearchTurns("hello", 30)
	if err != nil {
		t.Fatalf("SearchTurns: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].SessionID != "s1" {
		t.Fatalf("expected s1, got %s", results[0].SessionID)
	}
}

func TestIndexer_UsesDetailMetadataWhenAvailable(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	mr := &mockReader{
		agentType: "codex",
		sessions: []model.Session{
			{
				ID:            "s1",
				ModelName:     "GPT-5",
				ModelProvider: "openai",
				UpdatedAt:     time.Unix(0, 100),
				CreatedAt:     time.Unix(0, 100),
			},
		},
		details: map[string]*model.SessionDetail{
			"s1": {
				Session: model.Session{
					ID:            "s1",
					ModelName:     "gpt-5.5",
					ModelProvider: "openai",
					UpdatedAt:     time.Unix(0, 100),
					CreatedAt:     time.Unix(0, 100),
				},
				Turns: []model.TurnVM{{TurnIndex: 0, UserMessage: "hello"}},
			},
		},
	}

	ix := New(database, []reader.BaseSessionReader{mr})
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	var modelName, modelProvider string
	if err := database.Conn().QueryRow(`SELECT model_name, model_provider FROM sessions WHERE agent_type = 'codex' AND id = 's1'`).Scan(&modelName, &modelProvider); err != nil {
		t.Fatalf("query session metadata: %v", err)
	}
	if modelName != "gpt-5.5" || modelProvider != "openai" {
		t.Fatalf("stored model/provider = %q/%q, want gpt-5.5/openai", modelName, modelProvider)
	}
}

func TestIndexer_UnchangedSkip(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	var getSessionCalls int32
	mr := &mockReader{
		agentType: "test",
		sessions: []model.Session{
			{ID: "s1", UpdatedAt: time.Unix(0, 100)},
		},
		details: map[string]*model.SessionDetail{
			"s1": {
				Session: model.Session{ID: "s1", UpdatedAt: time.Unix(0, 100)},
				Turns: []model.TurnVM{
					{TurnIndex: 0, UserMessage: "hello world"},
				},
			},
		},
		getSessionCalls: &getSessionCalls,
	}

	ix := New(database, []reader.BaseSessionReader{mr})
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}

	if n := atomic.LoadInt32(&getSessionCalls); n != 1 {
		t.Fatalf("expected 1 GetSession call after first run, got %d", n)
	}
	if _, err := database.Conn().Exec(`UPDATE sessions SET resume_id='parent-id' WHERE agent_type='test' AND id='s1'`); err != nil {
		t.Fatal(err)
	}
	mr.sessions[0].ResumeID = "child-id"

	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}

	if n := atomic.LoadInt32(&getSessionCalls); n != 1 {
		t.Fatalf("expected GetSession not called on second run (same revision), got %d calls", n)
	}
	summaries, err := database.ListSessionSummaries("test")
	if err != nil || len(summaries) != 1 || summaries[0].ResumeID != "child-id" {
		t.Fatalf("resume id metadata sync failed: summaries=%+v err=%v", summaries, err)
	}
}

func TestIndexer_RevisionChange(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	session := model.Session{ID: "s1", UpdatedAt: time.Unix(0, 100)}
	detail := &model.SessionDetail{
		Session: model.Session{ID: "s1", UpdatedAt: time.Unix(0, 100)},
		Turns: []model.TurnVM{
			{TurnIndex: 0, UserMessage: "old content"},
		},
	}

	mr := &mockReader{
		agentType: "test",
		sessions:  []model.Session{session},
		details:   map[string]*model.SessionDetail{"s1": detail},
	}

	ix := New(database, []reader.BaseSessionReader{mr})
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}

	results, err := database.SearchTurns("old", 30)
	if err != nil {
		t.Fatalf("SearchTurns old: %v", err)
	}
	if len(results) == 0 || results[0].SessionID != "s1" {
		t.Fatal("old content not found after first run")
	}

	// Change revision and content.
	mr.sessions[0] = model.Session{ID: "s1", UpdatedAt: time.Unix(0, 200)}
	mr.details["s1"] = &model.SessionDetail{
		Session: model.Session{ID: "s1", UpdatedAt: time.Unix(0, 200)},
		Turns: []model.TurnVM{
			{TurnIndex: 0, UserMessage: "new content here"},
		},
	}

	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}

	results, err = database.SearchTurns("new", 30)
	if err != nil {
		t.Fatalf("SearchTurns new: %v", err)
	}
	if len(results) == 0 || results[0].SessionID != "s1" {
		t.Fatal("new content not found after revision change")
	}
}

func TestIndexer_OrphanCleanup(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	mr := &mockReader{
		agentType: "test",
		sessions: []model.Session{
			{ID: "a", UpdatedAt: time.Unix(0, 100)},
			{ID: "b", UpdatedAt: time.Unix(0, 100)},
		},
		details: map[string]*model.SessionDetail{
			"a": {
				Session: model.Session{ID: "a", UpdatedAt: time.Unix(0, 100)},
				Turns:   []model.TurnVM{{TurnIndex: 0, UserMessage: "alpha content"}},
			},
			"b": {
				Session: model.Session{ID: "b", UpdatedAt: time.Unix(0, 100)},
				Turns:   []model.TurnVM{{TurnIndex: 0, UserMessage: "bravo content"}},
			},
		},
	}

	ix := New(database, []reader.BaseSessionReader{mr})
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}

	// Remove session B.
	mr.sessions = []model.Session{
		{ID: "a", UpdatedAt: time.Unix(0, 100)},
	}
	delete(mr.details, "b")

	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}

	results, err := database.SearchTurns("bravo", 30)
	if err != nil {
		t.Fatalf("SearchTurns: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for orphan B, got %d", len(results))
	}
}

func TestIndexer_ReaderFailurePreserve(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	mr := &mockReader{
		agentType: "test",
		sessions: []model.Session{
			{ID: "a", UpdatedAt: time.Unix(0, 100)},
		},
		details: map[string]*model.SessionDetail{
			"a": {
				Session: model.Session{ID: "a", UpdatedAt: time.Unix(0, 100)},
				Turns:   []model.TurnVM{{TurnIndex: 0, UserMessage: "preserved content"}},
			},
		},
	}

	ix := New(database, []reader.BaseSessionReader{mr})
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}

	mr.listErr = errors.New("fail")

	// Should log error but not panic.
	_ = ix.RunOnce(context.Background())

	results, err := database.SearchTurns("preserved", 30)
	if err != nil {
		t.Fatalf("SearchTurns: %v", err)
	}
	if len(results) == 0 || results[0].SessionID != "a" {
		t.Fatal("old index should be preserved after reader failure")
	}
}

func TestIndexer_GetSessionFailure(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	mr := &mockReader{
		agentType: "test",
		sessions: []model.Session{
			{ID: "s1", UpdatedAt: time.Unix(0, 100)},
		},
		details:       map[string]*model.SessionDetail{},
		getSessionErr: errors.New("get session failed"),
	}

	ix := New(database, []reader.BaseSessionReader{mr})
	err = ix.RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected RunOnce to surface session errors")
	}
	prog := ix.SnapshotProgress()
	if prog.Message != "completed_with_errors" {
		t.Fatalf("progress message = %q, want completed_with_errors", prog.Message)
	}

	_, exists, err := database.GetWatermark("test", "s1")
	if err != nil {
		t.Fatalf("GetWatermark: %v", err)
	}
	if exists {
		t.Fatal("watermark should not exist after GetSession failure")
	}
}

func TestIndexer_ContextCancel(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	sessions := make([]model.Session, 10)
	details := make(map[string]*model.SessionDetail)
	for i := range 10 {
		id := "s" + string(rune('0'+i))
		sessions[i] = model.Session{ID: id, UpdatedAt: time.Unix(0, 100)}
		details[id] = &model.SessionDetail{
			Session: model.Session{ID: id, UpdatedAt: time.Unix(0, 100)},
			Turns:   []model.TurnVM{{TurnIndex: 0, UserMessage: "data for " + id}},
		}
	}

	mr := &mockReader{
		agentType: "test",
		sessions:  sessions,
		details:   details,
	}

	ix := New(database, []reader.BaseSessionReader{mr})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = ix.RunOnce(ctx)
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
}

func TestIndexer_TransactionAtomicity(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	mr := &mockReader{
		agentType: "test",
		sessions: []model.Session{
			{ID: "s1", UpdatedAt: time.Unix(0, 100)},
		},
		details: map[string]*model.SessionDetail{
			"s1": {
				Session: model.Session{ID: "s1", UpdatedAt: time.Unix(0, 100)},
				Turns:   []model.TurnVM{{TurnIndex: 0, UserMessage: "atomic test content"}},
			},
		},
	}

	ix := New(database, []reader.BaseSessionReader{mr})
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	rev, exists, err := database.GetWatermark("test", "s1")
	if err != nil {
		t.Fatalf("GetWatermark: %v", err)
	}
	if !exists {
		t.Fatal("watermark should exist after successful RunOnce")
	}
	if rev != 100 {
		t.Fatalf("expected revision 100, got %d", rev)
	}
}

func TestIndexer_GetSessionFailurePreservesOrphan(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	// First run: both sessions indexed successfully
	mr := &mockReader{
		agentType: "test",
		sessions: []model.Session{
			{ID: "s1", UpdatedAt: time.Unix(0, 100)},
			{ID: "s2", UpdatedAt: time.Unix(0, 200)},
		},
		details: map[string]*model.SessionDetail{
			"s1": {Session: model.Session{ID: "s1"}, Turns: []model.TurnVM{{TurnIndex: 0, UserMessage: "data one"}}},
			"s2": {Session: model.Session{ID: "s2"}, Turns: []model.TurnVM{{TurnIndex: 0, UserMessage: "data two"}}},
		},
	}
	ix := New(database, []reader.BaseSessionReader{mr})
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Verify s2 was indexed
	results, err := database.SearchTurns("data two", 30)
	if err != nil {
		t.Fatalf("SearchTurns: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected s2 to be indexed after first run")
	}

	// Second run: ListSessions still returns both, but GetSession fails for s2
	// s1's revision changes so it gets re-indexed
	mr.sessions[0].UpdatedAt = time.Unix(0, 300)
	mr.details["s1"] = &model.SessionDetail{
		Session: model.Session{ID: "s1"},
		Turns:   []model.TurnVM{{TurnIndex: 0, UserMessage: "data one updated"}},
	}
	mr.getSessionErr = errors.New("temp failure")
	delete(mr.details, "s2") // s2's detail unreachable anyway due to getSessionErr

	var calls int32
	mr.getSessionCalls = &calls

	// s1 fails GetSession; cycle reports errors but must not orphan s2 (still listed).
	if err := ix.RunOnce(context.Background()); err == nil {
		t.Fatal("expected RunOnce error when s1 GetSession fails")
	}
	if msg := ix.SnapshotProgress().Message; msg != "completed_with_errors" {
		t.Fatalf("progress message = %q, want completed_with_errors", msg)
	}

	// s2 should STILL be searchable — GetSession failure must not trigger orphan deletion
	results, err = database.SearchTurns("data two", 30)
	if err != nil {
		t.Fatalf("SearchTurns: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("s2 data was deleted despite GetSession failure — orphan cleanup should not remove sessions that are still in ListSessions")
	}
}
