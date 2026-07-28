package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
)

// collabStubReader is a fake reader with the optional
// reader.CollaborationReader interface and per-session render events, so API
// tests do not depend on the parallel concrete-adapter branch.
type collabStubReader struct {
	agentType       string
	eventsByID      map[string][]model.RenderEvent
	readCollabCalls int32
}

func (s *collabStubReader) AgentType() string   { return s.agentType }
func (s *collabStubReader) DisplayName() string { return s.agentType }
func (s *collabStubReader) ListSessions() ([]model.Session, error) {
	return nil, nil
}
func (s *collabStubReader) GetSession(id string) (*model.SessionDetail, error) {
	return &model.SessionDetail{Session: model.Session{ID: id, AgentType: s.agentType}}, nil
}
func (s *collabStubReader) RenderANSI(id string, cols int) (string, error) { return "", nil }
func (s *collabStubReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	if events, ok := s.eventsByID[id]; ok {
		return events, nil
	}
	return nil, nil
}
func (s *collabStubReader) ReadCollaboration(ctx context.Context, root model.Session) (collaboration.CollaborationGraph, error) {
	atomic.AddInt32(&s.readCollabCalls, 1)
	return collaboration.CollaborationGraph{}, nil
}

func seedCollabSession(t *testing.T, database *db.DB, agentType, id string, isSubagent bool) {
	t.Helper()
	now := time.Now()
	parent := ""
	if isSubagent {
		parent = "root"
	}
	if err := database.UpsertSessionMetaWithHistoryAndLineage(
		agentType, id, "", "", "", "", id, "", "",
		parent, "", isSubagent, 0, 0, 0, 0, now, now,
	); err != nil {
		t.Fatal(err)
	}
}

// apiCollabGraph builds a valid graph whose children have the given
// (nativeID, status) pairs.
func apiCollabGraph(agentType, sessionID string, revision int64, children ...collaboration.AgentInvocation) collaboration.CollaborationGraph {
	rootID := collaboration.RootInvocationID(agentType, sessionID)
	g := collaboration.CollaborationGraph{
		RootAgentType: agentType,
		RootSessionID: sessionID,
		Revision:      revision,
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
	for _, inv := range children {
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

func apiChild(agentType, sessionID, nativeID string, status collaboration.InvocationStatus) collaboration.AgentInvocation {
	return collaboration.AgentInvocation{
		ID:               collaboration.ChildInvocationID(agentType, sessionID, nativeID),
		DisplayName:      nativeID,
		AgentType:        agentType,
		Status:           status,
		TimePrecision:    collaboration.ExactFact(),
		ContentPrecision: collaboration.ExactFact(),
		SourceIdentity:   collaboration.SourceIdentity{Kind: collaboration.IdentityPayloadID, NativeID: nativeID},
	}
}

func openCollabAPIDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func getJSON(t *testing.T, srv *Server, url string, hdr map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	var body map[string]any
	if strings.Contains(w.Header().Get("Content-Type"), "application/json") && w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
	return w, body
}

func TestListSessionsCollaborationSummary(t *testing.T) {
	database := openCollabAPIDB(t)
	seedCollabSession(t, database, "codex", "root", false)
	seedCollabSession(t, database, "codex", "child", true) // backing child: never a root row
	seedCollabSession(t, database, "codex", "solo", false)
	seedCollabSession(t, database, "codex", "plain", false)

	graph := apiCollabGraph("codex", "root", 100,
		apiChild("codex", "root", "c-run", collaboration.StatusRunning),
		apiChild("codex", "root", "c-fail", collaboration.StatusFailed),
		apiChild("codex", "root", "c-done", collaboration.StatusCompleted))
	if err := database.ReplaceCollaborationGraph(graph); err != nil {
		t.Fatal(err)
	}
	if err := database.ReplaceCollaborationGraph(apiCollabGraph("codex", "solo", 100)); err != nil {
		t.Fatal(err)
	}

	srv := New(database, []reader.BaseSessionReader{&collabStubReader{agentType: "codex"}})
	req := httptest.NewRequest("GET", "/api/sessions?agent=codex", nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	var sessions []SessionSummary
	if err := json.Unmarshal(w.Body.Bytes(), &sessions); err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 3 {
		t.Fatalf("root list = %d rows, want 3 (backing child filtered): %+v", len(sessions), sessions)
	}
	if got := w.Header().Get("X-Total-Count"); got != "3" {
		t.Fatalf("X-Total-Count = %s, must match the returned root list (3)", got)
	}

	byID := map[string]SessionSummary{}
	for _, s := range sessions {
		byID[s.ID] = s
	}
	root := byID["root"].Collaboration
	if root == nil || root.ChildCount != 3 || root.ActiveCount != 1 || root.ProblemCount != 1 || root.Precision != "exact" {
		t.Fatalf("root summary = %+v, want 3/1/1/exact", root)
	}
	solo := byID["solo"].Collaboration
	if solo == nil {
		t.Fatal("exact zero-child root must still carry a summary (distinguishable from unindexed)")
	}
	if solo.ChildCount != 0 || solo.ActiveCount != 0 || solo.ProblemCount != 0 {
		t.Fatalf("solo summary = %+v, want exact zero", solo)
	}
	if byID["plain"].Collaboration != nil {
		t.Fatal("unindexed root must omit the summary object")
	}
	if _, isChild := byID["child"]; isChild {
		t.Fatal("backing child must not appear as a root row")
	}

	// The list payload must not grow child arrays or task text.
	if strings.Contains(w.Body.String(), "c-run") || strings.Contains(w.Body.String(), "task_summary") {
		t.Fatal("list payload leaks collaboration detail beyond the three-count aggregate")
	}
}

func TestListSessionsCollaborationStaleDowngrade(t *testing.T) {
	database := openCollabAPIDB(t)
	seedCollabSession(t, database, "codex", "root", false)
	if err := database.ReplaceCollaborationGraph(apiCollabGraph("codex", "root", 100,
		apiChild("codex", "root", "c1", collaboration.StatusRunning))); err != nil {
		t.Fatal(err)
	}
	if err := database.MarkCollaborationStale("codex", "root", "parse failed"); err != nil {
		t.Fatal(err)
	}

	srv := New(database, []reader.BaseSessionReader{&collabStubReader{agentType: "codex"}})
	req := httptest.NewRequest("GET", "/api/sessions?agent=codex", nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)

	var sessions []SessionSummary
	if err := json.Unmarshal(w.Body.Bytes(), &sessions); err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Collaboration == nil {
		t.Fatalf("sessions = %+v", sessions)
	}
	summary := sessions[0].Collaboration
	if summary.Precision != "estimated" || summary.ReasonCode != "stale_graph_retained" {
		t.Fatalf("stale summary = %+v, want estimated/stale_graph_retained", summary)
	}
}

func TestListSessionsCollaborationChangeInvalidatesETag(t *testing.T) {
	database := openCollabAPIDB(t)
	seedCollabSession(t, database, "codex", "root", false)
	srv := New(database, []reader.BaseSessionReader{&collabStubReader{agentType: "codex"}})

	get := func(etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/sessions", nil)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		w := httptest.NewRecorder()
		srv.Mux.ServeHTTP(w, req)
		return w
	}

	w := get("")
	etag := w.Header().Get("ETag")
	if w.Code != http.StatusOK || etag == "" {
		t.Fatalf("first GET: %d %q", w.Code, etag)
	}
	if w := get(etag); w.Code != http.StatusNotModified {
		t.Fatalf("unchanged list must 304, got %d", w.Code)
	}

	// The indexer bumps the list revision after a cycle that changed data;
	// a collaboration write is such a change.
	if err := database.ReplaceCollaborationGraph(apiCollabGraph("codex", "root", 100,
		apiChild("codex", "root", "c1", collaboration.StatusRunning))); err != nil {
		t.Fatal(err)
	}
	srv.NotifySessionsChanged()

	if w := get(etag); w.Code != http.StatusOK {
		t.Fatalf("collaboration change must invalidate the list ETag, got %d", w.Code)
	}
}

func TestGetCollaborationStates(t *testing.T) {
	database := openCollabAPIDB(t)
	seedCollabSession(t, database, "codex", "root", false)
	seedCollabSession(t, database, "codex", "pending", false)
	seedCollabSession(t, database, "plain", "p1", false)

	started := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	ended := time.Date(2026, 7, 1, 10, 5, 0, 0, time.UTC)
	child := apiChild("codex", "root", "c1", collaboration.StatusCompleted)
	child.StartedAt = &started
	child.EndedAt = &ended
	if err := database.ReplaceCollaborationGraph(apiCollabGraph("codex", "root", 123, child)); err != nil {
		t.Fatal(err)
	}

	collabReader := &collabStubReader{agentType: "codex"}
	srv := New(database, []reader.BaseSessionReader{collabReader, &stubReader{agentType: "plain"}})

	// Current graph: 200 ok with metadata only.
	w, body := getJSON(t, srv, "/api/sessions/root/collaboration?agent=codex", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("ok state: %d %s", w.Code, w.Body.String())
	}
	if body["state"] != "ok" || body["revision"].(float64) != 123 {
		t.Fatalf("body = %v", body)
	}
	if len(body["invocations"].([]any)) != 2 || len(body["delegations"].([]any)) != 1 {
		t.Fatalf("graph payload = %v", body)
	}
	tr := body["time_range"].(map[string]any)
	if tr["start"] != "2026-07-01T10:00:00Z" || tr["end"] != "2026-07-01T10:05:00Z" {
		t.Fatalf("time_range = %v", tr)
	}
	if strings.Contains(w.Body.String(), "user_message") || strings.Contains(w.Body.String(), "assistant_message") {
		t.Fatal("collaboration detail must not contain transcript bodies")
	}

	// Conditional refetch: 304 without any graph reparse.
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}
	w, _ = getJSON(t, srv, "/api/sessions/root/collaboration?agent=codex", map[string]string{"If-None-Match": etag})
	if w.Code != http.StatusNotModified {
		t.Fatalf("conditional request = %d, want 304", w.Code)
	}
	if n := atomic.LoadInt32(&collabReader.readCollabCalls); n != 0 {
		t.Fatalf("304 path reparsed the graph: %d ReadCollaboration calls", n)
	}

	// Stale retained graph: 200 with stale state and contract evidence.
	if err := database.MarkCollaborationStale("codex", "root", "parse failed"); err != nil {
		t.Fatal(err)
	}
	w, body = getJSON(t, srv, "/api/sessions/root/collaboration?agent=codex", nil)
	if w.Code != http.StatusOK || body["state"] != "stale" {
		t.Fatalf("stale state: %d %v", w.Code, body)
	}
	ev := body["state_evidence"].(map[string]any)
	if ev["reason_code"] != "stale_graph_retained" {
		t.Fatalf("state_evidence = %v", ev)
	}
	if w.Header().Get("ETag") == etag {
		t.Fatal("stale flip must change the ETag")
	}

	// Supported but not yet indexed.
	w, body = getJSON(t, srv, "/api/sessions/pending/collaboration?agent=codex", nil)
	if w.Code != http.StatusNotFound || body["code"] != "collaboration_not_indexed" {
		t.Fatalf("not indexed: %d %v", w.Code, body)
	}

	// Reader without the collaboration interface.
	w, body = getJSON(t, srv, "/api/sessions/p1/collaboration?agent=plain", nil)
	if w.Code != http.StatusNotFound || body["code"] != "collaboration_unsupported" {
		t.Fatalf("unsupported: %d %v", w.Code, body)
	}

	// Unknown session and missing composite identity.
	w, body = getJSON(t, srv, "/api/sessions/ghost/collaboration?agent=codex", nil)
	if w.Code != http.StatusNotFound || body["code"] != "session_not_found" {
		t.Fatalf("not found: %d %v", w.Code, body)
	}
	w, body = getJSON(t, srv, "/api/sessions/root/collaboration", nil)
	if w.Code != http.StatusBadRequest || body["code"] != "missing_agent" {
		t.Fatalf("missing agent: %d %v", w.Code, body)
	}
}

func TestRenderInvocationRouting(t *testing.T) {
	database := openCollabAPIDB(t)
	seedCollabSession(t, database, "codex", "root", false)

	rootID := collaboration.RootInvocationID("codex", "root")
	embeddedID := collaboration.ChildInvocationID("codex", "root", "embedded-1")
	backedID := collaboration.ChildInvocationID("codex", "root", "backed-1")
	lifecycleID := collaboration.ChildInvocationID("codex", "root", "lifecycle-1")

	embedded := apiChild("codex", "root", "embedded-1", collaboration.StatusCompleted)
	backed := apiChild("codex", "root", "backed-1", collaboration.StatusCompleted)
	backed.BackingSession = &collaboration.BackingSessionRef{AgentType: "codex", SessionID: "child-session"}
	lifecycle := apiChild("codex", "root", "lifecycle-1", collaboration.StatusCompleted)
	lifecycle.ContentPrecision = collaboration.FactEvidence{
		State:      collaboration.EvidenceEstimated,
		ReasonCode: collaboration.ReasonAggregateWindow,
	}
	if err := database.ReplaceCollaborationGraph(apiCollabGraph("codex", "root", 100, embedded, backed, lifecycle)); err != nil {
		t.Fatal(err)
	}

	rd := &collabStubReader{
		agentType: "codex",
		eventsByID: map[string][]model.RenderEvent{
			"root": {
				{Type: "UserPrompt", Text: "parent-only-question"},
				{Type: "TextChunk", Text: "embedded-child-answer", InvocationID: embeddedID},
				{Type: "TextChunk", Text: "parent-answer"},
			},
			"child-session": {
				{Type: "UserPrompt", Text: "backing-session-question"},
			},
		},
	}
	srv := New(database, []reader.BaseSessionReader{rd})

	get := func(url string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		srv.Mux.ServeHTTP(w, req)
		return w
	}

	// Root invocation preserves the current render behavior byte-for-byte.
	plain := get("/api/sessions/root/render")
	root := get("/api/sessions/root/render?agent=codex&invocation=" + rootID)
	if plain.Code != http.StatusOK || root.Code != http.StatusOK || plain.Body.String() != root.Body.String() {
		t.Fatalf("root invocation render diverged: plain=%d root=%d", plain.Code, root.Code)
	}

	// Embedded invocation: exactly the associated events.
	w := get("/api/sessions/root/render?agent=codex&invocation=" + embeddedID)
	if w.Code != http.StatusOK {
		t.Fatalf("embedded render: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "embedded-child-answer") {
		t.Fatal("embedded render missing the child event")
	}
	if strings.Contains(body, "parent-only-question") || strings.Contains(body, "parent-answer") {
		t.Fatal("embedded render leaked root events")
	}

	// Backed invocation: resolved through the backing session's reader.
	w = get("/api/sessions/root/render?agent=codex&invocation=" + backedID)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "backing-session-question") {
		t.Fatalf("backed render: %d %s", w.Code, w.Body.String())
	}

	// Lifecycle-only content: explicit typed unavailable, never a parent
	// window presented as exact.
	w = get("/api/sessions/root/render?agent=codex&invocation=" + lifecycleID)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("lifecycle-only render: %d %s", w.Code, w.Body.String())
	}
	var apiErr map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &apiErr); err != nil {
		t.Fatal(err)
	}
	if apiErr["code"] != "invocation_content_unavailable" || !strings.Contains(apiErr["detail"], "aggregate_window") {
		t.Fatalf("unavailable envelope = %v", apiErr)
	}

	// Unknown invocation, unindexed session, missing agent.
	w = get("/api/sessions/root/render?agent=codex&invocation=codex:root:child:nope")
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown invocation: %d", w.Code)
	}
	seedCollabSession(t, database, "codex", "unindexed", false)
	w = get("/api/sessions/unindexed/render?agent=codex&invocation=" + embeddedID)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unindexed collaboration render: %d", w.Code)
	}
	w = get("/api/sessions/root/render?invocation=" + embeddedID)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing agent on invocation render: %d", w.Code)
	}
}

// TestRenderInvocationEmbeddedWithoutAssociatedEvents covers the integration
// gap window: the graph claims exact embedded content but no adapter has set
// RenderEvent.InvocationID yet — the response must be the typed unavailable
// envelope, not the parent stream.
func TestRenderInvocationEmbeddedWithoutAssociatedEvents(t *testing.T) {
	database := openCollabAPIDB(t)
	seedCollabSession(t, database, "codex", "root", false)

	embeddedID := collaboration.ChildInvocationID("codex", "root", "embedded-1")
	embedded := apiChild("codex", "root", "embedded-1", collaboration.StatusCompleted)
	if err := database.ReplaceCollaborationGraph(apiCollabGraph("codex", "root", 100, embedded)); err != nil {
		t.Fatal(err)
	}

	rd := &collabStubReader{
		agentType: "codex",
		eventsByID: map[string][]model.RenderEvent{
			"root": {{Type: "UserPrompt", Text: "parent-only-question"}},
		},
	}
	srv := New(database, []reader.BaseSessionReader{rd})

	req := httptest.NewRequest("GET", "/api/sessions/root/render?agent=codex&invocation="+embeddedID, nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unassociated embedded render: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "parent-only-question") {
		t.Fatal("parent content presented as child content")
	}
}
