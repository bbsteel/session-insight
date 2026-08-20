package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/bbsteel/session-insight/internal/reader"
)

// agentInfo is one registered Agent row for the picker.
type agentInfo struct {
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
	Discovered  bool   `json:"discovered"`
}

// slotState is the resolved view of one logical screenshot file.
type slotState struct {
	LogicalName string `json:"logical_name"`
	Label       string `json:"label"`
	Hint        string `json:"hint"`
	Status      string `json:"status"`
	// LocalUnavailable: the catalog references content whose blob is gone
	// from this machine. Accepted presentation work stays valid.
	LocalUnavailable    bool           `json:"local_unavailable,omitempty"`
	Capture             *CaptureRecord `json:"capture,omitempty"`
	ImageURL            string         `json:"image_url,omitempty"`
	AcceptedHash        string         `json:"accepted_hash,omitempty"`
	AcceptedImageURL    string         `json:"accepted_image_url,omitempty"`
	NotApplicable       bool           `json:"not_applicable,omitempty"`
	NotApplicableReason string         `json:"not_applicable_reason,omitempty"`
}

type itemState struct {
	ChecklistItem
	Candidate *Candidate  `json:"candidate,omitempty"`
	Slots     []slotState `json:"slots"`
}

type workOrderView struct {
	WorkOrderRecord
	State string `json:"state"`
}

type scanStatus struct {
	Running         bool     `json:"running"`
	ScannedSessions int      `json:"scanned_sessions"`
	Errors          []string `json:"errors,omitempty"`
}

type stateResponse struct {
	StoreRoot      string          `json:"store_root"`
	CheckoutDir    string          `json:"checkout_dir"`
	Agents         []agentInfo     `json:"agents"`
	Agent          string          `json:"agent"`
	CurrentVersion string          `json:"current_version,omitempty"`
	Items          []itemState     `json:"items"`
	WorkOrders     []workOrderView `json:"work_orders"`
	Scan           scanStatus      `json:"scan"`
}

type server struct {
	store       *Store
	checkoutDir string
	agents      []agentInfo
	readers     map[string]reader.BaseSessionReader
	scanLimit   int

	mu       sync.Mutex
	reports  map[string]*CandidateReport
	scanning map[string]bool
}

func newServer(store *Store, checkoutDir string, readers []reader.BaseSessionReader, scanLimit int) *server {
	s := &server{
		store:       store,
		checkoutDir: checkoutDir,
		readers:     map[string]reader.BaseSessionReader{},
		scanLimit:   scanLimit,
		reports:     map[string]*CandidateReport{},
		scanning:    map[string]bool{},
	}
	discovered := map[string]bool{}
	for _, r := range readers {
		s.readers[r.AgentType()] = r
		discovered[r.AgentType()] = true
	}
	for _, def := range reader.AgentDefinitions() {
		if def.AgentType == "imported" {
			continue // imported sessions have no native CLI to reference
		}
		s.agents = append(s.agents, agentInfo{
			Type:        def.AgentType,
			DisplayName: def.DisplayName,
			Discovered:  discovered[def.AgentType],
		})
	}
	sort.Slice(s.agents, func(i, j int) bool { return s.agents[i].Type < s.agents[j].Type })
	return s
}

func (s *server) knownAgent(agent string) bool {
	for _, a := range s.agents {
		if a.Type == agent {
			return true
		}
	}
	return false
}

// refreshCandidates runs candidate discovery for one Agent in the background.
func (s *server) refreshCandidates(agent string) {
	s.mu.Lock()
	if s.scanning[agent] {
		s.mu.Unlock()
		return
	}
	source, ok := s.readers[agent]
	if !ok {
		report := &CandidateReport{Agent: agent}
		report.Errors = append(report.Errors, "agent storage not discovered on this machine")
		s.reports[agent] = report
		s.mu.Unlock()
		return
	}
	s.scanning[agent] = true
	s.mu.Unlock()

	go func() {
		report := DiscoverCandidates(source, s.scanLimit)
		s.mu.Lock()
		s.reports[agent] = report
		s.scanning[agent] = false
		s.mu.Unlock()
		log.Printf("candidate scan %s: %d sessions, %d candidates", agent, report.ScannedSessions, len(report.Candidates))
	}()
}

func (s *server) report(agent string) (*CandidateReport, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reports[agent], s.scanning[agent]
}

func (s *server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("GET /api/image", s.handleImage)
	mux.HandleFunc("POST /api/upload", s.handleUpload)
	mux.HandleFunc("POST /api/accept", s.handleAccept)
	mux.HandleFunc("POST /api/not-applicable", s.handleNotApplicable)
	mux.HandleFunc("POST /api/version", s.handleVersion)
	mux.HandleFunc("POST /api/rescan", s.handleRescan)
	mux.HandleFunc("POST /api/work-order", s.handleWorkOrder)
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// jsonBody decodes a small JSON request body.
func jsonBody(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}

func (s *server) validAgentOr400(w http.ResponseWriter, agent string) bool {
	if !s.knownAgent(agent) {
		writeError(w, http.StatusBadRequest, "unknown agent "+agent)
		return false
	}
	return true
}

func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	agent := r.URL.Query().Get("agent")
	if agent == "" && len(s.agents) > 0 {
		agent = s.agents[0].Type
	}
	if !s.validAgentOr400(w, agent) {
		return
	}
	report, scanning := s.report(agent)
	if report == nil && !scanning {
		s.refreshCandidates(agent)
		scanning = true
	}
	cat, err := s.store.LoadCatalog(agent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var candidates map[string]*Candidate
	scan := scanStatus{Running: scanning}
	if report != nil {
		candidates = report.ByItem()
		scan.ScannedSessions = report.ScannedSessions
		scan.Errors = report.Errors
	}

	resp := stateResponse{
		StoreRoot:      s.store.Root,
		CheckoutDir:    s.checkoutDir,
		Agents:         s.agents,
		Agent:          agent,
		CurrentVersion: cat.CurrentVersion,
		Scan:           scan,
		WorkOrders:     []workOrderView{},
	}
	for _, item := range checklist {
		view := itemState{ChecklistItem: item, Candidate: candidates[item.ID]}
		for _, slot := range item.Slots {
			st := cat.Items[slot.LogicalName]
			resolved := resolveStatus(st, candidates[item.ID] != nil, s.store.blobExists(agent, currentCapture(st)))
			ss := slotState{
				LogicalName:      slot.LogicalName,
				Label:            slot.Label,
				Hint:             slot.Hint,
				Status:           resolved.Status,
				LocalUnavailable: resolved.LocalUnavailable,
			}
			if st != nil {
				ss.NotApplicable = st.NotApplicable
				ss.NotApplicableReason = st.NotApplicableReason
				ss.AcceptedHash = st.AcceptedHash
				if st.Current != nil {
					ss.Capture = st.Current
					ss.ImageURL = fmt.Sprintf("/api/image?agent=%s&hash=%s", agent, st.Current.Hash)
				}
				if st.AcceptedHash != "" && (st.Current == nil || st.AcceptedHash != st.Current.Hash) {
					ss.AcceptedImageURL = fmt.Sprintf("/api/image?agent=%s&hash=%s", agent, st.AcceptedHash)
				}
			}
			view.Slots = append(view.Slots, ss)
		}
		resp.Items = append(resp.Items, view)
	}
	for _, record := range cat.WorkOrders {
		resp.WorkOrders = append(resp.WorkOrders, workOrderView{record, workOrderState(record, cat)})
	}
	writeJSON(w, http.StatusOK, resp)
}

func currentCapture(st *ItemState) *CaptureRecord {
	if st == nil {
		return nil
	}
	return st.Current
}

// handleImage serves one blob by content hash, but only hashes the catalog
// knows. The store directory is never exposed as static content.
func (s *server) handleImage(w http.ResponseWriter, r *http.Request) {
	agent := r.URL.Query().Get("agent")
	hash := r.URL.Query().Get("hash")
	if !s.validAgentOr400(w, agent) {
		return
	}
	path, ext, err := s.store.lookupBlob(agent, hash)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache")
	switch ext {
	case ".png":
		w.Header().Set("Content-Type", "image/png")
	case ".jpg":
		w.Header().Set("Content-Type", "image/jpeg")
	case ".gif":
		w.Header().Set("Content-Type", "image/gif")
	}
	http.ServeFile(w, r, path)
}

// handleUpload imports one dropped screenshot into the referenced slot.
func (s *server) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImageBytes+1<<20)
	if err := r.ParseMultipartForm(maxImageBytes); err != nil {
		writeError(w, http.StatusBadRequest, "multipart form too large or malformed")
		return
	}
	agent := r.FormValue("agent")
	logicalName := r.FormValue("logical_name")
	if !s.validAgentOr400(w, agent) {
		return
	}
	if !knownLogicalNames[logicalName] {
		writeError(w, http.StatusBadRequest, "unknown logical file name "+logicalName)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer file.Close() //nolint:errcheck
	rec, err := s.store.Import(agent, logicalName, file, header.Filename)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "capture": rec})
}

// handleAccept records the current capture as the accepted content version.
// This is local bookkeeping only; it never edits product code and never
// auto-accepts an update_available input.
func (s *server) handleAccept(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Agent       string `json:"agent"`
		LogicalName string `json:"logical_name"`
	}
	if err := jsonBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.validAgentOr400(w, req.Agent) {
		return
	}
	if !knownLogicalNames[req.LogicalName] {
		writeError(w, http.StatusBadRequest, "unknown logical file name")
		return
	}
	err := s.store.UpdateCatalog(req.Agent, func(cat *AgentCatalog) error {
		st := cat.item(req.LogicalName)
		if st.Current == nil {
			return fmt.Errorf("nothing captured for %s", req.LogicalName)
		}
		st.AcceptedHash = st.Current.Hash
		st.AcceptedExt = st.Current.Ext
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleNotApplicable marks a scene as researched-absent (or clears that
// mark). A reason is required; the state is never derived from missing images.
func (s *server) handleNotApplicable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Agent       string `json:"agent"`
		LogicalName string `json:"logical_name"`
		Value       bool   `json:"value"`
		Reason      string `json:"reason"`
	}
	if err := jsonBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.validAgentOr400(w, req.Agent) {
		return
	}
	if !knownLogicalNames[req.LogicalName] {
		writeError(w, http.StatusBadRequest, "unknown logical file name")
		return
	}
	if req.Value && strings.TrimSpace(req.Reason) == "" {
		writeError(w, http.StatusBadRequest, "a reason is required: not_applicable must come from adapter research, never from a missing image")
		return
	}
	err := s.store.UpdateCatalog(req.Agent, func(cat *AgentCatalog) error {
		st := cat.item(req.LogicalName)
		st.NotApplicable = req.Value
		st.NotApplicableReason = strings.TrimSpace(req.Reason)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleVersion records version context. A version change never invalidates
// evidence; it is displayed next to captures as observation context only.
func (s *server) handleVersion(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Agent       string `json:"agent"`
		LogicalName string `json:"logical_name"` // empty = installed CLI version for the agent
		Version     string `json:"version"`
	}
	if err := jsonBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.validAgentOr400(w, req.Agent) {
		return
	}
	if len(req.Version) > 120 {
		writeError(w, http.StatusBadRequest, "version string too long")
		return
	}
	err := s.store.UpdateCatalog(req.Agent, func(cat *AgentCatalog) error {
		if req.LogicalName == "" {
			cat.CurrentVersion = req.Version
			return nil
		}
		if !knownLogicalNames[req.LogicalName] {
			return fmt.Errorf("unknown logical file name")
		}
		st := cat.item(req.LogicalName)
		if st.Current == nil {
			return fmt.Errorf("nothing captured for %s", req.LogicalName)
		}
		st.Current.ObservedVersion = req.Version
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleRescan imports canonically named drop-in files and restarts candidate
// discovery. There is no prepare/--capture command: folder scans do the work.
func (s *server) handleRescan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Agent string `json:"agent"`
	}
	if err := jsonBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.validAgentOr400(w, req.Agent) {
		return
	}
	imported, skipped, err := s.store.ScanDropIns(req.Agent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.refreshCandidates(req.Agent)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "imported": imported, "skipped": skipped})
}

// handleWorkOrder freezes the pending inputs into a work order. This is the
// manager's output boundary: no goals, branches, PRs or product-code edits.
func (s *server) handleWorkOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Agent string `json:"agent"`
	}
	if err := jsonBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.validAgentOr400(w, req.Agent) {
		return
	}
	report, _ := s.report(req.Agent)
	var candidates map[string]*Candidate
	if report != nil {
		candidates = report.ByItem()
	}
	record, err := generateWorkOrder(s.store, s.checkoutDir, req.Agent, candidates)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "work_order": record})
}

// listen binds a random loopback port. The manager never listens on external
// interfaces.
func listenLoopback() (net.Listener, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("bind loopback: %w", err)
	}
	return listener, nil
}
