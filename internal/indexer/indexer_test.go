//go:build sqlite_fts5

package indexer

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"path/filepath"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/reader/codex"
)

type mockReader struct {
	agentType       string
	sessions        []model.Session
	details         map[string]*model.SessionDetail
	listErr         error
	getSessionErr   error
	getSessionCalls *int32
	listCalls       *int32
}

func (m *mockReader) AgentType() string { return m.agentType }

func (m *mockReader) DisplayName() string { return m.agentType }

func (m *mockReader) ListSessions() ([]model.Session, error) {
	sessions, _, err := m.ListSessionsDetailed()
	return sessions, err
}

func (m *mockReader) ListSessionsDetailed() ([]model.Session, bool, error) {
	if m.listCalls != nil {
		atomic.AddInt32(m.listCalls, 1)
	}
	if m.listErr != nil {
		return nil, false, m.listErr
	}
	result := make([]model.Session, len(m.sessions))
	copy(result, m.sessions)
	return result, true, nil
}

func TestIndexer_AgentFilteredCycleSkipsOtherReaders(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	var selectedCalls, skippedCalls int32
	selected := &mockReader{
		agentType: "selected",
		listCalls: &selectedCalls,
		details:   map[string]*model.SessionDetail{},
	}
	skipped := &mockReader{
		agentType: "skipped",
		listCalls: &skippedCalls,
		details:   map[string]*model.SessionDetail{},
	}
	ix := New(database, []reader.BaseSessionReader{selected, skipped})

	if err := ix.indexOnce(context.Background(), map[string]struct{}{"selected": {}}); err != nil {
		t.Fatalf("filtered indexOnce: %v", err)
	}
	if got := atomic.LoadInt32(&selectedCalls); got != 1 {
		t.Fatalf("selected ListSessions calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&skippedCalls); got != 0 {
		t.Fatalf("skipped ListSessions calls = %d, want 0", got)
	}
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

type authoritativeMockReader struct {
	*mockReader
	envelope           *model.IndexSnapshotEnvelope
	authoritativeCalls int32
	legacyCalls        int32
}

func (m *authoritativeMockReader) ReadIndexSnapshotEnvelope(context.Context, model.Session) (*model.IndexSnapshotEnvelope, error) {
	atomic.AddInt32(&m.authoritativeCalls, 1)
	return m.envelope, nil
}

func (m *authoritativeMockReader) ReadIndexSnapshot(context.Context, model.Session) (*model.SessionDetail, []model.RenderEvent, error) {
	atomic.AddInt32(&m.legacyCalls, 1)
	return m.details["s1"], nil, nil
}

func testAuthoritativeEnvelope(session model.Session, message string) *model.IndexSnapshotEnvelope {
	digest := strings.Repeat("a", 64)
	revision := "sha256:" + digest
	missing := model.NonExactGitEvidence(model.GitEvidenceMissing, model.ReasonAgentGitFactMissing)
	missingString := func() model.GitFact[string] {
		return model.GitFact[string]{
			Assessment: missing, Source: model.GitSourceAgentRecorded, SourceRevision: revision,
		}
	}
	return &model.IndexSnapshotEnvelope{
		Detail: &model.SessionDetail{
			Session: session,
			Turns:   []model.TurnVM{{TurnIndex: 0, UserMessage: message}},
		},
		RenderEvents:      []model.RenderEvent{},
		SourceRevision:    revision,
		SourceFingerprint: model.SourceFingerprint{Algorithm: model.SourceFingerprintSHA256, Digest: digest, SizeBytes: 1},
		OriginGit: &model.SessionGitOrigin{
			RepositoryURL: missingString(), WorktreePath: missingString(), Branch: missingString(), HeadSHA: missingString(),
			DirtyState: model.GitFact[model.GitDirtyState]{
				Value: model.GitDirtyUnknown, Assessment: missing,
				Source: model.GitSourceAgentRecorded, SourceRevision: revision,
			},
		},
		Finalization: model.SessionFinalizationEvidence{
			State:            model.SessionFinalizationUnknown,
			Assessment:       model.NonExactSessionEvidence(model.SessionEvidenceMissing, model.ReasonSessionStateNotRecorded),
			SignalKind:       model.SessionSignalNone,
			SignalAssessment: model.NonExactSessionEvidence(model.SessionEvidenceMissing, model.ReasonSessionStateNotRecorded),
		},
	}
}

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

func TestIndexerPrefersAuthoritativeSnapshotEnvelope(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	session := model.Session{ID: "s1", AgentType: "test", UpdatedAt: time.Unix(0, 100)}
	r := &authoritativeMockReader{
		mockReader: &mockReader{
			agentType: "test",
			sessions:  []model.Session{session},
			details: map[string]*model.SessionDetail{
				"s1": {Session: session, Turns: []model.TurnVM{{TurnIndex: 0, UserMessage: "legacy path"}}},
			},
		},
		envelope: testAuthoritativeEnvelope(session, "authoritative path"),
	}
	r.envelope.RenderEvents = []model.RenderEvent{
		{EventID: "create", Type: "ToolInvocation", ToolName: "exec", ToolCallID: "call-create",
			ToolInput: map[string]any{"command": "gh pr create --base main"}},
		{EventID: "created", ParentEventID: "create", Type: "ToolResult", ToolCallID: "call-create",
			Timestamp: time.Date(2026, 8, 11, 16, 17, 21, 0, time.UTC), Stdout: "https://github.com/acme/widgets/pull/42\n"},
	}

	ix := New(database, []reader.BaseSessionReader{r})
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&r.authoritativeCalls); got != 1 {
		t.Fatalf("authoritative calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&r.legacyCalls); got != 0 {
		t.Fatalf("legacy snapshot calls = %d, want 0", got)
	}
	results, err := database.SearchTurns("authoritative", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].SessionID != "s1" {
		t.Fatalf("authoritative envelope was not indexed: %+v", results)
	}
	created, err := database.ChangeRequestCreationSessions("https://github.com/acme/widgets/pull/42", 10)
	if err != nil || len(created) != 1 || created[0].RootSessionID != "s1" {
		t.Fatalf("creation evidence not indexed: matches=%+v err=%v", created, err)
	}

	// A source rewrite must invalidate exact local evidence even if a buggy or
	// coarse lister leaves UpdatedAt unchanged.
	r.envelope = testAuthoritativeEnvelope(session, "rewritten authoritative path")
	r.envelope.SourceFingerprint.Digest = strings.Repeat("b", 64)
	r.envelope.SourceRevision = "sha256:" + strings.Repeat("b", 64)
	for _, fact := range []*model.GitFact[string]{
		&r.envelope.OriginGit.RepositoryURL, &r.envelope.OriginGit.WorktreePath,
		&r.envelope.OriginGit.Branch, &r.envelope.OriginGit.HeadSHA,
	} {
		fact.SourceRevision = r.envelope.SourceRevision
	}
	r.envelope.OriginGit.DirtyState.SourceRevision = r.envelope.SourceRevision
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	created, err = database.ChangeRequestCreationSessions("https://github.com/acme/widgets/pull/42", 10)
	if err != nil || len(created) != 0 {
		t.Fatalf("rewritten source retained creation evidence: matches=%+v err=%v", created, err)
	}
	if got := atomic.LoadInt32(&r.authoritativeCalls); got != 2 {
		t.Fatalf("authoritative calls after unchanged-timestamp rewrite = %d, want 2", got)
	}
}

func TestIndexerRejectsInvalidAuthoritativeSnapshotEnvelope(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	session := model.Session{ID: "s1", AgentType: "test", UpdatedAt: time.Unix(0, 100)}
	envelope := testAuthoritativeEnvelope(session, "invalid envelope")
	envelope.SourceRevision = "positions-layout-7"
	r := &authoritativeMockReader{
		mockReader: &mockReader{agentType: "test", sessions: []model.Session{session}, details: map[string]*model.SessionDetail{}},
		envelope:   envelope,
	}

	err = New(database, []reader.BaseSessionReader{r}).RunOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "validate authoritative index snapshot") {
		t.Fatalf("invalid authoritative envelope error = %v", err)
	}
	if _, exists, err := database.GetWatermark("test", "s1"); err != nil || exists {
		t.Fatalf("invalid envelope advanced watermark: exists=%v err=%v", exists, err)
	}
}

func TestIndexerClearsWatermarkWhenCreationEvidenceReplaceFails(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	session := model.Session{ID: "s1", AgentType: "test", UpdatedAt: time.Unix(0, 100)}
	r := &authoritativeMockReader{
		mockReader: &mockReader{
			agentType: "test",
			sessions:  []model.Session{session},
			details:   map[string]*model.SessionDetail{"s1": {Session: session}},
		},
		envelope: testAuthoritativeEnvelope(session, "zero-timestamp result"),
	}
	r.envelope.RenderEvents = []model.RenderEvent{
		{EventID: "create", Type: "ToolInvocation", ToolName: "exec",
			ToolInput: map[string]any{"command": "gh pr create --base main"}},
		{EventID: "created", ParentEventID: "create", Type: "ToolResult",
			Stdout: "https://github.com/acme/widgets/pull/42\n"},
	}
	err = New(database, []reader.BaseSessionReader{r}).RunOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "index Change Request creation evidence") {
		t.Fatalf("creation replace error = %v", err)
	}
	if _, exists, err := database.GetWatermark("test", "s1"); err != nil || exists {
		t.Fatalf("failed creation replace left watermark: exists=%v err=%v", exists, err)
	}
}

func TestIndexerBackfillsLegacySessionWithoutCreationIndex(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	session := model.Session{ID: "s1", AgentType: "test", UpdatedAt: time.Unix(0, 100)}
	legacy := &mockReader{
		agentType: "test",
		sessions:  []model.Session{session},
		details: map[string]*model.SessionDetail{
			"s1": {Session: session, Turns: []model.TurnVM{{TurnIndex: 0, UserMessage: "legacy"}}},
		},
	}
	if err := New(database, []reader.BaseSessionReader{legacy}).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err := database.HasSessionChangeRequestCreationIndex("test", "s1")
	if err != nil || current {
		t.Fatalf("legacy path should not write creation index: current=%v err=%v", current, err)
	}

	r := &authoritativeMockReader{
		mockReader: legacy,
		envelope:   testAuthoritativeEnvelope(session, "backfill"),
	}
	r.envelope.RenderEvents = []model.RenderEvent{
		{EventID: "create", Type: "ToolInvocation", ToolName: "exec",
			ToolInput: map[string]any{"command": "gh pr create --base main"}},
		{EventID: "created", ParentEventID: "create", Type: "ToolResult",
			Timestamp: time.Date(2026, 8, 11, 16, 17, 21, 0, time.UTC),
			Stdout:    "https://github.com/acme/widgets/pull/42\n"},
	}
	if err := New(database, []reader.BaseSessionReader{r}).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&r.authoritativeCalls); got != 1 {
		t.Fatalf("authoritative backfill calls = %d, want 1", got)
	}
	created, err := database.ChangeRequestCreationSessions("https://github.com/acme/widgets/pull/42", 10)
	if err != nil || len(created) != 1 || created[0].RootSessionID != "s1" {
		t.Fatalf("legacy session was not backfilled: matches=%+v err=%v", created, err)
	}
}

func TestIndexerIndexesSanitizedCodexCreationTranscript(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	r := codex.New(filepath.Join("..", "reader", "codex", "testdata", "pr-creation"))
	if err := New(database, []reader.BaseSessionReader{r}).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	created, err := database.ChangeRequestCreationSessions("https://github.com/acme/widgets/pull/42", 10)
	if err != nil || len(created) != 1 || created[0].RootAgentType != "codex" ||
		created[0].RootSessionID != "created" || created[0].Evidence.EventID == "" ||
		created[0].Evidence.SourceRevision == "" || created[0].Evidence.Assessment.State != model.GitEvidenceExact {
		t.Fatalf("sanitized transcript was not indexed: matches=%+v err=%v", created, err)
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
			{ID: "s1", UpdatedAt: time.Unix(0, 100), Project: "session-insight"},
		},
		details: map[string]*model.SessionDetail{
			"s1": {
				Session: model.Session{ID: "s1", UpdatedAt: time.Unix(0, 100), Project: "session-insight"},
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
	if _, err := database.Conn().Exec(`UPDATE sessions SET resume_id='parent-id', project='/home/deck/projects/session-insight/' WHERE agent_type='test' AND id='s1'`); err != nil {
		t.Fatal(err)
	}
	mr.sessions[0].ResumeID = "child-id"
	mr.sessions[0].Project = "session-insight"

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
	if summaries[0].Project != "session-insight" {
		t.Fatalf("project metadata sync failed: got %q, want session-insight", summaries[0].Project)
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

	// v0.5.1: successful discovery with a missing session becomes a recoverable
	// source_missing tombstone — FTS/metadata retained, not hard-deleted.
	results, err := database.SearchTurns("bravo", 30)
	if err != nil {
		t.Fatalf("SearchTurns: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected retained FTS hit for tombstoned B, got %d", len(results))
	}
	prov, ok, err := database.GetProvenance("test", "b")
	if err != nil || !ok {
		t.Fatalf("provenance for B: ok=%v err=%v", ok, err)
	}
	if prov.State != model.RecordSourceMissing {
		t.Fatalf("expected source_missing for B, got %s", prov.State)
	}
	// Session A still complete/searchable and not tombstoned.
	resultsA, err := database.SearchTurns("alpha", 30)
	if err != nil {
		t.Fatalf("SearchTurns alpha: %v", err)
	}
	if len(resultsA) != 1 {
		t.Fatalf("expected A retained, got %d", len(resultsA))
	}
	provA, okA, _ := database.GetProvenance("test", "a")
	if okA && provA.State == model.RecordSourceMissing {
		t.Fatal("session A must not be marked source_missing")
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
