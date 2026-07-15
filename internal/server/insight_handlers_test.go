package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
)

// insightReader returns a fixed SessionDetail so handler tests can drive the
// snapshot/generation path deterministically.
type insightReader struct {
	detail *model.SessionDetail
}

func (r *insightReader) AgentType() string   { return "copilot" }
func (r *insightReader) DisplayName() string { return "Copilot" }
func (r *insightReader) ListSessions() ([]model.Session, error) {
	return []model.Session{r.detail.Session}, nil
}
func (r *insightReader) GetSession(id string) (*model.SessionDetail, error) {
	if id == r.detail.ID {
		return r.detail, nil
	}
	return nil, nil
}
func (r *insightReader) RenderANSI(id string, cols int) (string, error) { return "", nil }
func (r *insightReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	return nil, nil
}

// findingDetail builds a non-live session whose turn 2 (400 tool calls) trips
// the tool-loop finding, so the bundle exposes finding tool_loop -> turn:2.
func findingDetail() *model.SessionDetail {
	old := time.Now().Add(-1 * time.Hour)
	exact := model.TokenPresence{Output: model.PresenceExact}
	return &model.SessionDetail{
		Session: model.Session{ID: "sess-1", AgentType: "copilot", UpdatedAt: old, CreatedAt: old},
		Turns: []model.TurnVM{
			{TurnIndex: 0, UserMessage: "实现", RequestCount: 2, TokenUsage: model.TokenUsage{CompletionTokens: 5, Present: exact}},
			{TurnIndex: 1, UserMessage: "继续", RequestCount: 2, TokenUsage: model.TokenUsage{CompletionTokens: 5, Present: exact}},
			{TurnIndex: 2, UserMessage: "修复", RequestCount: 3, ToolCallCount: 400, AssistantMessage: "done", TokenUsage: model.TokenUsage{CompletionTokens: 5, Present: exact}},
		},
	}
}

func newInsightServer(t *testing.T, detail *model.SessionDetail) *Server {
	t.Helper()
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return New(database, []reader.BaseSessionReader{&insightReader{detail: detail}})
}

func addProvider(t *testing.T, s *Server, baseURL string) {
	t.Helper()
	id, err := s.DB.AddLLMProvider(db.LLMProvider{Name: "fake", Kind: "api", BaseURL: baseURL, ModelID: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	s.DB.SetDefaultLLMProviderID(id)
}

// fakeModel serves an OpenAI-compatible /chat/completions returning `content`.
func fakeModel(content string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "m1"}}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": content}}},
		})
	}))
}

func postInsight(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/sessions/sess-1/ai/insight", strings.NewReader(body))
	req.Header.Set("Origin", "http://localhost")
	req.Header.Set("Content-Type", "application/json")
	req.Host = "localhost"
	w := httptest.NewRecorder()
	s.Mux.ServeHTTP(w, req)
	return w
}

func TestInsightNoProviderReturns412(t *testing.T) {
	s := newInsightServer(t, findingDetail())
	w := postInsight(t, s, `{}`)
	if w.Code != http.StatusPreconditionFailed {
		t.Errorf("want 412 without provider, got %d: %s", w.Code, w.Body.String())
	}
}

func TestInsightSessionNotFound(t *testing.T) {
	s := newInsightServer(t, findingDetail())
	addProvider(t, s, "http://unused")
	req := httptest.NewRequest("POST", "/api/sessions/nope/ai/insight", strings.NewReader(`{"confirm_target":true}`))
	req.Header.Set("Origin", "http://localhost")
	req.Header.Set("Content-Type", "application/json")
	req.Host = "localhost"
	w := httptest.NewRecorder()
	s.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestInsightActiveSessionRejected(t *testing.T) {
	detail := findingDetail()
	detail.UpdatedAt = time.Now() // live now
	s := newInsightServer(t, detail)
	addProvider(t, s, "http://unused")
	w := postInsight(t, s, `{"confirm_target":true}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "session_active") {
		t.Errorf("active session must be 409 session_active, got %d: %s", w.Code, w.Body.String())
	}
}

func TestInsightNoFindings(t *testing.T) {
	old := time.Now().Add(-time.Hour)
	detail := &model.SessionDetail{
		Session: model.Session{ID: "sess-1", AgentType: "copilot", UpdatedAt: old, CreatedAt: old},
		Turns:   []model.TurnVM{{TurnIndex: 0, UserMessage: "hi", RequestCount: 1}},
	}
	s := newInsightServer(t, detail)
	addProvider(t, s, "http://unused")
	w := postInsight(t, s, `{"confirm_target":true}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 no_findings, got %d: %s", w.Code, w.Body.String())
	}
}

func TestInsightUnconfirmedTargetReturnsPreview(t *testing.T) {
	s := newInsightServer(t, findingDetail())
	addProvider(t, s, "http://unused")
	w := postInsight(t, s, `{}`) // no confirm
	if w.Code != http.StatusOK {
		t.Fatalf("preview should be 200, got %d", w.Code)
	}
	var pv sendPreview
	if err := json.Unmarshal(w.Body.Bytes(), &pv); err != nil {
		t.Fatal(err)
	}
	if !pv.NeedsConfirmation || pv.FactCount == 0 || pv.TargetFingerprint == "" {
		t.Errorf("bad preview: %+v", pv)
	}
}

func TestInsightHappyPathPersistsAndRenders(t *testing.T) {
	fm := fakeModel(`{"schema_version":1,"summary":"级联放大","insights":[{"title":"工具循环","finding_codes":["tool_loop"],"confidence":"medium","cause":{"statement":"单轮大量工具调用","epistemic_status":"inferred","causal_strength":"moderate","evidence_ids":["turn:2"]},"impact":{"statement":"请求增多","evidence_ids":["turn:2"]}}],"evidence_gaps":[]}`)
	defer fm.Close()
	s := newInsightServer(t, findingDetail())
	addProvider(t, s, fm.URL)

	w := postInsight(t, s, `{"confirm_target":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 SSE, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: done") {
		t.Fatalf("no done event in SSE:\n%s", body)
	}
	// Persisted and readable back with resolved evidence in the markdown.
	gen, err := s.DB.LatestAIGeneration("insight", "copilot", "sess-1")
	if err != nil || gen == nil {
		t.Fatalf("insight not persisted: %v", err)
	}
	if gen.PromptVersion != "findings-insight-v1" || gen.SourceFingerprint == "" {
		t.Errorf("freshness fields not stored: %+v", gen)
	}
	if !strings.Contains(gen.Content, "工具循环") {
		t.Errorf("markdown missing insight title:\n%s", gen.Content)
	}
	var meta map[string]any
	json.Unmarshal([]byte(gen.Metadata), &meta)
	if _, ok := meta["output"]; !ok {
		t.Errorf("structured output not stored in metadata: %s", gen.Metadata)
	}
}

func TestInsightParseFailureFallback(t *testing.T) {
	fm := fakeModel("这不是 JSON，只是自由文本 <script>")
	defer fm.Close()
	s := newInsightServer(t, findingDetail())
	addProvider(t, s, fm.URL)

	w := postInsight(t, s, `{"confirm_target":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("parse failure should still complete SSE, got %d", w.Code)
	}
	gen, _ := s.DB.LatestAIGeneration("insight", "copilot", "sess-1")
	if gen == nil {
		t.Fatal("fallback generation not persisted")
	}
	if strings.Contains(gen.Content, "<script>") {
		t.Errorf("raw HTML must be escaped in fallback: %s", gen.Content)
	}
	if !strings.Contains(gen.Metadata, "parse_failed") {
		t.Errorf("metadata must mark parse_failed: %s", gen.Metadata)
	}
}

func TestInsightRevokeReTriggersPreview(t *testing.T) {
	fm := fakeModel(`{"schema_version":1,"summary":"x","insights":[],"evidence_gaps":["无数据"]}`)
	defer fm.Close()
	s := newInsightServer(t, findingDetail())
	addProvider(t, s, fm.URL)

	// Confirm once (generation runs), then revoke.
	if w := postInsight(t, s, `{"confirm_target":true}`); w.Code != http.StatusOK {
		t.Fatalf("first generate failed: %d", w.Code)
	}
	// Without revoke, a bare request now proceeds (target confirmed) → SSE.
	if w := postInsight(t, s, `{}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "event:") {
		t.Fatalf("confirmed target should skip preview, got %d: %s", w.Code, w.Body.String())
	}
	// Revoke, then a bare request must show the preview again.
	rev := httptest.NewRequest("POST", "/api/insight/targets/revoke", nil)
	rev.Header.Set("Origin", "http://localhost")
	rev.Header.Set("Content-Type", "application/json")
	rev.Host = "localhost"
	rw := httptest.NewRecorder()
	s.Mux.ServeHTTP(rw, rev)
	if rw.Code != http.StatusNoContent {
		t.Fatalf("revoke failed: %d", rw.Code)
	}
	w := postInsight(t, s, `{}`)
	var pv sendPreview
	if json.Unmarshal(w.Body.Bytes(), &pv) != nil || !pv.NeedsConfirmation {
		t.Errorf("revoke must re-trigger preview, got %s", w.Body.String())
	}
}
