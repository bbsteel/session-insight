package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/reader/capability"
	"github.com/bbsteel/session-insight/internal/terminal"
)

const (
	resumeVerificationGrace             = 15 * time.Second
	terminalVerificationPersistInterval = 5 * time.Second
)

type resumePlanResponse struct {
	Status         string                           `json:"status"`
	AgentType      string                           `json:"agent_type"`
	SessionID      string                           `json:"session_id"`
	CWD            string                           `json:"cwd"`
	Command        string                           `json:"command,omitempty"`
	SupportsUnsafe bool                             `json:"supports_unsafe"`
	Liveness       capability.SessionLivenessStatus `json:"liveness"`
	Terminal       terminalStatusResponse           `json:"terminal"`
}

type resumeRequest struct {
	SkipPermissions bool `json:"skip_permissions"`
}

type resumeResponse struct {
	Launched bool                   `json:"launched"`
	Status   string                 `json:"status"`
	Command  string                 `json:"command"`
	Terminal terminalStatusResponse `json:"terminal"`
}

type terminalStatusResponse struct {
	State          string     `json:"state"`
	SessionLive    bool       `json:"session_live"`
	LivenessState  string     `json:"liveness_state"`
	TerminalID     string     `json:"terminal_id,omitempty"`
	TerminalName   string     `json:"terminal_name,omitempty"`
	InstanceID     string     `json:"instance_id,omitempty"`
	WindowID       string     `json:"window_id,omitempty"`
	TabID          string     `json:"tab_id,omitempty"`
	TerminalPID    int        `json:"terminal_pid,omitempty"`
	AgentPID       int        `json:"agent_pid,omitempty"`
	Confidence     string     `json:"confidence"`
	Focusable      bool       `json:"focusable"`
	LaunchedAt     *time.Time `json:"launched_at,omitempty"`
	LastVerifiedAt *time.Time `json:"last_verified_at,omitempty"`
}

type sessionRuntime struct {
	reader reader.BaseSessionReader
	detail *model.SessionDetail
	static capability.AgentCapabilities
	caps   capability.SessionCapabilities
}

func (s *Server) handleGetResumePlan(w http.ResponseWriter, r *http.Request) {
	runtime, err := s.resolveSessionRuntime(r.PathValue("id"), r.URL.Query().Get("agent"))
	if err != nil {
		writeResumeError(w, err)
		return
	}
	unsafe := r.URL.Query().Get("unsafe") == "1"
	command, buildErr := buildResumeCommand(runtime, unsafe)
	status := "ready"
	if buildErr != nil {
		status = resumeErrorCode(buildErr)
	}
	if runtime.caps.Liveness.IsLive {
		status = "session_running"
	}
	terminalStatus := s.resolveTerminalStatus(runtime)
	response := resumePlanResponse{
		Status: status, AgentType: runtime.detail.AgentType, SessionID: runtime.detail.ID,
		CWD: runtime.detail.CWD, SupportsUnsafe: runtime.static.ResumeCommand != nil && runtime.static.ResumeCommand.UnsafeArgs != nil,
		Liveness: runtime.caps.Liveness, Terminal: terminalStatus,
	}
	if buildErr == nil {
		response.Command = terminal.FormatCommand(command)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response) //nolint:errcheck
}

func (s *Server) handleResumeSession(w http.ResponseWriter, r *http.Request) {
	runtime, err := s.resolveSessionRuntime(r.PathValue("id"), r.URL.Query().Get("agent"))
	if err != nil {
		writeResumeError(w, err)
		return
	}
	var request resumeRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
			writeResumeJSONError(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
			return
		}
	}
	switch s.resolveTerminalStatus(runtime).State {
	case "active", "launching":
		writeResumeJSONError(w, http.StatusConflict, "session_running", "the Agent session is already running in a known terminal")
		return
	}
	command, err := buildResumeCommand(runtime, request.SkipPermissions)
	if err != nil {
		writeResumeError(w, err)
		return
	}
	key := runtime.detail.AgentType + "\x00" + runtime.detail.ID
	if !s.beginResume(key) {
		writeResumeJSONError(w, http.StatusConflict, "resume_in_progress", "another Resume launch is already in progress")
		return
	}
	defer s.endResume(key)

	binding, err := s.terminalLauncher.Launch(r.Context(), command)
	if err != nil {
		writeResumeJSONError(w, http.StatusUnprocessableEntity, "terminal_launch_failed", err.Error())
		return
	}
	status := terminalStatusFromBinding(binding, "launching", false, runtime.caps.Liveness.State)
	if s.DB != nil {
		record := bindingRecord(runtime.detail.AgentType, runtime.detail.ID, binding, "launching")
		if err := s.DB.UpsertTerminalBinding(record); err != nil {
			writeResumeJSONError(w, http.StatusInternalServerError, "terminal_binding_save_failed", err.Error())
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resumeResponse{
		Launched: true, Status: "launching", Command: terminal.FormatCommand(command), Terminal: status,
	}) //nolint:errcheck
}

func (s *Server) handleGetSessionTerminal(w http.ResponseWriter, r *http.Request) {
	runtime, err := s.resolveSessionRuntime(r.PathValue("id"), r.URL.Query().Get("agent"))
	if err != nil {
		writeResumeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.resolveTerminalStatus(runtime)) //nolint:errcheck
}

func (s *Server) handleFocusSessionTerminal(w http.ResponseWriter, r *http.Request) {
	runtime, err := s.resolveSessionRuntime(r.PathValue("id"), r.URL.Query().Get("agent"))
	if err != nil {
		writeResumeError(w, err)
		return
	}
	if s.DB == nil {
		writeResumeJSONError(w, http.StatusNotFound, "terminal_binding_missing", "terminal binding is unavailable")
		return
	}
	record, ok, err := s.DB.GetTerminalBinding(runtime.detail.AgentType, runtime.detail.ID)
	if err != nil {
		writeResumeJSONError(w, http.StatusInternalServerError, "terminal_binding_load_failed", err.Error())
		return
	}
	if !ok || !record.Focusable {
		writeResumeJSONError(w, http.StatusConflict, "terminal_not_focusable", "the exact terminal tab is not available")
		return
	}
	result, err := s.terminalLauncher.Focus(r.Context(), terminalBinding(record))
	if err != nil {
		writeResumeJSONError(w, http.StatusUnprocessableEntity, "terminal_focus_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result) //nolint:errcheck
}

func (s *Server) resolveSessionRuntime(id, agentType string) (sessionRuntime, error) {
	if id == "" {
		return sessionRuntime{}, resumeActionError{code: "missing_session_id", status: http.StatusBadRequest}
	}
	for _, source := range s.Readers {
		if agentType != "" && source.AgentType() != agentType {
			continue
		}
		detail, err := source.GetSession(id)
		if err != nil || detail == nil {
			continue
		}
		static, ok := reader.AgentDefinition(detail.AgentType)
		if !ok {
			return sessionRuntime{}, resumeActionError{code: "unknown_agent", status: http.StatusInternalServerError}
		}
		caps, err := reader.ResolveSessionCapabilities(source, detail, static)
		if err != nil {
			return sessionRuntime{}, resumeActionError{code: "capability_resolution_failed", status: http.StatusInternalServerError, detail: err.Error()}
		}
		detail.IsLive = caps.Liveness.IsLive
		return sessionRuntime{reader: source, detail: detail, static: static, caps: caps}, nil
	}
	return sessionRuntime{}, resumeActionError{code: "session_not_found", status: http.StatusNotFound}
}

func buildResumeCommand(runtime sessionRuntime, unsafe bool) (terminal.Command, error) {
	action := runtime.caps.Actions[capability.CapabilityResume]
	if action.Availability != capability.ActionAvailable {
		code := action.ReasonCode
		if code == "" {
			code = "resume_unsupported"
		}
		return terminal.Command{}, resumeActionError{code: code, status: http.StatusConflict}
	}
	if runtime.static.ResumeCommand == nil {
		return terminal.Command{}, resumeActionError{code: "resume_unsupported", status: http.StatusConflict}
	}
	identity := reader.ResumeCLIIdentity(runtime.detail, runtime.static.AgentType)
	args, ok := runtime.static.ResumeCommand.BuildResumeArgs(identity, runtime.detail.ModelName, unsafe)
	if !ok {
		code := "resume_command_unavailable"
		if unsafe {
			code = "unsafe_resume_unsupported"
		}
		return terminal.Command{}, resumeActionError{code: code, status: http.StatusConflict}
	}
	if runtime.detail.CWD == "" || !filepath.IsAbs(runtime.detail.CWD) {
		return terminal.Command{}, resumeActionError{code: "cwd_unavailable", status: http.StatusConflict}
	}
	info, err := os.Stat(runtime.detail.CWD)
	if err != nil || !info.IsDir() {
		return terminal.Command{}, resumeActionError{code: "cwd_unavailable", status: http.StatusConflict, detail: runtime.detail.CWD}
	}
	title := fmt.Sprintf("SI · %s · %s", runtime.static.DisplayName, shortSessionID(identity))
	return terminal.Command{Executable: runtime.static.ResumeCommand.Executable, Args: args, CWD: runtime.detail.CWD, Title: title}, nil
}

func shortSessionID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func (s *Server) resolveTerminalStatus(runtime sessionRuntime) terminalStatusResponse {
	live := runtime.caps.Liveness.IsLive
	quality := string(runtime.caps.Liveness.State)
	if s.DB == nil {
		if status, ok := externalTerminalStatus(runtime); ok {
			return status
		}
		return terminalStatusResponse{State: terminalStateWithoutBinding(live), SessionLive: live, LivenessState: quality, Confidence: terminal.ConfidenceUnknown}
	}
	record, ok, err := s.DB.GetTerminalBinding(runtime.detail.AgentType, runtime.detail.ID)
	if err != nil || !ok {
		if status, ok := externalTerminalStatus(runtime); ok {
			return status
		}
		return terminalStatusResponse{State: terminalStateWithoutBinding(live), SessionLive: live, LivenessState: quality, Confidence: terminal.ConfidenceUnknown}
	}
	// A later externally-started run must not inherit a stale exact tab from a
	// previous SI launch that was already observed stopped.
	if live && record.State == "stopped" {
		if status, ok := externalTerminalStatus(runtime); ok {
			return status
		}
		return terminalStatusResponse{State: "active_unknown", SessionLive: true, LivenessState: quality, Confidence: terminal.ConfidenceUnknown}
	}
	state := "stopped"
	if live {
		state = "active"
	} else if time.Since(record.LaunchedAt) < resumeVerificationGrace && record.State == "launching" {
		state = "launching"
	}
	now := time.Now().UTC()
	stateChanged := record.State != state
	pidChanged := false
	verificationDue := false
	if live {
		if finder, ok := runtime.reader.(reader.SessionProcessFinder); ok {
			if pids, err := finder.SessionProcesses(runtime.detail.ID); err == nil && len(pids) > 0 {
				pidChanged = record.AgentPID != pids[0]
				record.AgentPID = pids[0]
			}
		}
		verificationDue = record.LastVerifiedAt.IsZero() || now.Sub(record.LastVerifiedAt) >= terminalVerificationPersistInterval
		if stateChanged || pidChanged || verificationDue {
			record.LastVerifiedAt = now
		}
	}
	record.State = state
	if stateChanged || pidChanged || verificationDue {
		if err := s.DB.UpsertTerminalBinding(record); err != nil {
			log.Printf("resolve terminal status %s/%s: persist binding: %v", runtime.detail.AgentType, runtime.detail.ID, err)
		}
	}
	status := terminalStatusFromRecord(record, live, quality)
	return status
}

func externalTerminalStatus(runtime sessionRuntime) (terminalStatusResponse, bool) {
	if !runtime.caps.Liveness.IsLive {
		return terminalStatusResponse{}, false
	}
	finder, ok := runtime.reader.(reader.SessionProcessFinder)
	if !ok {
		return terminalStatusResponse{}, false
	}
	pids, err := finder.SessionProcesses(runtime.detail.ID)
	if err != nil || len(pids) == 0 {
		return terminalStatusResponse{}, false
	}
	if binding, ok := terminal.DetectFromAgentPID(pids[0]); ok {
		return terminalStatusFromBinding(binding, "active", true, runtime.caps.Liveness.State), true
	}
	return terminalStatusResponse{
		State: "active_unknown", SessionLive: true, LivenessState: string(runtime.caps.Liveness.State),
		AgentPID: pids[0], Confidence: terminal.ConfidenceUnknown,
	}, true
}

func terminalStateWithoutBinding(live bool) string {
	if live {
		return "active_unknown"
	}
	return "none"
}

func terminalStatusFromBinding(binding terminal.Binding, state string, live bool, liveness capability.CapabilityState) terminalStatusResponse {
	status := terminalStatusResponse{
		State: state, SessionLive: live, LivenessState: string(liveness),
		TerminalID: binding.TerminalID, TerminalName: binding.TerminalName,
		InstanceID: binding.InstanceID, WindowID: binding.WindowID, TabID: binding.TabID,
		TerminalPID: binding.TerminalPID, AgentPID: binding.AgentPID,
		Confidence: binding.Confidence, Focusable: binding.Focusable,
	}
	if !binding.LaunchedAt.IsZero() {
		launched := binding.LaunchedAt
		status.LaunchedAt = &launched
	}
	return status
}

func terminalStatusFromRecord(record db.TerminalBindingRecord, live bool, liveness string) terminalStatusResponse {
	launched := record.LaunchedAt
	var verified *time.Time
	if !record.LastVerifiedAt.IsZero() {
		value := record.LastVerifiedAt
		verified = &value
	}
	return terminalStatusResponse{
		State: record.State, SessionLive: live, LivenessState: liveness,
		TerminalID: record.TerminalID, TerminalName: record.TerminalName,
		InstanceID: record.InstanceID, WindowID: record.WindowID, TabID: record.TabID,
		TerminalPID: record.TerminalPID, AgentPID: record.AgentPID,
		Confidence: record.Confidence, Focusable: record.Focusable,
		LaunchedAt: &launched, LastVerifiedAt: verified,
	}
}

func bindingRecord(agentType, sessionID string, binding terminal.Binding, state string) db.TerminalBindingRecord {
	return db.TerminalBindingRecord{
		AgentType: agentType, SessionID: sessionID,
		TerminalID: binding.TerminalID, TerminalName: binding.TerminalName,
		InstanceID: binding.InstanceID, WindowID: binding.WindowID, TabID: binding.TabID,
		TerminalPID: binding.TerminalPID, AgentPID: binding.AgentPID,
		Confidence: binding.Confidence, Focusable: binding.Focusable,
		State: state, LaunchedAt: binding.LaunchedAt,
	}
}

func terminalBinding(record db.TerminalBindingRecord) terminal.Binding {
	return terminal.Binding{
		TerminalID: record.TerminalID, TerminalName: record.TerminalName,
		InstanceID: record.InstanceID, WindowID: record.WindowID, TabID: record.TabID,
		TerminalPID: record.TerminalPID, AgentPID: record.AgentPID,
		Confidence: record.Confidence, Focusable: record.Focusable, LaunchedAt: record.LaunchedAt,
	}
}

func (s *Server) beginResume(key string) bool {
	s.resumeMu.Lock()
	defer s.resumeMu.Unlock()
	if s.resumeInFlight[key] {
		return false
	}
	s.resumeInFlight[key] = true
	return true
}

func (s *Server) endResume(key string) {
	s.resumeMu.Lock()
	delete(s.resumeInFlight, key)
	s.resumeMu.Unlock()
}

type resumeActionError struct {
	code   string
	status int
	detail string
}

func (e resumeActionError) Error() string { return e.code }

func resumeErrorCode(err error) string {
	var actionErr resumeActionError
	if errors.As(err, &actionErr) {
		return actionErr.code
	}
	return "resume_failed"
}

func writeResumeError(w http.ResponseWriter, err error) {
	var actionErr resumeActionError
	if errors.As(err, &actionErr) {
		writeResumeJSONError(w, actionErr.status, actionErr.code, actionErr.detail)
		return
	}
	writeResumeJSONError(w, http.StatusInternalServerError, "resume_failed", err.Error())
}

func writeResumeJSONError(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"code": code, "detail": strings.TrimSpace(detail)}) //nolint:errcheck
}
