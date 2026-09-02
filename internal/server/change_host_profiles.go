package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/changehost"
	"github.com/bbsteel/session-insight/internal/changehost/openapi"
	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
)

// change_host_profiles.go: import / probe / mapping lifecycle for OpenAPI
// host profiles (design §12). The OpenAPI document itself is never persisted;
// the database stores only the digest, the profile, and a sanitized inference
// report. Tokens never enter the request/response DTOs, the profile JSON, or
// the database.

const maxOpenAPIImportBytes = 5 << 20

// storedProbePlan is the sanitized, structural part of the import-time
// candidate analysis persisted in inference_report_json so the probe endpoint
// can run without re-uploading the document. Descriptions, examples, and any
// free-text from the document are deliberately excluded.
type storedProbePlan struct {
	SampleNumber         string                 `json:"sample_number"`
	SampleRepositorySlug string                 `json:"sample_repository_slug"`
	SamplePathSegments   []string               `json:"sample_path_segments"`
	Candidates           []storedProbeCandidate `json:"candidates"`
}

type storedProbeCandidate struct {
	Role     string            `json:"role"`
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	BaseURL  string            `json:"base_url"`
	Score    float64           `json:"score"`
	Bindings map[string]string `json:"bindings"`
}

// changeHostProfileDTO is the credential-safe profile view. The credential
// reference name and any secret material are never projected.
type changeHostProfileDTO struct {
	ProfileID                string                         `json:"profile_id"`
	HostID                   string                         `json:"host_id"`
	ProfileRevision          int                            `json:"profile_revision"`
	DisplayName              string                         `json:"display_name"`
	Lifecycle                openapi.ProfileLifecycle       `json:"lifecycle"`
	SpecDigest               string                         `json:"spec_digest"`
	SpecVersion              string                         `json:"spec_version"`
	Capabilities             openapi.Capabilities           `json:"capabilities"`
	AuthenticationConfigured bool                           `json:"authentication_configured"`
	AuthenticationMode       *changehost.AuthenticationMode `json:"authentication_mode,omitempty"`
	RequiredConfirmations    []openapi.RequiredConfirmation `json:"required_confirmations,omitempty"`
	Warnings                 []string                       `json:"warnings,omitempty"`
	CreatedAt                time.Time                      `json:"created_at"`
	UpdatedAt                time.Time                      `json:"updated_at"`
	VerifiedAt               *time.Time                     `json:"verified_at,omitempty"`
	ActivatedAt              *time.Time                     `json:"activated_at,omitempty"`
	LastFailureCode          string                         `json:"last_failure_code,omitempty"`
}

type storedProbeReport struct {
	Plan                  storedProbePlan                `json:"plan"`
	RequiredConfirmations []openapi.RequiredConfirmation `json:"required_confirmations,omitempty"`
	Warnings              []string                       `json:"warnings,omitempty"`
}

func profileDTOFromRecord(record db.ChangeHostProfileRecord) changeHostProfileDTO {
	dto := changeHostProfileDTO{
		ProfileID: record.ProfileID, HostID: record.HostID,
		ProfileRevision: record.ProfileRevision, DisplayName: record.DisplayName,
		Lifecycle: record.Lifecycle, SpecDigest: record.SpecDigest, SpecVersion: record.SpecVersion,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
		VerifiedAt: record.VerifiedAt, ActivatedAt: record.ActivatedAt,
		LastFailureCode: record.LastFailureCode,
	}
	if profile, err := openapi.DecodeProfile([]byte(record.ProfileJSON)); err == nil {
		dto.Capabilities = profile.Capabilities
		if reference, ok := model.ParseCredentialReference(profile.Authentication.CredentialReference); ok {
			dto.AuthenticationConfigured = true
			if mode, known := changehost.AuthenticationModeForReference(reference); known {
				dto.AuthenticationMode = &mode
			}
		}
	}
	if report := decodeStoredProbeReport(record.InferenceReportJSON); report != nil {
		dto.RequiredConfirmations = report.RequiredConfirmations
		dto.Warnings = report.Warnings
	}
	return dto
}

func decodeStoredProbeReport(raw string) *storedProbeReport {
	if raw == "" {
		return nil
	}
	var report storedProbeReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		return nil
	}
	return &report
}

// --- import ---------------------------------------------------------------

func (s *Server) handleImportChangeHostProfile(w http.ResponseWriter, r *http.Request) {
	if s.DB == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxOpenAPIImportBytes)
	if err := r.ParseMultipartForm(maxOpenAPIImportBytes); err != nil {
		writeAPIError(w, http.StatusRequestEntityTooLarge, string(openapi.IssueDocumentInvalid))
		return
	}
	file, _, err := r.FormFile("document")
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, string(openapi.IssueDocumentInvalid))
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxOpenAPIImportBytes))
	if err != nil {
		writeAPIError(w, http.StatusRequestEntityTooLarge, string(openapi.IssueDocumentInvalid))
		return
	}
	displayName := strings.TrimSpace(r.FormValue("display_name"))
	apiBaseURL := strings.TrimSpace(r.FormValue("api_base_url"))
	sampleURL := strings.TrimSpace(r.FormValue("sample_change_url"))
	credentialMode := strings.TrimSpace(r.FormValue("credential_mode"))
	credentialEnvName := strings.TrimSpace(r.FormValue("credential_env_name"))

	document, err := openapi.ParseDocument(raw)
	if err != nil {
		var docErr *openapi.DocumentError
		if errors.As(err, &docErr) {
			writeAPIError(w, http.StatusBadRequest, string(docErr.Code))
			return
		}
		writeAPIError(w, http.StatusBadRequest, string(openapi.IssueDocumentInvalid))
		return
	}
	sample, ok := openapi.AnalyzeSampleURL(sampleURL)
	if !ok || displayName == "" {
		writeAPIError(w, http.StatusBadRequest, string(openapi.IssueDocumentInvalid))
		return
	}
	// Canonicalize the display origin once: profile, host record, and the
	// approved host identity must all share one representation (an explicit
	// default port must not split them).
	if canonical, ok := openapi.NormalizeOrigin(sample.Origin); ok {
		sample.Origin = canonical
	}

	var credentialReference model.CredentialReference
	switch credentialMode {
	case "environment":
		parsed, ok := model.ParseCredentialReference("env:" + credentialEnvName)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, string(openapi.IssueCredentialUnavailable))
			return
		}
		credentialReference = parsed
	case "keyring":
		// The OS keyring write path is not wired in this build; only
		// environment-variable references are accepted.
		writeAPIError(w, http.StatusBadRequest, string(openapi.IssueCredentialUnavailable), "keyring credential storage is not available in this build")
		return
	default:
		writeAPIError(w, http.StatusBadRequest, string(openapi.IssueCredentialUnavailable))
		return
	}

	baseURL, origins, err := resolveImportOrigins(document, apiBaseURL, sample.Origin)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, string(openapi.IssueDocumentInvalid), err.Error())
		return
	}
	scheme, ok := firstSupportedSecurityScheme(document)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, string(openapi.IssueCredentialUnavailable), "document declares no header-based security scheme")
		return
	}

	hostID := openAPIHostIDForOrigin(sample.Origin)
	if err := s.DB.StoreChangeHostPreview(db.ChangeHostRecord{
		HostID: hostID, Provider: model.ChangeProviderOpenAPI,
		DisplayOrigin: sample.Origin, EndpointOrigins: origins,
	}); err != nil {
		existing, exists, readErr := s.DB.ChangeHost(hostID)
		if readErr != nil || !exists || existing.Provider != model.ChangeProviderOpenAPI || existing.Lifecycle == "revoked" {
			writeAPIError(w, http.StatusConflict, "change_host_conflict")
			return
		}
	}

	candidates := openapi.ScoreOperations(document, sample, baseURL)
	grouped := openapi.TopCandidatesPerRole(candidates)
	plan := storedProbePlan{
		SampleNumber:         sample.Number,
		SampleRepositorySlug: sample.RepositorySlug,
		SamplePathSegments:   sample.Segments,
	}
	for _, roleCandidates := range grouped {
		for _, candidate := range roleCandidates {
			plan.Candidates = append(plan.Candidates, storedProbeCandidate{
				Role: string(candidate.Role), Method: candidate.Operation.Method,
				Path: candidate.Operation.Path, BaseURL: candidate.BaseURL,
				Score: candidate.Score, Bindings: candidate.Bindings,
			})
		}
	}
	sort.Slice(plan.Candidates, func(i, j int) bool {
		if plan.Candidates[i].Role != plan.Candidates[j].Role {
			return plan.Candidates[i].Role < plan.Candidates[j].Role
		}
		return plan.Candidates[i].Score > plan.Candidates[j].Score
	})

	// Re-imports onto an existing host create the next revision (design §8:
	// edits never mutate an existing revision in place).
	revision := 1
	if existing, err := s.DB.ListChangeHostProfiles(hostID); err == nil && len(existing) > 0 {
		revision = existing[0].ProfileRevision + 1 // ListChangeHostProfiles orders by revision DESC
	}
	profileID := newProfileID()
	reference := openapi.ReferenceTemplate{
		Origin:              sample.Origin,
		PathTemplate:        referencePathTemplate(sample),
		RepositoryParameter: "repository",
		NumberParameter:     "number",
	}
	draft := openapi.Profile{
		SchemaVersion:   openapi.SchemaVersion,
		ProfileID:       profileID,
		ProfileRevision: revision,
		DisplayName:     displayName,
		Adapter:         openapi.AdapterKind,
		HostID:          hostID,
		DisplayOrigin:   sample.Origin,
		EndpointOrigins: origins,
		Reference:       reference,
		Authentication: openapi.Authentication{
			Scheme:              "header",
			HeaderName:          scheme.HeaderName,
			ValuePrefix:         scheme.ValuePrefix,
			CredentialReference: string(credentialReference),
		},
		Capabilities: openapi.Capabilities{
			Metadata: openapi.CapabilityUnsupported, FileSet: openapi.CapabilityUnsupported,
			Patches: openapi.CapabilityUnsupported, Modes: openapi.CapabilityUnsupported,
			Commits: openapi.CapabilityUnsupported, RepositoryID: openapi.CapabilityUnsupported,
		},
		SpecDigest: document.Digest,
	}
	profileJSON, err := openapi.EncodeProfile(draft)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	reportJSON, err := json.Marshal(storedProbeReport{Plan: plan})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	record := db.ChangeHostProfileRecord{
		ProfileID: profileID, HostID: hostID, ProfileRevision: revision,
		SchemaVersion: openapi.SchemaVersion, DisplayName: displayName,
		Lifecycle: openapi.ProfileDraft, ProfileJSON: string(profileJSON),
		InferenceReportJSON: string(reportJSON), SpecDigest: document.Digest,
		SpecVersion: document.Version,
	}
	if err := s.DB.CreateChangeHostProfileDraft(record); err != nil {
		writeAPIError(w, http.StatusConflict, "change_profile_conflict")
		return
	}
	// Re-read so the DTO carries the persisted timestamps.
	if stored, exists, readErr := s.DB.ChangeHostProfile(record.ProfileID); readErr == nil && exists {
		record = stored
	}

	hostRecord, _, err := s.DB.ChangeHost(hostID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{
		"profile":                profileDTOFromRecord(record),
		"endpoint_origins":       origins,
		"host":                   hostIdentityFromRecord(hostRecord),
		"candidate_count":        len(plan.Candidates),
		"requires_host_approval": hostRecord.Lifecycle != "approved",
	})
}

// resolveImportOrigins derives the API base URL and the full endpoint origin
// set. Relative document servers resolve against the user-provided base URL;
// every origin (sample display origin included) lands in the approval set.
func resolveImportOrigins(document *openapi.Document, apiBaseURL, sampleOrigin string) (string, []string, error) {
	base := strings.TrimSuffix(strings.TrimSpace(apiBaseURL), "/")
	if base == "" {
		for _, server := range document.ServerURLs {
			if strings.HasPrefix(server, "https://") || strings.HasPrefix(server, "http://") {
				base = strings.TrimSuffix(server, "/")
				break
			}
		}
	}
	if base == "" {
		return "", nil, fmt.Errorf("an absolute API base URL is required")
	}
	originOf := func(raw string) string {
		if idx := strings.Index(raw, "://"); idx >= 0 {
			rest := raw[idx+3:]
			if slash := strings.Index(rest, "/"); slash >= 0 {
				raw = raw[:idx+3+slash]
			}
			if canonical, ok := openapi.NormalizeOrigin(raw); ok {
				return canonical
			}
		}
		return ""
	}
	seen := map[string]bool{}
	origins := []string{}
	add := func(raw string) {
		origin := originOf(raw)
		if origin != "" && !seen[origin] {
			seen[origin] = true
			origins = append(origins, origin)
		}
	}
	add(sampleOrigin)
	add(base)
	for _, server := range document.ServerURLs {
		if strings.HasPrefix(server, "http") {
			add(server)
		} else if strings.HasPrefix(server, "/") {
			add(base)
		}
	}
	if len(origins) == 0 {
		return "", nil, fmt.Errorf("no endpoint origins could be derived")
	}
	return base, origins, nil
}

func firstSupportedSecurityScheme(document *openapi.Document) (openapi.SecurityScheme, bool) {
	if len(document.SecuritySchemes) == 0 {
		return openapi.SecurityScheme{}, false
	}
	return document.SecuritySchemes[0], true
}

// openAPIHostIDForOrigin derives a stable host key from the display origin.
func openAPIHostIDForOrigin(origin string) string {
	host := strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
	var b strings.Builder
	for _, r := range strings.ToLower(host) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return "openapi-" + strings.Trim(b.String(), "-")
}

// referencePathTemplate generalizes the sample URL path: the repository range
// becomes {repository} and the numeric change segment becomes {number}.
func referencePathTemplate(sample openapi.SampleReference) string {
	segments := append([]string(nil), sample.Segments...)
	if sample.NumberIndex >= 0 && sample.NumberIndex < len(segments) {
		segments[sample.NumberIndex] = "{number}"
	}
	// Replace the contiguous repository segments with a single placeholder.
	if sample.RepositorySlug != "" {
		repositoryParts := strings.Split(sample.RepositorySlug, "/")
		joined := "/" + strings.Join(segments, "/") + "/"
		needle := "/" + sample.RepositorySlug + "/"
		if idx := strings.Index(joined, needle); idx >= 0 {
			joined = joined[:idx] + "/{repository}/" + joined[idx+len(needle):]
			return strings.TrimSuffix(joined, "/")
		}
		_ = repositoryParts
	}
	return "/" + strings.Join(segments, "/")
}

func newProfileID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return "profile-" + hex.EncodeToString(buf)
}

// --- list / get -----------------------------------------------------------

func (s *Server) handleListChangeHostProfiles(w http.ResponseWriter, r *http.Request) {
	if s.DB == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	hostID := r.URL.Query().Get("host_id")
	var records []db.ChangeHostProfileRecord
	var err error
	if hostID != "" {
		records, err = s.DB.ListChangeHostProfiles(hostID)
	} else {
		records, err = listAllChangeHostProfiles(s.DB)
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	profiles := make([]changeHostProfileDTO, 0, len(records))
	for _, record := range records {
		profiles = append(profiles, profileDTOFromRecord(record))
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{"profiles": profiles})
}

func listAllChangeHostProfiles(database *db.DB) ([]db.ChangeHostProfileRecord, error) {
	hosts, err := database.ListChangeHosts()
	if err != nil {
		return nil, err
	}
	all := []db.ChangeHostProfileRecord{}
	for _, host := range hosts {
		profiles, err := database.ListChangeHostProfiles(host.HostID)
		if err != nil {
			return nil, err
		}
		all = append(all, profiles...)
	}
	return all, nil
}

func (s *Server) handleGetChangeHostProfile(w http.ResponseWriter, r *http.Request) {
	record, ok := s.loadChangeHostProfile(w, r.PathValue("profileId"))
	if !ok {
		return
	}
	writeJSONStatus(w, http.StatusOK, profileDTOFromRecord(record))
}

func (s *Server) loadChangeHostProfile(w http.ResponseWriter, profileID string) (db.ChangeHostProfileRecord, bool) {
	if s.DB == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return db.ChangeHostProfileRecord{}, false
	}
	record, exists, err := s.DB.ChangeHostProfile(profileID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return db.ChangeHostProfileRecord{}, false
	}
	if !exists {
		writeAPIError(w, http.StatusNotFound, string(openapi.IssueProbeFailed), "change_host_profile_not_found")
		return db.ChangeHostProfileRecord{}, false
	}
	return record, true
}

// --- probe / verify ---------------------------------------------------------

// handleProbeChangeHostProfile executes the stored probe plan against the
// approved host. On success the draft is rebuilt from real responses; when
// every required field maps confidently the profile becomes verified.
func (s *Server) handleProbeChangeHostProfile(w http.ResponseWriter, r *http.Request) {
	s.runChangeHostProfileProbe(w, r)
}

func (s *Server) handleVerifyChangeHostProfile(w http.ResponseWriter, r *http.Request) {
	// Verify is the probe re-run: it is side-effect free apart from the draft
	// document refresh and lifecycle transition.
	s.runChangeHostProfileProbe(w, r)
}

func (s *Server) runChangeHostProfileProbe(w http.ResponseWriter, r *http.Request) {
	record, ok := s.loadChangeHostProfile(w, r.PathValue("profileId"))
	if !ok {
		return
	}
	if record.Lifecycle != openapi.ProfileDraft && record.Lifecycle != openapi.ProfileInvalid {
		writeAPIError(w, http.StatusConflict, "change_profile_conflict")
		return
	}
	report := decodeStoredProbeReport(record.InferenceReportJSON)
	if report == nil || len(report.Plan.Candidates) == 0 {
		writeAPIError(w, http.StatusConflict, string(openapi.IssueProbeFailed), "no stored probe plan")
		return
	}
	profile, err := openapi.DecodeProfile([]byte(record.ProfileJSON))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
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
	approved, err := s.approvedHost(r.Context(), hostRecord)
	if err != nil {
		writeHostPolicyError(w, err)
		return
	}
	reference, ok := model.ParseCredentialReference(profile.Authentication.CredentialReference)
	if !ok {
		writeAPIError(w, http.StatusConflict, string(openapi.IssueCredentialUnavailable))
		return
	}
	var source changehost.CredentialSource
	switch reference.Scheme() {
	case model.CredentialSchemeEnvironment:
		source = changehost.EnvironmentCredentialSource{}
	default:
		writeAPIError(w, http.StatusConflict, string(openapi.IssueCredentialUnavailable), "keyring credential resolution is not available in this build")
		return
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
		writeAPIError(w, http.StatusConflict, string(openapi.IssueCredentialUnavailable))
		return
	}

	sample := openapi.SampleReference{
		Origin:         profile.DisplayOrigin,
		Number:         report.Plan.SampleNumber,
		RepositorySlug: report.Plan.SampleRepositorySlug,
		Segments:       report.Plan.SamplePathSegments,
	}
	inferenceContext := openapi.InferenceContext{
		SampleNumber:         report.Plan.SampleNumber,
		SampleRepositorySlug: report.Plan.SampleRepositorySlug,
		DisplayOrigin:        profile.DisplayOrigin,
	}
	grouped := map[openapi.OperationID][]openapi.OperationCandidate{}
	for _, stored := range report.Plan.Candidates {
		role := openapi.OperationID(stored.Role)
		if !openapi.IsKnownOperationID(role) {
			continue
		}
		grouped[role] = append(grouped[role], openapi.OperationCandidate{
			Role: role,
			Operation: openapi.SpecOperation{
				ID: string(role) + " " + stored.Path, Method: stored.Method, Path: stored.Path,
			},
			Score: stored.Score, Bindings: stored.Bindings, BaseURL: stored.BaseURL,
		})
	}
	outcomes := changehost.ProbeOpenAPICandidates(r.Context(), client, grouped, sample, inferenceContext)

	built := openapi.BuildProfile(
		profile.ProfileID, profile.ProfileRevision, profile.DisplayName, profile.HostID,
		profile.DisplayOrigin, profile.EndpointOrigins, profile.Reference, profile.Authentication,
		profile.SpecDigest, record.SpecVersion, outcomes,
	)
	// User-confirmed mappings survive a re-probe: a selector the user already
	// picked is re-applied when its pointer is still among the probed
	// candidates, instead of being asked about again on every verify.
	preserveConfirmedMappings(profile, &built, outcomes)
	profileJSON, err := openapi.EncodeProfile(built.Profile)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	report.RequiredConfirmations = built.RequiredConfirmations
	report.Warnings = built.Warnings
	reportJSON, err := json.Marshal(report)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	if err := s.DB.UpdateChangeHostProfileDraft(record.ProfileID, string(profileJSON), string(reportJSON)); err != nil {
		writeAPIError(w, http.StatusConflict, "change_profile_conflict")
		return
	}

	verified := false
	if len(built.RequiredConfirmations) == 0 && len(built.Warnings) == 0 {
		if issues := openapi.ValidateProfile(built.Profile); issues.OK() {
			if err := s.DB.MarkChangeHostProfileVerified(record.ProfileID, time.Now().UTC()); err == nil {
				verified = true
			}
		}
	}
	updated, exists, err := s.DB.ChangeHostProfile(record.ProfileID)
	if err != nil || !exists {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	dto := profileDTOFromRecord(updated)
	writeJSONStatus(w, http.StatusOK, map[string]any{
		"profile":                dto,
		"verified":               verified,
		"required_confirmations": built.RequiredConfirmations,
		"warnings":               built.Warnings,
		"capabilities":           built.Profile.Capabilities,
	})
}

// --- mapping ----------------------------------------------------------------

type changeHostProfileMappingRequest struct {
	Selections []struct {
		Role    string `json:"role"`
		Field   string `json:"field"`
		Pointer string `json:"pointer"`
	} `json:"selections"`
}

// handlePatchChangeHostProfileMapping applies user confirmations. A selection
// may only pick one of the pointers the probe report offered for that field —
// arbitrary new mappings cannot be smuggled past the probe.
func (s *Server) handlePatchChangeHostProfileMapping(w http.ResponseWriter, r *http.Request) {
	record, ok := s.loadChangeHostProfile(w, r.PathValue("profileId"))
	if !ok {
		return
	}
	if record.Lifecycle != openapi.ProfileDraft && record.Lifecycle != openapi.ProfileInvalid {
		writeAPIError(w, http.StatusConflict, "change_profile_conflict")
		return
	}
	var request changeHostProfileMappingRequest
	if !decodeBoundedJSON(w, r, &request) {
		return
	}
	report := decodeStoredProbeReport(record.InferenceReportJSON)
	if report == nil {
		writeAPIError(w, http.StatusConflict, string(openapi.IssueProbeFailed))
		return
	}
	profile, err := openapi.DecodeProfile([]byte(record.ProfileJSON))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	for _, selection := range request.Selections {
		role := openapi.OperationID(selection.Role)
		var chosen *openapi.FieldCandidate
		for _, confirmation := range report.RequiredConfirmations {
			if confirmation.Role != role || confirmation.Field != selection.Field {
				continue
			}
			for i := range confirmation.Candidates {
				if confirmation.Candidates[i].Pointer == selection.Pointer {
					candidate := confirmation.Candidates[i]
					chosen = &candidate
				}
			}
		}
		if chosen == nil {
			writeAPIError(w, http.StatusBadRequest, string(openapi.IssueMappingIncomplete),
				"selection must pick one of the probed candidate pointers")
			return
		}
		operation := profile.Operations.ForID(role)
		if operation == nil {
			writeAPIError(w, http.StatusBadRequest, string(openapi.IssueMappingIncomplete))
			return
		}
		if operation.Response.Fields == nil {
			operation.Response.Fields = map[string]openapi.FieldSelector{}
		}
		operation.Response.Fields[selection.Field] = openapi.FieldSelector{
			Pointer: chosen.Pointer, Transform: chosen.Transform,
		}
		// A confirmed head_sha on resolve_change restores the content anchor.
		if role == openapi.OperationResolveChange && selection.Field == "head_sha" {
			profile.Capabilities.ContentAnchor = "head_sha"
		}
		report.RequiredConfirmations = dropResolvedConfirmation(report.RequiredConfirmations, role, selection.Field)
	}
	profileJSON, err := openapi.EncodeProfile(profile)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	reportJSON, err := json.Marshal(report)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	if err := s.DB.UpdateChangeHostProfileDraft(record.ProfileID, string(profileJSON), string(reportJSON)); err != nil {
		writeAPIError(w, http.StatusConflict, "change_profile_conflict")
		return
	}
	updated, exists, err := s.DB.ChangeHostProfile(record.ProfileID)
	if err != nil || !exists {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSONStatus(w, http.StatusOK, profileDTOFromRecord(updated))
}

func dropResolvedConfirmation(confirmations []openapi.RequiredConfirmation, role openapi.OperationID, field string) []openapi.RequiredConfirmation {
	kept := confirmations[:0]
	for _, confirmation := range confirmations {
		if confirmation.Role == role && confirmation.Field == field {
			continue
		}
		kept = append(kept, confirmation)
	}
	return kept
}

// preserveConfirmedMappings carries selectors the user already confirmed into
// a freshly rebuilt profile so a verify re-probe never re-asks settled
// questions. A selector is carried only when its pointer is still among the
// candidates the latest probe observed.
func preserveConfirmedMappings(existing openapi.Profile, built *openapi.BuildResult, outcomes map[openapi.OperationID][]openapi.RoleOutcome) {
	for _, role := range openapi.OperationIDs() {
		existingOperation := existing.Operations.ForID(role)
		builtOperation := built.Profile.Operations.ForID(role)
		if existingOperation == nil || builtOperation == nil {
			continue
		}
		var outcomeFields []openapi.FieldCandidate
		for _, outcome := range outcomes[role] {
			if outcome.RejectReason == "" {
				outcomeFields = outcome.Fields
				break
			}
		}
		for field, selector := range existingOperation.Response.Fields {
			stillOffered := false
			for _, candidate := range outcomeFields {
				if candidate.Field == field && candidate.Pointer == selector.Pointer {
					stillOffered = true
					break
				}
			}
			if !stillOffered {
				continue
			}
			if builtOperation.Response.Fields == nil {
				builtOperation.Response.Fields = map[string]openapi.FieldSelector{}
			}
			if _, alreadyMapped := builtOperation.Response.Fields[field]; !alreadyMapped {
				builtOperation.Response.Fields[field] = selector
			}
			built.RequiredConfirmations = dropResolvedConfirmation(built.RequiredConfirmations, role, field)
			if role == openapi.OperationResolveChange && field == "head_sha" {
				built.Profile.Capabilities.ContentAnchor = "head_sha"
			}
		}
	}
	// A settled content anchor also settles its warning.
	if built.Profile.Capabilities.ContentAnchor != "" {
		warnings := built.Warnings[:0]
		for _, warning := range built.Warnings {
			if warning == string(openapi.IssueContentAnchorMissing) {
				continue
			}
			warnings = append(warnings, warning)
		}
		built.Warnings = warnings
	}
}

// --- revoke -------------------------------------------------------------------

func (s *Server) handleRevokeChangeHostProfile(w http.ResponseWriter, r *http.Request) {
	record, ok := s.loadChangeHostProfile(w, r.PathValue("profileId"))
	if !ok {
		return
	}
	if err := s.DB.RevokeChangeHostProfile(record.ProfileID, time.Now().UTC()); err != nil {
		if errors.Is(err, db.ErrChangeHostProfileConflict) {
			writeAPIError(w, http.StatusConflict, "change_profile_conflict")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	// A revoked profile stops parsing immediately and atomically.
	if err := s.refreshOpenAPIHostParsers(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
