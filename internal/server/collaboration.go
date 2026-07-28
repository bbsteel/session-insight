package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/render"
)

// CollaborationSummary is the compact aggregate attached to one root Session
// list row (frozen contract: three counts plus precision; never child arrays,
// labels, prompts, or timelines). The object is omitted entirely when
// collaboration is unsupported or the root is not yet indexed, which keeps an
// exact reader-confirmed zero-child graph distinguishable from "no data".
type CollaborationSummary struct {
	ChildCount   int    `json:"child_count"`
	ActiveCount  int    `json:"active_count"`
	ProblemCount int    `json:"problem_count"`
	Precision    string `json:"precision"`
	ReasonCode   string `json:"reason_code,omitempty"`
}

// collaborationTimeRange is the observed invocation time span the timeline UI
// needs for its initial time domain. Boundaries stay absent (null) when no
// invocation carries the corresponding evidence — never guessed.
type collaborationTimeRange struct {
	Start *time.Time `json:"start"`
	End   *time.Time `json:"end"`
}

// collaborationDetailResponse is the GET /api/sessions/{id}/collaboration
// payload: the stored normalized graph (metadata and anchors only — no
// transcript bodies), the index state, the observed time range, and the
// canonical validation projection (issues, canonical parents, unlinked and
// quarantined lists) so the UI does not re-derive contract semantics.
type collaborationDetailResponse struct {
	RootAgentType string                          `json:"root_agent_type"`
	RootSessionID string                          `json:"root_session_id"`
	Revision      int64                           `json:"revision"`
	State         string                          `json:"state"` // "ok" | "stale"
	StateEvidence *collaboration.FactEvidence     `json:"state_evidence,omitempty"`
	Completeness  collaboration.FactEvidence      `json:"completeness"`
	TimeRange     collaborationTimeRange          `json:"time_range"`
	Invocations   []collaboration.AgentInvocation `json:"invocations"`
	Delegations   []collaboration.Delegation      `json:"delegations"`
	Validation    collaboration.Validation        `json:"validation"`
}

// collaborationSupported reports whether the discovered reader for agentType
// implements the optional reader.CollaborationReader interface. The UI must
// not fabricate a graph for readers without it.
func (s *Server) collaborationSupported(agentType string) bool {
	for _, rd := range s.Readers {
		if rd.AgentType() != agentType {
			continue
		}
		_, ok := rd.(reader.CollaborationReader)
		return ok
	}
	return false
}

// handleGetCollaboration serves the stored normalized collaboration graph for
// one root Session. It reads only the SQLite index (a conditional 304 never
// reparses anything) and distinguishes: unknown session (404
// session_not_found), reader without the collaboration interface (404
// collaboration_unsupported), supported but not yet indexed (404
// collaboration_not_indexed), a retained stale graph after an interrupted
// re-index (200, state="stale" with stale_graph_retained evidence), and a
// current graph (200, state="ok").
func (s *Server) handleGetCollaboration(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeAPIError(w, http.StatusBadRequest, "missing_session_id")
		return
	}
	agentType := r.URL.Query().Get("agent")
	if agentType == "" {
		writeAPIError(w, http.StatusBadRequest, "missing_agent")
		return
	}
	if s.DB == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal", "database unavailable")
		return
	}

	indexed, err := s.DB.SessionIndexed(agentType, id)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if !indexed {
		writeAPIError(w, http.StatusNotFound, "session_not_found")
		return
	}

	stored, err := s.DB.GetCollaboration(agentType, id)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if stored == nil {
		if s.collaborationSupported(agentType) {
			writeAPIError(w, http.StatusNotFound, "collaboration_not_indexed")
		} else {
			writeAPIError(w, http.StatusNotFound, "collaboration_unsupported")
		}
		return
	}

	// Conditional requests: the tag tracks the graph revision and index state,
	// so a stale flip or a replacement both invalidate, and repeated conditional
	// requests return 304 served purely from the index (no graph reparse).
	etag := fmt.Sprintf(`"collab-%d-%d-%s"`, s.startNano, stored.Graph.Revision, stored.GraphStatus)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	var timeRange collaborationTimeRange
	for _, inv := range stored.Graph.Invocations {
		if inv.StartedAt != nil && (timeRange.Start == nil || inv.StartedAt.Before(*timeRange.Start)) {
			start := *inv.StartedAt
			timeRange.Start = &start
		}
		if inv.EndedAt != nil && (timeRange.End == nil || inv.EndedAt.After(*timeRange.End)) {
			end := *inv.EndedAt
			timeRange.End = &end
		}
	}

	var stateEvidence *collaboration.FactEvidence
	if stored.GraphStatus == db.CollaborationGraphStale {
		stateEvidence = &collaboration.FactEvidence{
			State:      collaboration.EvidenceEstimated,
			ReasonCode: collaboration.ReasonStaleGraphRetained,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(collaborationDetailResponse{ //nolint:errcheck
		RootAgentType: stored.Graph.RootAgentType,
		RootSessionID: stored.Graph.RootSessionID,
		Revision:      stored.Graph.Revision,
		State:         stored.GraphStatus,
		StateEvidence: stateEvidence,
		Completeness:  stored.Graph.Completeness,
		TimeRange:     timeRange,
		Invocations:   stored.Graph.Invocations,
		Delegations:   stored.Graph.Delegations,
		Validation:    stored.Validation,
	})
}

// routeInvocationRender implements the invocation dimension of
// GET /api/sessions/{id}/render?agent=…&invocation=… on top of the existing
// render pipeline — no second transcript model:
//
//   - the root invocation returns false so the caller falls through to the
//     unchanged root render;
//   - an embedded invocation renders exactly the root replay events carrying
//     its InvocationID;
//   - a backed invocation resolves its BackingSessionRef through that
//     Agent's reader;
//   - lifecycle-only or otherwise unreliable content gets an explicit typed
//     422 invocation_content_unavailable (carrying the contract precision
//     state and reason code) — a parent time window is never presented as
//     exact child content.
//
// It returns true when the request was fully handled (success or typed
// error), false only for the root-invocation fall-through.
func (s *Server) routeInvocationRender(w http.ResponseWriter, r *http.Request, id, invocationID string, cols int, opts render.Options) bool {
	agentType := r.URL.Query().Get("agent")
	if agentType == "" {
		writeAPIError(w, http.StatusBadRequest, "missing_agent", "invocation rendering requires the composite session identity (?agent=)")
		return true
	}
	if s.DB == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal", "database unavailable")
		return true
	}

	if invocationID == collaboration.RootInvocationID(agentType, id) {
		return false
	}

	stored, err := s.DB.GetCollaboration(agentType, id)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal", err.Error())
		return true
	}
	if stored == nil {
		writeAPIError(w, http.StatusNotFound, "collaboration_not_indexed")
		return true
	}

	var invocation *collaboration.AgentInvocation
	for i := range stored.Graph.Invocations {
		if stored.Graph.Invocations[i].ID == invocationID {
			invocation = &stored.Graph.Invocations[i]
			break
		}
	}
	if invocation == nil {
		writeAPIError(w, http.StatusNotFound, "invocation_not_found")
		return true
	}

	writeUnavailable := func(detail string) {
		writeAPIError(w, http.StatusUnprocessableEntity, "invocation_content_unavailable", detail)
	}

	if invocation.BackingSession != nil {
		backing := invocation.BackingSession
		for _, rd := range s.Readers {
			if rd.AgentType() != backing.AgentType {
				continue
			}
			events, err := rd.GetRenderEvents(backing.SessionID)
			if err != nil {
				writeUnavailable(fmt.Sprintf("backing session %s/%s unreadable: %v", backing.AgentType, backing.SessionID, err))
				return true
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write([]byte(render.FormatEventsOpts(events, cols, opts))) //nolint:errcheck
			return true
		}
		writeUnavailable(fmt.Sprintf("backing agent %q is not discovered on this machine", backing.AgentType))
		return true
	}

	if invocation.ContentPrecision.State != collaboration.EvidenceExact {
		detail := fmt.Sprintf("invocation content is %s", invocation.ContentPrecision.State)
		if invocation.ContentPrecision.ReasonCode != "" {
			detail += fmt.Sprintf(" (%s)", invocation.ContentPrecision.ReasonCode)
		}
		writeUnavailable(detail)
		return true
	}

	// Embedded invocation with exact content evidence: filter the root replay
	// to the events the adapter associated with this invocation.
	for _, rd := range s.Readers {
		if rd.AgentType() != agentType {
			continue
		}
		events, err := rd.GetRenderEvents(id)
		if err != nil {
			writeAPIError(w, http.StatusNotFound, "session_not_found", err.Error())
			return true
		}
		var filtered []model.RenderEvent
		for _, ev := range events {
			if ev.InvocationID == invocationID {
				filtered = append(filtered, ev)
			}
		}
		if len(filtered) == 0 {
			writeUnavailable("no render events are associated with this invocation yet (adapter event association pending)")
			return true
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(render.FormatEventsOpts(filtered, cols, opts))) //nolint:errcheck
		return true
	}
	writeAPIError(w, http.StatusNotFound, "session_not_found")
	return true
}
