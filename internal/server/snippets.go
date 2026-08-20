package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/bbsteel/session-insight/internal/db"
)

type snippetRequest struct {
	Content     string `json:"content"`
	AgentType   string `json:"agent_type"`
	SessionID   string `json:"session_id"`
	SessionName string `json:"session_name"`
	SourceKind  string `json:"source_kind"`
	TurnIndex   *int   `json:"turn_index,omitempty"`
}

func (s *Server) handleListSnippets(w http.ResponseWriter, _ *http.Request) {
	if !s.requireDB(w) {
		return
	}
	snippets, err := s.DB.ListSnippets()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snippets)
}

func (s *Server) handleCreateSnippet(w http.ResponseWriter, r *http.Request) {
	if rejectUnsafeWrite(w, r) || !s.requireDB(w) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 131072)
	var request snippetRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	request.Content = strings.TrimSpace(request.Content)
	request.AgentType = strings.TrimSpace(request.AgentType)
	request.SessionID = strings.TrimSpace(request.SessionID)
	request.SessionName = strings.TrimSpace(request.SessionName)
	if request.Content == "" || request.AgentType == "" || request.SessionID == "" {
		http.Error(w, "content, agent_type, and session_id are required", http.StatusBadRequest)
		return
	}
	if len([]rune(request.Content)) > 20000 {
		http.Error(w, "snippet is too long", http.StatusBadRequest)
		return
	}
	if request.SourceKind != "selection" && request.SourceKind != "assistant" {
		http.Error(w, "invalid snippet source", http.StatusBadRequest)
		return
	}
	if request.TurnIndex != nil && *request.TurnIndex < 0 {
		http.Error(w, "invalid turn index", http.StatusBadRequest)
		return
	}

	snippet, err := s.DB.AddSnippet(db.Snippet{
		Content: request.Content, AgentType: request.AgentType, SessionID: request.SessionID,
		SessionName: request.SessionName, SourceKind: request.SourceKind, TurnIndex: request.TurnIndex,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(snippet)
}

func (s *Server) handleDeleteSnippet(w http.ResponseWriter, r *http.Request) {
	if rejectUnsafeWrite(w, r) || !s.requireDB(w) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid snippet id", http.StatusBadRequest)
		return
	}
	deleted, err := s.DB.DeleteSnippet(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !deleted {
		http.Error(w, "snippet not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
