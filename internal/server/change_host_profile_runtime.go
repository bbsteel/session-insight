package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/bbsteel/session-insight/internal/changehost"
	"github.com/bbsteel/session-insight/internal/changehost/openapi"
	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
)

// change_host_profile_runtime.go: activation and runtime wiring for OpenAPI
// host profiles (design §11). Activating a profile atomically refreshes the
// registry's host-bound parser set; querying routes by reference host_id to
// the matching host and its active profile revision.

// refreshOpenAPIHostParsers rebuilds the registry's host-bound parser set
// from the currently active profiles. It is atomic: a failure leaves the
// previous set untouched.
func (s *Server) refreshOpenAPIHostParsers() error {
	if s.DB == nil {
		return nil
	}
	hosts, err := s.DB.ListChangeHosts()
	if err != nil {
		return err
	}
	parsers := []changehost.RegisteredReferenceParser{}
	for _, host := range hosts {
		if host.Provider != model.ChangeProviderOpenAPI || host.Lifecycle != "approved" {
			continue
		}
		profile, exists, err := s.DB.ActiveChangeHostProfile(host.HostID)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		decoded, err := openapi.DecodeProfile([]byte(profile.ProfileJSON))
		if err != nil {
			return fmt.Errorf("decode active profile %s: %w", profile.ProfileID, err)
		}
		parsers = append(parsers, changehost.RegisteredReferenceParser{
			ID:     host.HostID + "/" + profile.ProfileID,
			HostID: host.HostID,
			Parser: changehost.OpenAPIProfileParser{Profile: decoded},
		})
	}
	return s.changeRegistry.ReplaceHostParsers(parsers)
}

// restoreOpenAPIHostParsers runs at startup so active profiles survive a
// process restart (design §16.4). A failure is logged by the caller; parsing
// falls back to built-ins only.
func (s *Server) restoreOpenAPIHostParsers() error {
	return s.refreshOpenAPIHostParsers()
}

func (s *Server) openapiProviderForHost(ctx context.Context, hostRecord db.ChangeHostRecord, profileRecord db.ChangeHostProfileRecord) (*changehost.OpenAPIProvider, *changehost.ApprovedHost, error) {
	profile, err := openapi.DecodeProfile([]byte(profileRecord.ProfileJSON))
	if err != nil {
		return nil, nil, fmt.Errorf("decode active profile: %w", err)
	}
	approved, err := s.approvedHost(ctx, hostRecord)
	if err != nil {
		return nil, nil, err
	}
	reference, ok := model.ParseCredentialReference(profile.Authentication.CredentialReference)
	if !ok {
		return nil, nil, model.ErrInvalidCredentialReference
	}
	source, err := s.credentialSourceFor(reference)
	if err != nil {
		return nil, nil, err
	}
	client, err := changehost.NewAuthenticatedHTTPClient(approved, changehost.HTTPClientConfig{}, changehost.ResolvedAuthentication{
		Source:    source,
		Reference: reference,
		Scheme: changehost.AuthenticationScheme{
			HeaderName:  profile.Authentication.HeaderName,
			ValuePrefix: profile.Authentication.ValuePrefix,
		},
	})
	if err != nil {
		return nil, nil, err
	}
	provider, err := changehost.NewOpenAPIProvider(approved.Identity(), client, profile, func(code string) {
		if err := s.DB.MarkChangeHostProfileDegraded(profileRecord.ProfileID, code, time.Now().UTC()); err != nil {
			// Degradation marking is best-effort; the structured provider
			// error already protects the current operation.
			_ = err
		}
	})
	if err != nil {
		return nil, nil, err
	}
	return provider, approved, nil
}

func (s *Server) credentialSourceFor(reference model.CredentialReference) (changehost.CredentialSource, error) {
	switch reference.Scheme() {
	case model.CredentialSchemeEnvironment:
		return changehost.EnvironmentCredentialSource{}, nil
	default:
		return nil, fmt.Errorf("%w: keyring credential resolution is not available in this build", changehost.ErrCredentialUnavailable)
	}
}

// --- activate / test ---------------------------------------------------------

// handleActivateChangeHostProfile promotes one verified profile to active.
// The registry refresh is part of the same request: after this call returns
// 200, URLs of the platform parse against the new active revision.
func (s *Server) handleActivateChangeHostProfile(w http.ResponseWriter, r *http.Request) {
	record, ok := s.loadChangeHostProfile(w, r.PathValue("profileId"))
	if !ok {
		return
	}
	if record.Lifecycle != openapi.ProfileVerified {
		writeAPIError(w, http.StatusConflict, "change_profile_conflict")
		return
	}
	hostRecord, exists, err := s.DB.ChangeHost(record.HostID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	if !exists || hostRecord.Lifecycle != "approved" {
		writeAPIError(w, http.StatusForbidden, "change_host_not_approved")
		return
	}
	if err := s.DB.ActivateChangeHostProfile(record.ProfileID, time.Now().UTC()); err != nil {
		if errors.Is(err, db.ErrChangeHostProfileConflict) {
			writeAPIError(w, http.StatusConflict, "change_profile_conflict")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	if err := s.refreshOpenAPIHostParsers(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	updated, _, _ := s.DB.ChangeHostProfile(record.ProfileID)
	writeJSONStatus(w, http.StatusOK, profileDTOFromRecord(updated))
}

// handleTestChangeHostProfile runs a read-only resolution of the profile's
// sample URL through the active mapping — no snapshot is persisted.
func (s *Server) handleTestChangeHostProfile(w http.ResponseWriter, r *http.Request) {
	record, ok := s.loadChangeHostProfile(w, r.PathValue("profileId"))
	if !ok {
		return
	}
	if record.Lifecycle != openapi.ProfileActive && record.Lifecycle != openapi.ProfileVerified {
		writeAPIError(w, http.StatusConflict, "change_profile_conflict")
		return
	}
	var request struct {
		Reference string `json:"reference"`
	}
	if !decodeBoundedJSON(w, r, &request) {
		return
	}
	hostRecord, exists, err := s.DB.ChangeHost(record.HostID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	if !exists || hostRecord.Lifecycle != "approved" {
		writeAPIError(w, http.StatusForbidden, "change_host_not_approved")
		return
	}
	profile, err := openapi.DecodeProfile([]byte(record.ProfileJSON))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	reference, ok := openapi.MatchReferenceTemplate(profile.Reference, profile.HostID, request.Reference)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, string(openapi.IssueReferenceAmbiguous), "reference does not match this profile")
		return
	}
	provider, _, err := s.openapiProviderForHost(r.Context(), hostRecord, record)
	if err != nil {
		writeChangeHostError(w, err)
		return
	}
	resolved, err := provider.Resolve(r.Context(), reference)
	if err != nil {
		writeChangeHostError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{
		"summary":      resolved.Change,
		"capabilities": provider.Capabilities(),
	})
}
