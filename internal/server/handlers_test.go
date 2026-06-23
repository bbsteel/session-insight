package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"session-insight/internal/model"
	"session-insight/internal/reader"
)

func TestHandleListSessionsEmpty(t *testing.T) {
	srv := &Server{
		Readers: nil,
	}
	srv.Mux = http.NewServeMux()
	srv.registerRoutes()

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var sessions []SessionSummary
	if err := json.NewDecoder(w.Body).Decode(&sessions); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}

	if w.Header().Get("X-Total-Count") != "0" {
		t.Errorf("expected X-Total-Count 0, got %s", w.Header().Get("X-Total-Count"))
	}
}

// stubReader is a minimal BaseSessionReader for handler tests.
type stubReader struct {
	id     string
	events []model.RenderEvent
}

func (s *stubReader) AgentType() string                               { return "stub" }
func (s *stubReader) DisplayName() string                             { return "stub" }
func (s *stubReader) ListSessions() ([]model.Session, error)          { return nil, nil }
func (s *stubReader) GetSession(id string) (*model.SessionDetail, error) {
	if id == s.id {
		return &model.SessionDetail{Session: model.Session{ID: id}}, nil
	}
	return nil, nil
}
func (s *stubReader) RenderANSI(id string, cols int) (string, error) { return "", nil }
func (s *stubReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	if id == s.id {
		return s.events, nil
	}
	return nil, nil
}

func TestHandleSessionEditsMultiFile(t *testing.T) {
	patch := "*** Begin Patch\n" +
		"*** Update File: a.go\n" +
		"@@ -1 +1 @@\n" +
		"-aold\n" +
		"+anew\n" +
		"*** Update File: b.go\n" +
		"@@ -1 +1 @@\n" +
		"-bold\n" +
		"+bnew\n" +
		"*** End Patch"

	rd := &stubReader{
		id: "sess-1",
		events: []model.RenderEvent{
			{
				Type:      "ToolInvocation",
				TurnIndex: 0,
				ToolName:  "apply_patch",
				ToolInput: map[string]any{"args": patch},
			},
		},
	}

	srv := New(nil, []reader.BaseSessionReader{rd})

	req := httptest.NewRequest("GET", "/api/sessions/sess-1/edits", nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var edits []model.EditCall
	if err := json.NewDecoder(w.Body).Decode(&edits); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(edits) != 2 {
		t.Fatalf("expected 2 EditCalls from multi-file patch, got %d", len(edits))
	}
	if edits[0].FilePath != "a.go" || edits[1].FilePath != "b.go" {
		t.Errorf("unexpected file paths: %q %q", edits[0].FilePath, edits[1].FilePath)
	}
}
