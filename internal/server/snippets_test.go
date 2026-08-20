package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/db"
)

func TestSnippetAPIStoresListsAndDeletesSnapshot(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()
	server := New(database, nil)

	request := httptest.NewRequest(http.MethodPost, "/api/snippets", strings.NewReader(`{
		"content":"Keep this decision.","agent_type":"codex","session_id":"s-1",
		"session_name":"Snippet work","source_kind":"assistant","turn_index":2
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Mux.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST snippets status=%d body=%s", response.Code, response.Body.String())
	}
	var created db.Snippet
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatalf("decode created snippet: %v", err)
	}
	if created.Content != "Keep this decision." || created.TurnIndex == nil || *created.TurnIndex != 2 {
		t.Fatalf("unexpected created snippet: %+v", created)
	}

	response = httptest.NewRecorder()
	server.Mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/snippets", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET snippets status=%d body=%s", response.Code, response.Body.String())
	}
	var snippets []db.Snippet
	if err := json.NewDecoder(response.Body).Decode(&snippets); err != nil {
		t.Fatalf("decode snippets: %v", err)
	}
	if len(snippets) != 1 || snippets[0].ID != created.ID {
		t.Fatalf("unexpected snippets: %+v", snippets)
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/snippets/"+strconv.FormatInt(created.ID, 10), nil)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.Mux.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("DELETE snippets status=%d body=%s", response.Code, response.Body.String())
	}
}
