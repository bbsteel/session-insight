package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/reader/capability"
)

type provAPIReader struct {
	agent  string
	detail *model.SessionDetail
	err    error
}

func (r *provAPIReader) AgentType() string   { return r.agent }
func (r *provAPIReader) DisplayName() string { return r.agent }
func (r *provAPIReader) ListSessions() ([]model.Session, error) {
	if r.detail == nil {
		return nil, nil
	}
	return []model.Session{r.detail.Session}, nil
}
func (r *provAPIReader) GetSession(id string) (*model.SessionDetail, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.detail == nil || r.detail.ID != id {
		return nil, nil
	}
	return r.detail, nil
}
func (r *provAPIReader) RenderANSI(id string, cols int) (string, error) {
	return "", nil
}
func (r *provAPIReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	return nil, nil
}

func openServerDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestDetailIncludesProvenance(t *testing.T) {
	now := time.Now().UTC()
	path := "/tmp/secret/session.jsonl"
	prov := model.SessionProvenance{
		State: model.RecordDegraded, CapturedAt: now, AdapterRevision: 2,
		Sources: []model.SessionSourceFile{{
			Role: model.SourceRolePrimaryTranscript, Path: path, State: model.SourcePresent,
		}},
		Warnings: []model.ParseWarning{{
			Code: model.WarnMalformedRecordSkipped, Severity: model.WarningSeverityWarning,
			AffectsCompleteness: true, Impacts: []string{model.ImpactReplay}, Count: 2,
		}},
		WarningSummary: model.WarningSummary{Total: 2, Warning: 2, ImpactCounts: map[string]int{"replay": 2}},
	}
	detail := &model.SessionDetail{
		Session:    model.Session{ID: "s1", AgentType: "claude", Name: "n", CreatedAt: now, UpdatedAt: now},
		Turns:      []model.TurnVM{{TurnIndex: 0, UserMessage: "hi"}},
		Provenance: &prov,
	}
	// Register-less: AgentDefinition for claude exists in catalog.
	rd := &provAPIReader{agent: "claude", detail: detail}
	s := New(openServerDB(t), []reader.BaseSessionReader{rd})
	req := httptest.NewRequest("GET", "/api/sessions/s1", nil)
	w := httptest.NewRecorder()
	s.Mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	p, ok := body["provenance"].(map[string]any)
	if !ok {
		t.Fatalf("missing provenance: %v", body)
	}
	if p["state"] != "degraded" {
		t.Fatalf("state=%v", p["state"])
	}
	// capability must still be present and independent
	if _, ok := body["agent_capabilities"]; !ok {
		t.Fatal("expected agent_capabilities alongside provenance")
	}
}

func TestListCompactRecordStatusNoPaths(t *testing.T) {
	database := openServerDB(t)
	now := time.Now().UTC()
	prov := model.SessionProvenance{
		State: model.RecordDegraded, CapturedAt: now, AdapterRevision: 1,
		WarningSummary: model.WarningSummary{Total: 3},
		Sources: []model.SessionSourceFile{{
			Role: "primary_transcript", Path: "/secret/path.jsonl", State: model.SourcePresent,
		}},
	}
	sess := model.Session{ID: "s1", AgentType: "claude", Name: "n", CreatedAt: now, UpdatedAt: now}
	if err := database.ReplaceSessionSnapshot(db.SessionSnapshotWrite{
		AgentType: "claude", Session: sess, TurnCount: 1, MessageCount: 1,
		Turns: []db.TurnText{{TurnIndex: 0, Role: "user", Content: "x"}},
		Provenance: &prov, Revision: 1,
	}); err != nil {
		t.Fatal(err)
	}
	s := New(database, nil)
	req := httptest.NewRequest("GET", "/api/sessions", nil)
	w := httptest.NewRecorder()
	s.Mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "/secret") {
		t.Fatalf("list leaked path: %s", body)
	}
	var list []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("len=%d", len(list))
	}
	rs, ok := list[0]["record_status"].(map[string]any)
	if !ok {
		t.Fatalf("missing record_status: %v", list[0])
	}
	if rs["state"] != "degraded" {
		t.Fatalf("state=%v", rs["state"])
	}
	if int(rs["warning_count"].(float64)) != 3 {
		t.Fatalf("warning_count=%v", rs["warning_count"])
	}
}

func TestMetadataEnvelope200VsUnknown404(t *testing.T) {
	database := openServerDB(t)
	now := time.Now().UTC()
	ms := now.Add(-time.Hour)
	prov := model.SessionProvenance{
		State: model.RecordSourceMissing, CapturedAt: now, AdapterRevision: 1,
		MissingSince: &ms, ReasonCode: "source_missing",
		Sources: []model.SessionSourceFile{{
			Role: "primary_transcript", Path: "/gone.jsonl", State: model.SourceMissing,
		}},
	}
	sess := model.Session{ID: "tomb-1", AgentType: "claude", Name: "gone", CreatedAt: now, UpdatedAt: now}
	if err := database.ReplaceSessionSnapshot(db.SessionSnapshotWrite{
		AgentType: "claude", Session: sess, Provenance: &prov, Revision: 1,
	}); err != nil {
		t.Fatal(err)
	}
	// Reader returns nothing; envelope from DB.
	s := New(database, []reader.BaseSessionReader{&provAPIReader{agent: "claude"}})

	req := httptest.NewRequest("GET", "/api/sessions/tomb-1", nil)
	w := httptest.NewRecorder()
	s.Mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("envelope status=%d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["record_available"] != false {
		t.Fatalf("record_available=%v", body["record_available"])
	}
	turns, _ := body["turns"].([]any)
	if len(turns) != 0 {
		t.Fatalf("turns should be empty")
	}
	p := body["provenance"].(map[string]any)
	if p["state"] != "source_missing" {
		t.Fatalf("state=%v", p["state"])
	}

	req2 := httptest.NewRequest("GET", "/api/sessions/never-indexed", nil)
	w2 := httptest.NewRecorder()
	s.Mux.ServeHTTP(w2, req2)
	if w2.Code != 404 {
		t.Fatalf("unknown id status=%d", w2.Code)
	}
}

func TestRemoveFromIndexOnlySourceMissing(t *testing.T) {
	database := openServerDB(t)
	now := time.Now().UTC()
	ms := now
	prov := model.SessionProvenance{
		State: model.RecordSourceMissing, CapturedAt: now, AdapterRevision: 1, MissingSince: &ms,
	}
	sess := model.Session{ID: "t1", AgentType: "claude", CreatedAt: now, UpdatedAt: now}
	_ = database.ReplaceSessionSnapshot(db.SessionSnapshotWrite{
		AgentType: "claude", Session: sess, Provenance: &prov, Revision: 1,
	})
	s := New(database, nil)
	req := httptest.NewRequest("DELETE", "/api/sessions/t1/index", nil)
	w := httptest.NewRecorder()
	s.Mux.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	_, ok, _ := database.GetSessionRow("claude", "t1")
	if ok {
		t.Fatal("expected full index removal")
	}
}

func TestSearchMarksSourceMissingStale(t *testing.T) {
	database := openServerDB(t)
	now := time.Now().UTC()
	ms := now
	prov := model.SessionProvenance{
		State: model.RecordSourceMissing, CapturedAt: now, AdapterRevision: 1, MissingSince: &ms,
	}
	sess := model.Session{ID: "s1", AgentType: "claude", Name: "n", CreatedAt: now, UpdatedAt: now}
	_ = database.ReplaceSessionSnapshot(db.SessionSnapshotWrite{
		AgentType: "claude", Session: sess,
		Turns: []db.TurnText{{TurnIndex: 0, Role: "user", Content: "unique-tombstone-token"}},
		Provenance: &prov, Revision: 1,
	})
	s := New(database, nil)
	req := httptest.NewRequest("GET", "/api/search?q=unique-tombstone-token", nil)
	w := httptest.NewRecorder()
	s.Mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	var hits []map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &hits)
	if len(hits) == 0 {
		t.Fatal("expected hit")
	}
	if hits[0]["source_missing"] != true || hits[0]["stale"] != true {
		t.Fatalf("hit flags: %v", hits[0])
	}
}

func TestCapabilityNotOverwrittenByCompleteness(t *testing.T) {
	// Sanity: degraded provenance coexists with exact replay capability shape.
	now := time.Now().UTC()
	detail := &model.SessionDetail{
		Session: model.Session{ID: "s1", AgentType: "claude", CreatedAt: now, UpdatedAt: now},
		Turns:   []model.TurnVM{{TurnIndex: 0, UserMessage: "x"}},
		Provenance: &model.SessionProvenance{
			State: model.RecordDegraded, CapturedAt: now, AdapterRevision: 2,
			Warnings: []model.ParseWarning{{
				Code: "malformed_record_skipped", Severity: "warning", AffectsCompleteness: true, Count: 1,
				Impacts: []string{"replay"},
			}},
			WarningSummary: model.WarningSummary{Total: 1, Warning: 1},
			Sources:        []model.SessionSourceFile{{Role: "primary_transcript", Path: "/p", State: model.SourcePresent}},
		},
	}
	rd := &provAPIReader{agent: "claude", detail: detail}
	s := New(openServerDB(t), []reader.BaseSessionReader{rd})
	req := httptest.NewRequest("GET", "/api/sessions/s1", nil)
	w := httptest.NewRecorder()
	s.Mux.ServeHTTP(w, req)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	caps := body["agent_capabilities"].(map[string]any)
	status := caps["status"].(map[string]any)
	replay := status["replay"].(map[string]any)
	if replay["state"] == "degraded" {
		t.Fatal("capability must not inherit record degraded state")
	}
	// Ensure capability package types are still valid
	_ = capability.CapabilityExact
}
