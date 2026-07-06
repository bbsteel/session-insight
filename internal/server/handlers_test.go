package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"session-insight/internal/db"
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
	agentType       string
	id              string
	sessions        []model.Session
	events          []model.RenderEvent
	getSessionCalls int
}

func (s *stubReader) AgentType() string {
	if s.agentType != "" {
		return s.agentType
	}
	return "stub"
}
func (s *stubReader) DisplayName() string { return s.AgentType() }
func (s *stubReader) ListSessions() ([]model.Session, error) {
	if s.sessions != nil {
		return s.sessions, nil
	}
	return nil, nil
}
func (s *stubReader) GetSession(id string) (*model.SessionDetail, error) {
	s.getSessionCalls++
	for _, sess := range s.sessions {
		if sess.ID == id {
			return &model.SessionDetail{Session: sess}, nil
		}
	}
	if id == s.id {
		return &model.SessionDetail{Session: model.Session{ID: id, AgentType: s.AgentType()}}, nil
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

func TestBookmarkHandlersAnnotateSessionsAndListBookmarks(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open db: %v", err)
	}
	defer database.Close()

	updated := time.Date(2026, 7, 6, 8, 0, 0, 0, time.UTC)
	rd := &stubReader{
		agentType: "claude",
		sessions: []model.Session{
			{
				ID:           "sess-1",
				AgentType:    "claude",
				Name:         "Fix bookmark UI",
				Project:      "session-insight",
				TurnCount:    2,
				MessageCount: 4,
				CreatedAt:    updated.Add(-time.Hour),
				UpdatedAt:    updated,
			},
			{
				ID:           "sess-2",
				AgentType:    "claude",
				Name:         "Other session",
				TurnCount:    1,
				MessageCount: 2,
				CreatedAt:    updated.Add(-2 * time.Hour),
				UpdatedAt:    updated.Add(-time.Minute),
			},
		},
	}
	srv := New(database, []reader.BaseSessionReader{rd})

	req := httptest.NewRequest("PUT", "/api/sessions/sess-1/bookmark?agent=claude", nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT bookmark expected 204, got %d: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/sessions", nil)
	w = httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET sessions expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var sessions []SessionSummary
	if err := json.NewDecoder(w.Body).Decode(&sessions); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	if !sessions[0].Bookmarked {
		t.Fatalf("expected sess-1 to be bookmarked in list: %+v", sessions[0])
	}
	if sessions[1].Bookmarked {
		t.Fatalf("expected sess-2 not to be bookmarked in list: %+v", sessions[1])
	}

	req = httptest.NewRequest("GET", "/api/sessions/sess-1", nil)
	w = httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET session expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var detail model.SessionDetail
	if err := json.NewDecoder(w.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if !detail.Bookmarked {
		t.Fatal("expected detail to be bookmarked")
	}

	req = httptest.NewRequest("GET", "/api/bookmarks", nil)
	w = httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET bookmarks expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var bookmarks []SessionSummary
	if err := json.NewDecoder(w.Body).Decode(&bookmarks); err != nil {
		t.Fatalf("decode bookmarks: %v", err)
	}
	if len(bookmarks) != 1 || bookmarks[0].ID != "sess-1" || !bookmarks[0].Bookmarked {
		t.Fatalf("unexpected bookmarks response: %+v", bookmarks)
	}

	req = httptest.NewRequest("DELETE", "/api/sessions/sess-1/bookmark?agent=claude", nil)
	w = httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE bookmark expected 204, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleListBookmarksUsesSessionSummaries(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open db: %v", err)
	}
	defer database.Close()

	updated := time.Date(2026, 7, 6, 8, 0, 0, 0, time.UTC)
	var sessions []model.Session
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("sess-%d", i+1)
		sessions = append(sessions, model.Session{
			ID:           id,
			AgentType:    "claude",
			Name:         fmt.Sprintf("Session %d", i+1),
			TurnCount:    1,
			MessageCount: 2,
			CreatedAt:    updated.Add(-time.Duration(i+1) * time.Hour),
			UpdatedAt:    updated.Add(-time.Duration(i) * time.Minute),
		})
		if err := database.AddBookmark("claude", id); err != nil {
			t.Fatalf("AddBookmark %s: %v", id, err)
		}
	}

	rd := &stubReader{agentType: "claude", sessions: sessions}
	srv := New(database, []reader.BaseSessionReader{rd})

	req := httptest.NewRequest("GET", "/api/bookmarks", nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET bookmarks expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var bookmarks []SessionSummary
	if err := json.NewDecoder(w.Body).Decode(&bookmarks); err != nil {
		t.Fatalf("decode bookmarks: %v", err)
	}
	if len(bookmarks) != 3 {
		t.Fatalf("expected 3 bookmarks, got %d", len(bookmarks))
	}
	if rd.getSessionCalls != 0 {
		t.Fatalf("expected bookmark list to use ListSessions summaries, called GetSession %d times", rd.getSessionCalls)
	}
}
