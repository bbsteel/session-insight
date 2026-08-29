package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/bbsteel/session-insight/internal/changehost"
	"github.com/bbsteel/session-insight/internal/changehost/openapi"
	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
)

type changeHostPreviewRequest struct {
	Reference string `json:"reference"`
}

type changeHostApprovalRequest struct {
	AllowHTTP           bool `json:"allow_http"`
	AllowPrivateNetwork bool `json:"allow_private_network"`
}

type changeHostRefreshRequest struct {
	Reference string `json:"reference"`
}

func (s *Server) handlePreviewChangeHost(w http.ResponseWriter, r *http.Request) {
	if s.DB == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	var request changeHostPreviewRequest
	if !decodeBoundedJSON(w, r, &request) {
		return
	}
	reference, err := s.changeRegistry.ResolveReference(request.Reference)
	if err != nil || reference.Provider == model.ChangeProviderGeneric {
		writeAPIError(w, http.StatusBadRequest, "change_provider_unsupported")
		return
	}
	host, ok := changehost.PublicHost(reference.Provider)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "change_provider_unsupported")
		return
	}
	preview, err := s.hostPolicy.Preview(host)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_change_host")
		return
	}
	if err := s.DB.StoreChangeHostPreview(db.ChangeHostRecord{
		HostID: preview.Host.Key, Provider: preview.Host.Provider,
		DisplayOrigin:   preview.Host.DisplayOrigin,
		EndpointOrigins: preview.Host.EndpointOrigins,
	}); err != nil {
		// Re-preview of an already approved host is a status read, not an
		// attempt to weaken or replace its authority.
		stored, exists, readErr := s.DB.ChangeHost(preview.Host.Key)
		if readErr != nil || !exists || stored.Lifecycle == "revoked" {
			writeAPIError(w, http.StatusConflict, "change_host_conflict")
			return
		}
	}
	writeJSONStatus(w, http.StatusOK, preview)
}

func (s *Server) handleApproveChangeHost(w http.ResponseWriter, r *http.Request) {
	if s.DB == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	var request changeHostApprovalRequest
	if !decodeBoundedJSON(w, r, &request) {
		return
	}
	hostKey := r.PathValue("hostKey")
	record, exists, err := s.DB.ChangeHost(hostKey)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	if !exists {
		writeAPIError(w, http.StatusNotFound, "change_host_not_found")
		return
	}
	if record.Lifecycle != "preview" {
		writeAPIError(w, http.StatusConflict, "change_host_conflict")
		return
	}
	approved, err := s.hostPolicy.Approve(r.Context(), hostIdentityFromRecord(record), changehost.HostApprovalOptions{
		AllowHTTP: request.AllowHTTP, AllowPrivateNetwork: request.AllowPrivateNetwork,
	})
	if err != nil {
		writeHostPolicyError(w, err)
		return
	}
	approvedAt := time.Now().UTC()
	if err := s.DB.ApproveChangeHost(hostKey, request.AllowHTTP, request.AllowPrivateNetwork, approvedAt); err != nil {
		approved.Revoke()
		writeAPIError(w, http.StatusConflict, "change_host_conflict")
		return
	}
	s.hostMu.Lock()
	s.approvedHosts[hostKey] = approved
	s.hostMu.Unlock()
	status, err := s.changeHostStatus(hostKey)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSONStatus(w, http.StatusOK, status)
}

func (s *Server) handleRevokeChangeHost(w http.ResponseWriter, r *http.Request) {
	if s.DB == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	hostKey := r.PathValue("hostKey")
	revoked, err := s.DB.RevokeChangeHost(hostKey, time.Now().UTC())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	if !revoked {
		writeAPIError(w, http.StatusNotFound, "change_host_not_found")
		return
	}
	s.hostMu.Lock()
	approved := s.approvedHosts[hostKey]
	delete(s.approvedHosts, hostKey)
	s.hostMu.Unlock()
	if approved != nil {
		approved.Revoke()
	}
	if err := s.refreshOpenAPIHostParsers(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListChangeHosts(w http.ResponseWriter, _ *http.Request) {
	if s.DB == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	records, err := s.DB.ListChangeHosts()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	statuses := make([]changehost.HostStatus, 0, len(records))
	for _, record := range records {
		statuses = append(statuses, s.changeHostStatusFromRecord(record))
	}
	writeJSONStatus(w, http.StatusOK, changehost.HostListResponse{Hosts: statuses})
}

func (s *Server) handleGetChangeHostStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.changeHostStatus(r.PathValue("hostKey"))
	if errors.Is(err, db.ErrChangeRequestNotFound) {
		writeAPIError(w, http.StatusNotFound, "change_host_not_found")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSONStatus(w, http.StatusOK, status)
}

func (s *Server) handleRefreshChangeHost(w http.ResponseWriter, r *http.Request) {
	if s.DB == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	var request changeHostRefreshRequest
	if !decodeBoundedJSON(w, r, &request) {
		return
	}
	reference, err := s.changeRegistry.ResolveReference(request.Reference)
	if err != nil || reference.Provider == model.ChangeProviderGeneric {
		writeAPIError(w, http.StatusBadRequest, "change_alias_ambiguous")
		return
	}
	// OpenAPI references route by host_id; built-ins keep their fixed host.
	hostKey := r.PathValue("hostKey")
	if reference.Provider == model.ChangeProviderOpenAPI {
		if reference.HostID == "" || reference.HostID != hostKey {
			writeAPIError(w, http.StatusBadRequest, "change_host_mismatch")
			return
		}
		lookup, err := s.refreshOpenAPIChangeRequest(r.Context(), hostKey, reference)
		if err != nil {
			writeChangeHostError(w, err)
			return
		}
		writeJSONStatus(w, http.StatusOK, lookup)
		return
	}
	host, ok := changehost.PublicHost(reference.Provider)
	if !ok || host.Key != hostKey {
		writeAPIError(w, http.StatusBadRequest, "change_host_mismatch")
		return
	}
	lookup, err := s.refreshChangeRequest(r.Context(), host.Key, reference)
	if err != nil {
		writeChangeHostError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, lookup)
}

// refreshOpenAPIChangeRequest resolves and syncs one change through the
// host's active OpenAPI profile revision.
func (s *Server) refreshOpenAPIChangeRequest(ctx context.Context, hostKey string, reference model.ChangeRequestReference) (changeRequestLookup, error) {
	hostRecord, exists, err := s.DB.ChangeHost(hostKey)
	if err != nil {
		return changeRequestLookup{}, err
	}
	if !exists || hostRecord.Lifecycle != "approved" || hostRecord.Provider != model.ChangeProviderOpenAPI {
		return changeRequestLookup{}, &changehost.Error{
			Code: changehost.ErrorHostNotApproved, Operation: changehost.OperationResolveChange,
		}
	}
	profileRecord, exists, err := s.DB.ActiveChangeHostProfile(hostKey)
	if err != nil {
		return changeRequestLookup{}, err
	}
	if !exists {
		return changeRequestLookup{}, &changehost.Error{
			Code: changehost.ErrorHostNotApproved, Operation: changehost.OperationResolveChange,
		}
	}
	provider, _, err := s.openapiProviderForHost(ctx, hostRecord, profileRecord)
	if err != nil {
		return changeRequestLookup{}, err
	}
	resolved, err := provider.Resolve(ctx, reference)
	if err != nil {
		s.recordChangeHostFailure(hostKey, err)
		return changeRequestLookup{}, err
	}
	synced, err := changehost.SyncSnapshot(
		ctx, provider, s.DB, resolved.Change.Identity, "",
		changehost.SnapshotSyncOptions{Quota: db.DefaultSourceContentQuota},
	)
	if err != nil {
		s.recordChangeHostFailure(hostKey, err)
		return changeRequestLookup{}, err
	}
	_ = s.DB.TouchChangeHost(hostKey, time.Now().UTC(), model.ExactGitEvidence())
	_ = s.DB.TouchChangeHostProfileSuccess(profileRecord.ProfileID, time.Now().UTC())
	return s.changeRequestLookup(synced.ChangeKey)
}

func (s *Server) refreshChangeRequest(ctx context.Context, hostKey string, reference model.ChangeRequestReference) (changeRequestLookup, error) {
	record, exists, err := s.DB.ChangeHost(hostKey)
	if err != nil {
		return changeRequestLookup{}, err
	}
	if !exists || record.Lifecycle != "approved" {
		return changeRequestLookup{}, &changehost.Error{
			Code: changehost.ErrorHostNotApproved, Operation: changehost.OperationResolveChange,
		}
	}
	approved, err := s.approvedHost(ctx, record)
	if err != nil {
		return changeRequestLookup{}, err
	}
	client, err := changehost.NewHTTPClient(approved, changehost.HTTPClientConfig{}, nil)
	if err != nil {
		return changeRequestLookup{}, err
	}
	provider, err := s.changeRegistry.NewProvider(record.Provider, approved.Identity(), client)
	if err != nil {
		return changeRequestLookup{}, err
	}
	resolved, err := provider.Resolve(ctx, reference)
	if err != nil {
		s.recordChangeHostFailure(hostKey, err)
		return changeRequestLookup{}, err
	}
	synced, err := changehost.SyncSnapshot(
		ctx, provider, s.DB, resolved.Change.Identity, "",
		changehost.SnapshotSyncOptions{Quota: db.DefaultSourceContentQuota},
	)
	if err != nil {
		s.recordChangeHostFailure(hostKey, err)
		return changeRequestLookup{}, err
	}
	_ = s.DB.TouchChangeHost(hostKey, time.Now().UTC(), model.ExactGitEvidence())
	return s.changeRequestLookup(synced.ChangeKey)
}

func (s *Server) approvedHost(ctx context.Context, record db.ChangeHostRecord) (*changehost.ApprovedHost, error) {
	s.hostMu.Lock()
	defer s.hostMu.Unlock()
	if approved := s.approvedHosts[record.HostID]; approved != nil {
		return approved, nil
	}
	approved, err := s.hostPolicy.Approve(ctx, hostIdentityFromRecord(record), changehost.HostApprovalOptions{
		AllowHTTP: record.AllowHTTP, AllowPrivateNetwork: record.AllowPrivateNetwork,
	})
	if err != nil {
		return nil, err
	}
	s.approvedHosts[record.HostID] = approved
	return approved, nil
}

func (s *Server) recordChangeHostFailure(hostKey string, cause error) {
	assessment := changeHostFailureAssessment(cause)
	if err := s.DB.TouchChangeHost(hostKey, time.Now().UTC(), assessment); err != nil {
		log.Printf("update Change Request host failure status: %v", err)
	}
}

func changeHostFailureAssessment(cause error) model.GitEvidenceAssessment {
	reason := model.ReasonChangeRequestPartial
	var providerError *changehost.Error
	if errors.As(cause, &providerError) {
		reason = providerError.EvidenceReason()
	}
	var policyError *changehost.HostPolicyError
	if errors.As(cause, &policyError) {
		switch policyError.Code {
		case changehost.HostPolicyApprovalRevoked:
			reason = model.ReasonChangeHostRevoked
		default:
			reason = model.ReasonChangeRequestPartial
		}
	}
	return model.NonExactGitEvidence(model.GitEvidenceUnavailable, reason)
}

func (s *Server) changeHostStatus(hostKey string) (changehost.HostStatus, error) {
	if s.DB == nil {
		return changehost.HostStatus{}, errors.New("database unavailable")
	}
	record, exists, err := s.DB.ChangeHost(hostKey)
	if err != nil {
		return changehost.HostStatus{}, err
	}
	if !exists {
		return changehost.HostStatus{}, db.ErrChangeRequestNotFound
	}
	return s.changeHostStatusFromRecord(record), nil
}

func (s *Server) changeHostCapabilities(record db.ChangeHostRecord) changehost.ProviderCapabilities {
	if record.Provider == model.ChangeProviderOpenAPI && s.DB != nil {
		if profile, exists, err := s.DB.ActiveChangeHostProfile(record.HostID); err == nil && exists {
			if decoded, err := openapi.DecodeProfile([]byte(profile.ProfileJSON)); err == nil {
				return changehost.OpenAPIProfileCapabilities(decoded)
			}
		}
	}
	capabilities, _ := changehost.BuiltInProviderCapabilities(record.Provider)
	return capabilities
}

func (s *Server) changeHostStatusFromRecord(record db.ChangeHostRecord) changehost.HostStatus {
	state := changehost.HostPendingApproval
	switch record.Lifecycle {
	case "approved":
		state = changehost.HostApproved
	case "revoked":
		state = changehost.HostRevoked
	}
	status := changehost.HostStatus{
		Host: hostIdentityFromRecord(record), ApprovalState: state,
		Capabilities: s.changeHostCapabilities(record),
		Assessment:   record.Assessment, LastCheckedAt: record.LastCheckedAt,
	}
	// The credential reference itself never leaves storage; the DTO carries
	// only whether a credential is configured and which store mode serves it.
	if reference, ok := model.ParseCredentialReference(record.CredentialReference); ok {
		status.AuthenticationConfigured = true
		if mode, known := changehost.AuthenticationModeForReference(reference); known {
			status.AuthenticationMode = &mode
		}
	}
	return status
}

func hostIdentityFromRecord(record db.ChangeHostRecord) changehost.HostIdentity {
	return changehost.HostIdentity{
		Key: record.HostID, Provider: record.Provider,
		DisplayOrigin:   record.DisplayOrigin,
		EndpointOrigins: append([]string(nil), record.EndpointOrigins...),
	}
}

func writeHostPolicyError(w http.ResponseWriter, err error) {
	var policyError *changehost.HostPolicyError
	if !errors.As(err, &policyError) {
		writeAPIError(w, http.StatusBadGateway, "change_host_unavailable")
		return
	}
	switch policyError.Code {
	case changehost.HostPolicyHTTPNotApproved, changehost.HostPolicyPrivateNotApproved:
		writeAPIError(w, http.StatusForbidden, string(policyError.Code))
	case changehost.HostPolicyResolutionFailed:
		writeAPIError(w, http.StatusBadGateway, "change_host_unavailable")
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_change_host")
	}
}

func writeChangeHostError(w http.ResponseWriter, err error) {
	var providerError *changehost.Error
	if errors.As(err, &providerError) {
		status := http.StatusBadGateway
		switch providerError.Code {
		case changehost.ErrorHostNotApproved, changehost.ErrorHostRevoked:
			status = http.StatusForbidden
		case changehost.ErrorAuthRequired:
			status = http.StatusUnauthorized
		case changehost.ErrorNotFound:
			status = http.StatusNotFound
		case changehost.ErrorRateLimited:
			status = http.StatusTooManyRequests
			if providerError.RetryAfter > 0 {
				seconds := int64(providerError.RetryAfter.Round(time.Second) / time.Second)
				if seconds < 1 {
					seconds = 1
				}
				w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
			}
		case changehost.ErrorCaptureRaced:
			status = http.StatusConflict
		}
		writeAPIError(w, status, string(providerError.EvidenceReason()))
		return
	}
	writeHostPolicyError(w, err)
}
