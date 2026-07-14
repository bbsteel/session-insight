package server

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/bbsteel/session-insight/internal/procfind"
	"github.com/bbsteel/session-insight/internal/reader"
)

// stopGrace is how long a force-stopped agent process gets between SIGTERM
// and SIGKILL — enough for codex to flush its rollout file and restore the
// terminal, short enough that the stop button feels immediate.
const stopGrace = 2 * time.Second

// findReaderForSession returns the reader that owns the session, using the
// same probe-each-reader pattern as handleGetSession.
func (s *Server) findReaderForSession(id string) (reader.BaseSessionReader, string) {
	for _, rd := range s.Readers {
		detail, err := rd.GetSession(id)
		if err != nil || detail == nil {
			continue
		}
		return rd, detail.AgentType
	}
	return nil, ""
}

// handleDeleteSession permanently deletes a session: the agent's own files
// (via the reader) plus every trace in the index DB. A session whose file is
// held open by a running process is refused with 409 so the frontend can
// offer force-stop instead — deleting the log under a live agent would break
// it mid-run.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rd, agentType := s.findReaderForSession(id)
	if rd == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	deleter, ok := rd.(reader.SessionDeleter)
	if !ok {
		http.Error(w, "delete not supported for agent type "+agentType, http.StatusNotImplemented)
		return
	}

	if finder, ok := rd.(reader.SessionProcessFinder); ok {
		pids, err := finder.SessionProcesses(id)
		if err != nil {
			http.Error(w, "check running processes: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if len(pids) > 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]any{
				"error": "session is running", "running": true, "pids": pids,
			})
			return
		}
	}

	if err := deleter.DeleteSession(id); err != nil {
		http.Error(w, "delete session: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if s.DB != nil {
		if err := s.DB.DeleteSessionData(agentType, id); err != nil {
			// Source files are already gone; the index row will also be
			// swept as an orphan on the next index round, so report but
			// don't fail the request.
			log.Printf("delete session %s/%s: index cleanup failed: %v", agentType, id, err)
		}
	}
	log.Printf("deleted session %s/%s", agentType, id)
	s.NotifySessionsChanged()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

// handleStopSession force-stops the agent processes that own a session, by
// the exact PIDs holding its file — never by name matching. Used from the
// delete dialog when the session turns out to be running.
func (s *Server) handleStopSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rd, agentType := s.findReaderForSession(id)
	if rd == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	finder, ok := rd.(reader.SessionProcessFinder)
	if !ok {
		http.Error(w, "stop not supported for agent type "+agentType, http.StatusNotImplemented)
		return
	}
	pids, err := finder.SessionProcesses(id)
	if err != nil {
		http.Error(w, "check running processes: "+err.Error(), http.StatusInternalServerError)
		return
	}
	for _, pid := range pids {
		if err := procfind.Terminate(pid, stopGrace); err != nil {
			http.Error(w, "stop process: "+err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("stopped session %s/%s process %d", agentType, id, pid)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"stopped": len(pids)})
}
