package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/changehost"
	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
)

const (
	maximumChangeRequestBody = 16 << 10
	maximumGitPatchReadBytes = 1 << 20
)

type changeRequestResolveRequest struct {
	Reference string `json:"reference"`
}

type changeRequestLookup struct {
	Change            db.ChangeRequestRecord         `json:"change"`
	LinkedSessions    []db.ChangeRequestSessionMatch `json:"linked_sessions"`
	CandidateSessions []db.ChangeRequestSessionMatch `json:"candidate_sessions"`
}

type changeRequestResolveResponse struct {
	Reference  model.ChangeRequestReference `json:"reference"`
	Matches    []changeRequestLookup        `json:"matches"`
	Assessment model.GitEvidenceAssessment  `json:"assessment"`
}

type changeRequestSessionsResponse struct {
	ChangeKey         string                         `json:"change_key"`
	LinkedSessions    []db.ChangeRequestSessionMatch `json:"linked_sessions"`
	CandidateSessions []db.ChangeRequestSessionMatch `json:"candidate_sessions"`
}

func (s *Server) handleGetGitEvidence(w http.ResponseWriter, r *http.Request) {
	agentType, sessionID, ok := s.requireIndexedSession(w, r)
	if !ok {
		return
	}
	envelope, exists, err := s.DB.SessionGitEvidenceEnvelope(agentType, sessionID)
	if err != nil {
		log.Printf("GET Git evidence %s/%s: %v", agentType, sessionID, err)
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	if !exists {
		session, _, err := s.DB.GetSessionRow(agentType, sessionID)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal")
			return
		}
		generatedAt := session.UpdatedAt
		if generatedAt.IsZero() {
			generatedAt = time.Now().UTC()
		}
		envelope = model.SessionGitEvidenceEnvelope{
			RootAgentType: agentType, RootSessionID: sessionID, Revision: 1,
			Assessment:  model.NonExactGitEvidence(model.GitEvidenceMissing, model.ReasonBaselineNotCaptured),
			GeneratedAt: generatedAt, Repositories: []model.SessionGitEvidence{},
		}
	}
	etag := fmt.Sprintf(`"git-evidence-%d-%d"`, s.startNano, envelope.Revision)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSONStatus(w, http.StatusOK, envelope)
}

func (s *Server) handleGetGitEvidencePatch(w http.ResponseWriter, r *http.Request) {
	agentType, sessionID, ok := s.requireIndexedSession(w, r)
	if !ok {
		return
	}
	repositoryEntryKey := strings.TrimSpace(r.URL.Query().Get("repository"))
	fileKey := r.PathValue("fileKey")
	if repositoryEntryKey == "" || fileKey == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	patch, err := s.DB.SessionGitEvidencePatch(
		agentType, sessionID, repositoryEntryKey, fileKey, maximumGitPatchReadBytes,
	)
	if errors.Is(err, db.ErrSourceContentNotFound) {
		writeAPIError(w, http.StatusNotFound, "git_patch_not_found")
		return
	}
	if errors.Is(err, db.ErrSourceContentReadLimit) {
		writeAPIError(w, http.StatusRequestEntityTooLarge, "git_patch_too_large")
		return
	}
	if err != nil {
		log.Printf("GET Git patch %s/%s: %v", agentType, sessionID, err)
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	w.Header().Set("Content-Type", "text/x-diff; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(patch)
}

func (s *Server) handleGetSessionChangeRequests(w http.ResponseWriter, r *http.Request) {
	agentType, sessionID, ok := s.requireIndexedSession(w, r)
	if !ok {
		return
	}
	links, err := s.DB.SessionChangeRequestLinks(agentType, sessionID)
	if err != nil {
		log.Printf("GET Session Change Requests %s/%s: %v", agentType, sessionID, err)
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSONStatus(w, http.StatusOK, struct {
		Links []model.SessionChangeRequestLink `json:"links"`
	}{Links: links})
}

func (s *Server) handleBindSessionChangeRequest(w http.ResponseWriter, r *http.Request) {
	agentType, sessionID, ok := s.requireIndexedSession(w, r)
	if !ok {
		return
	}
	var request model.ChangeRequestBindRequest
	if !decodeBoundedJSON(w, r, &request) {
		return
	}
	if validation := model.ValidateChangeRequestBindRequest(request); !validation.OK() {
		writeAPIError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	storedCollaboration, err := s.DB.GetCollaboration(agentType, sessionID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	if storedCollaboration == nil || storedCollaboration.GraphStatus != db.CollaborationGraphOK {
		writeAPIError(w, http.StatusConflict, "collaboration_not_indexed")
		return
	}
	record, err := s.DB.ChangeRequest(request.ChangeKey, request.ContentVersionKey)
	if errors.Is(err, db.ErrChangeRequestNotFound) {
		writeAPIError(w, http.StatusNotFound, "change_request_not_found")
		return
	}
	if err != nil {
		log.Printf("resolve bind Change Request %s: %v", request.ChangeKey, err)
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	linkID, err := newChangeRequestLinkID()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	link := model.SessionChangeRequestLink{
		LinkID: linkID, RootAgentType: agentType, RootSessionID: sessionID,
		SourceAgentType: agentType, SourceSessionID: sessionID,
		CollaborationRevision: storedCollaboration.Graph.Revision,
		RepositoryEntryKey:    request.RepositoryEntryKey,
		Change:                record.Identity, ContentVersionKey: request.ContentVersionKey,
		Relationship: request.Relationship, Method: model.ChangeLinkExplicit,
		Assessment: model.ExactGitEvidence(), ConfirmationSource: model.ChangeConfirmationNone,
		Evidence: []model.GitEvidenceLink{},
	}
	if request.Relationship == model.ChangeRelationshipExclusive {
		link.ConfirmationSource = model.ChangeConfirmationUser
		link.ConfirmationRevision, err = db.CanonicalChangeRequestConfirmationRevision(link)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request")
			return
		}
	}
	stored, err := s.DB.StoreSessionChangeRequestLink(link)
	if err != nil {
		log.Printf("bind Session Change Request %s/%s: %v", agentType, sessionID, err)
		writeAPIError(w, http.StatusConflict, "change_request_bind_conflict")
		return
	}
	writeJSONStatus(w, http.StatusCreated, stored)
}

func (s *Server) handleDeleteSessionChangeRequest(w http.ResponseWriter, r *http.Request) {
	agentType, sessionID, ok := s.requireIndexedSession(w, r)
	if !ok {
		return
	}
	linkID := r.PathValue("linkID")
	if linkID == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	deleted, err := s.DB.DeleteSessionChangeRequestLink(agentType, sessionID, linkID)
	if err != nil {
		log.Printf("delete Session Change Request %s/%s: %v", agentType, sessionID, err)
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	if !deleted {
		writeAPIError(w, http.StatusNotFound, "change_request_link_not_found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleResolveChangeRequest(w http.ResponseWriter, r *http.Request) {
	if s.DB == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	var request changeRequestResolveRequest
	if !decodeBoundedJSON(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.Reference) == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	reference, err := s.changeRegistry.ResolveReference(request.Reference)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "change_alias_ambiguous")
		return
	}
	changeKeys := []string{}
	if reference.Provider == model.ChangeProviderGeneric {
		identity, err := changehost.GenericIdentity(reference)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "change_alias_ambiguous")
			return
		}
		changeKey, err := s.DB.StoreGenericChangeRequest(reference, identity)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal")
			return
		}
		changeKeys = append(changeKeys, changeKey)
	} else {
		changeKeys, err = s.DB.FindChangeRequestsByURL(reference.NormalizedURL)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal")
			return
		}
	}
	response := changeRequestResolveResponse{
		Reference: reference, Matches: []changeRequestLookup{},
		Assessment: model.NonExactGitEvidence(model.GitEvidenceMissing, model.ReasonChangeRequestNotFound),
	}
	if len(changeKeys) == 0 && reference.Provider != model.ChangeProviderGeneric {
		if host, ok := changehost.PublicHost(reference.Provider); ok {
			record, exists, readErr := s.DB.ChangeHost(host.Key)
			switch {
			case readErr != nil:
				writeAPIError(w, http.StatusInternalServerError, "internal")
				return
			case !exists || record.Lifecycle == "preview":
				response.Assessment = model.NonExactGitEvidence(model.GitEvidenceMissing, model.ReasonChangeHostNotApproved)
			case record.Lifecycle == "revoked":
				response.Assessment = model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonChangeHostRevoked)
			case record.Lifecycle == "approved":
				lookup, refreshErr := s.refreshChangeRequest(r.Context(), host.Key, reference)
				if refreshErr == nil {
					response.Matches = append(response.Matches, lookup)
					response.Assessment = model.ExactGitEvidence()
					writeJSONStatus(w, http.StatusOK, response)
					return
				}
				response.Assessment = changeHostFailureAssessment(refreshErr)
			}
		}
	}
	for _, changeKey := range changeKeys {
		lookup, err := s.changeRequestLookup(changeKey)
		if err != nil {
			log.Printf("resolve cached Change Request %s: %v", changeKey, err)
			writeAPIError(w, http.StatusInternalServerError, "internal")
			return
		}
		response.Matches = append(response.Matches, lookup)
	}
	if len(response.Matches) > 0 {
		response.Assessment = model.ExactGitEvidence()
	}
	writeJSONStatus(w, http.StatusOK, response)
}

func (s *Server) handleGetChangeRequest(w http.ResponseWriter, r *http.Request) {
	if s.DB == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	record, err := s.DB.ChangeRequest(r.PathValue("changeID"), model.ContentVersionKey(r.URL.Query().Get("version")))
	if errors.Is(err, db.ErrChangeRequestNotFound) {
		writeAPIError(w, http.StatusNotFound, "change_request_not_found")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSONStatus(w, http.StatusOK, record)
}

func (s *Server) handleGetChangeRequestSessions(w http.ResponseWriter, r *http.Request) {
	if s.DB == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	changeKey := r.PathValue("changeID")
	if _, err := s.DB.ChangeRequest(changeKey, ""); errors.Is(err, db.ErrChangeRequestNotFound) {
		writeAPIError(w, http.StatusNotFound, "change_request_not_found")
		return
	} else if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	linked, err := s.DB.ChangeRequestSessions(changeKey)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	candidates, err := s.DB.ChangeRequestCandidateSessions(changeKey, 100)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSONStatus(w, http.StatusOK, changeRequestSessionsResponse{
		ChangeKey: changeKey, LinkedSessions: linked, CandidateSessions: candidates,
	})
}

func (s *Server) changeRequestLookup(changeKey string) (changeRequestLookup, error) {
	record, err := s.DB.ChangeRequest(changeKey, "")
	if err != nil {
		return changeRequestLookup{}, err
	}
	linked, err := s.DB.ChangeRequestSessions(changeKey)
	if err != nil {
		return changeRequestLookup{}, err
	}
	candidates, err := s.DB.ChangeRequestCandidateSessions(changeKey, 100)
	if err != nil {
		return changeRequestLookup{}, err
	}
	return changeRequestLookup{Change: record, LinkedSessions: linked, CandidateSessions: candidates}, nil
}

func (s *Server) requireIndexedSession(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		writeAPIError(w, http.StatusBadRequest, "missing_session_id")
		return "", "", false
	}
	agentType := strings.TrimSpace(r.URL.Query().Get("agent"))
	if agentType == "" {
		writeAPIError(w, http.StatusBadRequest, "missing_agent")
		return "", "", false
	}
	if s.DB == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return "", "", false
	}
	indexed, err := s.DB.SessionIndexed(agentType, sessionID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return "", "", false
	}
	if !indexed {
		writeAPIError(w, http.StatusNotFound, "session_not_found")
		return "", "", false
	}
	return agentType, sessionID, true
}

func decodeBoundedJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maximumChangeRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeAPIError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}

func newChangeRequestLinkID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "link-" + hex.EncodeToString(random[:]), nil
}

func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
