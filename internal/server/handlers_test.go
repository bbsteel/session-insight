package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
