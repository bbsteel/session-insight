package openapi

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/bbsteel/session-insight/internal/model"
)

// ProfileIssueCode is a stable, non-localized validation/verification failure
// code (design §14). UI copy is keyed by these values; details are diagnostic
// only.
type ProfileIssueCode string

const (
	IssueDocumentInvalid        ProfileIssueCode = "openapi_document_invalid"
	IssueExternalReference      ProfileIssueCode = "openapi_external_reference_rejected"
	IssueMappingIncomplete      ProfileIssueCode = "change_profile_mapping_incomplete"
	IssueMappingAmbiguous       ProfileIssueCode = "change_profile_mapping_ambiguous"
	IssueProbeFailed            ProfileIssueCode = "change_profile_probe_failed"
	IssueSchemaDrift            ProfileIssueCode = "change_profile_schema_drift"
	IssueReferenceAmbiguous     ProfileIssueCode = "change_profile_reference_ambiguous"
	IssueContentAnchorMissing   ProfileIssueCode = "change_profile_content_anchor_missing"
	IssueCredentialUnavailable  ProfileIssueCode = "change_profile_credential_unavailable"
	IssueProfileContractInvalid ProfileIssueCode = "change_profile_contract_invalid"
)

// ValidationIssue is one structural contract violation. Code is stable;
// Detail is diagnostic only.
type ValidationIssue struct {
	Code   ProfileIssueCode `json:"code"`
	Field  string           `json:"field"`
	Detail string           `json:"detail"`
}

// ValidationIssues is the ordered result of ValidateProfile.
type ValidationIssues []ValidationIssue

// OK reports whether the profile passed structural validation.
func (issues ValidationIssues) OK() bool { return len(issues) == 0 }

// Error summarizes the issues without embedding user-supplied profile values.
func (issues ValidationIssues) Error() string {
	if len(issues) == 0 {
		return "provider profile is valid"
	}
	return fmt.Sprintf("provider profile invalid: %s on %s", issues[0].Code, issues[0].Field)
}

const (
	maxDisplayNameLength    = 128
	maxHostIDLength         = 512
	maxPathTemplateLength   = 1024
	maxPathTemplateSegments = 32
	maxJSONPointerLength    = 512
	maxJSONPointerDepth     = 16
	maxHeaderValueLength    = 256
	maxFixedParameterLength = 256
	maxEndpointOrigins      = 8
	maxFieldSelectorCount   = 64
	maxOperationHeaderCount = 16
	maxOperationParamCount  = 32
	specDigestPrefix        = "sha256:"
)

// profileIDPattern accepts the generated "profile-01J..." shape and comparable
// opaque identifiers while rejecting whitespace and control characters.
var profileIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// templateParameterPattern matches one {name} placeholder segment content.
var templateParameterPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

// contentAnchorValues is the closed set of stable content anchors. updated_at,
// ETag, titles, and lifecycle state are deliberately absent: they can change
// on pure metadata edits and must never mint a content version alone.
var contentAnchorValues = map[string]bool{
	"head_sha":       true,
	"diff_version":   true,
	"native_version": true,
}

// standardFields is the closed vocabulary of SessionInsight model fields a
// selector may target. Extending the mapping model means extending this set.
var standardFields = map[string]bool{
	"provider_object_id":     true,
	"display_number":         true,
	"title":                  true,
	"lifecycle_state":        true,
	"web_url":                true,
	"target_repository_slug": true,
	"target_repository_id":   true,
	"source_repository_slug": true,
	"source_ref":             true,
	"target_ref":             true,
	"head_sha":               true,
	"base_sha":               true,
	"diff_base_sha":          true,
	"merge_commit_sha":       true,
	"squash_commit_sha":      true,
	"native_version":         true,
	"repository_id":          true,
	"repository_slug":        true,
	"path":                   true,
	"old_path":               true,
	"status":                 true,
	"patch":                  true,
	"old_mode":               true,
	"new_mode":               true,
	"additions":              true,
	"deletions":              true,
	"sha":                    true,
	"subject":                true,
	"author_name":            true,
	"authored_at":            true,
	"committed_at":           true,
	"diff_text":              true,
}

// reviewedAuthorizationPrefixes are the only credential prefixes allowed on
// the shared Authorization header (design §5.3).
var reviewedAuthorizationPrefixes = map[string]bool{
	"Bearer ": true,
	"token ":  true,
}

// ValidateProfile enforces the schema-version-1 structural contract. It is
// pure and local: no network, no credential resolution, no clock.
func ValidateProfile(profile Profile) ValidationIssues {
	v := &profileValidator{}
	if profile.SchemaVersion != SchemaVersion {
		v.add(IssueProfileContractInvalid, "schema_version", fmt.Sprintf("must be %d", SchemaVersion))
	}
	if !profileIDPattern.MatchString(profile.ProfileID) {
		v.add(IssueProfileContractInvalid, "profile_id", "must be an opaque identifier of letters, digits, and . _ : -")
	}
	if profile.ProfileRevision < 1 {
		v.add(IssueProfileContractInvalid, "profile_revision", "must be >= 1")
	}
	if profile.DisplayName == "" || strings.TrimSpace(profile.DisplayName) != profile.DisplayName ||
		len(profile.DisplayName) > maxDisplayNameLength {
		v.add(IssueProfileContractInvalid, "display_name", "must be a non-empty trimmed value within the length bound")
	}
	if profile.Adapter != AdapterKind {
		v.add(IssueProfileContractInvalid, "adapter", fmt.Sprintf("must be %q", AdapterKind))
	}
	if profile.HostID == "" || len(profile.HostID) > maxHostIDLength ||
		strings.TrimSpace(profile.HostID) != profile.HostID || strings.ContainsRune(profile.HostID, '\x00') {
		v.add(IssueProfileContractInvalid, "host_id", "must be a non-empty trimmed value without NUL")
	}
	displayOrigin := validateOriginValue(v, "display_origin", profile.DisplayOrigin)
	if len(profile.EndpointOrigins) == 0 || len(profile.EndpointOrigins) > maxEndpointOrigins {
		v.add(IssueProfileContractInvalid, "endpoint_origins", "must declare between 1 and 8 endpoint origins")
	}
	seenOrigins := map[string]bool{}
	displayOriginListed := false
	for i, rawOrigin := range profile.EndpointOrigins {
		field := fmt.Sprintf("endpoint_origins[%d]", i)
		origin := validateOriginValue(v, field, rawOrigin)
		if origin == "" {
			continue
		}
		if seenOrigins[origin] {
			v.add(IssueProfileContractInvalid, field, "duplicate endpoint origin")
		}
		seenOrigins[origin] = true
		if origin == displayOrigin {
			displayOriginListed = true
		}
	}
	if displayOrigin != "" && !displayOriginListed && len(profile.EndpointOrigins) > 0 {
		v.add(IssueProfileContractInvalid, "endpoint_origins", "must include the display origin")
	}
	validateReferenceTemplate(v, profile.Reference, displayOrigin)
	validateAuthentication(v, profile.Authentication)
	validateOperations(v, profile)
	validateCapabilities(v, profile)
	validateLimits(v, profile.Limits)
	if !strings.HasPrefix(profile.SpecDigest, specDigestPrefix) ||
		len(profile.SpecDigest) != len(specDigestPrefix)+64 ||
		!isLowerHex(profile.SpecDigest[len(specDigestPrefix):]) {
		v.add(IssueProfileContractInvalid, "spec_digest", "must be sha256:<64 lowercase hex>")
	}
	return v.issues
}

type profileValidator struct {
	issues ValidationIssues
}

func (v *profileValidator) add(code ProfileIssueCode, field, detail string) {
	v.issues = append(v.issues, ValidationIssue{Code: code, Field: field, Detail: detail})
}

// normalizeOriginValue canonicalizes an absolute http(s) origin: scheme and
// host are lowercased so equivalent spellings compare equal everywhere the
// allowlist is consulted.
func normalizeOriginValue(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", false
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host), true
}

// validateOriginValue requires an absolute http(s) origin: scheme, host, and
// optional port only. Paths, queries, fragments, and userinfo are rejected so
// an origin can never smuggle a token or pin one API path.
func validateOriginValue(v *profileValidator, field, raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		v.add(IssueProfileContractInvalid, field, "must be an absolute HTTP(S) origin")
		return ""
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" ||
		(u.Path != "" && u.Path != "/") {
		v.add(IssueProfileContractInvalid, field, "must not contain userinfo, path, query, or fragment data")
		return ""
	}
	origin, _ := normalizeOriginValue(raw)
	return origin
}

// validatePathTemplate enforces the restricted template grammar: literal
// segments match exactly, {name} segments are the only parameters, and no
// segment may attempt traversal.
func validatePathTemplate(v *profileValidator, field, template string) []string {
	if template == "" || !strings.HasPrefix(template, "/") ||
		len(template) > maxPathTemplateLength || strings.ContainsRune(template, '\x00') {
		v.add(IssueProfileContractInvalid, field, "must be a bounded absolute path template")
		return nil
	}
	trimmed := strings.Trim(template, "/")
	if trimmed == "" {
		v.add(IssueProfileContractInvalid, field, "must not be the root path")
		return nil
	}
	segments := strings.Split(trimmed, "/")
	if len(segments) > maxPathTemplateSegments {
		v.add(IssueProfileContractInvalid, field, "declares too many path segments")
		return nil
	}
	parameters := []string{}
	seen := map[string]bool{}
	for i, segment := range segments {
		segmentField := fmt.Sprintf("%s[%d]", field, i)
		if segment == "" || segment == "." || segment == ".." {
			v.add(IssueProfileContractInvalid, segmentField, "empty and traversal segments are forbidden")
			continue
		}
		if strings.HasPrefix(segment, "{") || strings.HasSuffix(segment, "}") {
			if !strings.HasPrefix(segment, "{") || !strings.HasSuffix(segment, "}") || len(segment) < 3 {
				v.add(IssueProfileContractInvalid, segmentField, "parameters must occupy a whole path segment")
				continue
			}
			name := segment[1 : len(segment)-1]
			if !templateParameterPattern.MatchString(name) {
				v.add(IssueProfileContractInvalid, segmentField, "invalid parameter name")
				continue
			}
			if seen[name] {
				v.add(IssueProfileContractInvalid, segmentField, "duplicate path parameter")
				continue
			}
			seen[name] = true
			parameters = append(parameters, name)
			continue
		}
		if strings.ContainsAny(segment, "{}?#") {
			v.add(IssueProfileContractInvalid, segmentField, "literal segments must not contain template or URL syntax")
		}
	}
	return parameters
}

func validateReferenceTemplate(v *profileValidator, reference ReferenceTemplate, displayOrigin string) {
	origin := validateOriginValue(v, "reference.origin", reference.Origin)
	if origin != "" && displayOrigin != "" && origin != displayOrigin {
		v.add(IssueProfileContractInvalid, "reference.origin", "must match the profile display origin")
	}
	parameters := validatePathTemplate(v, "reference.path_template", reference.PathTemplate)
	declared := map[string]bool{}
	for _, name := range parameters {
		declared[name] = true
	}
	if !templateParameterPattern.MatchString(reference.RepositoryParameter) {
		v.add(IssueProfileContractInvalid, "reference.repository_parameter", "invalid parameter name")
	} else if !declared[reference.RepositoryParameter] {
		v.add(IssueMappingIncomplete, "reference.repository_parameter", "must name a path template parameter")
	}
	if !templateParameterPattern.MatchString(reference.NumberParameter) {
		v.add(IssueProfileContractInvalid, "reference.number_parameter", "invalid parameter name")
	} else if !declared[reference.NumberParameter] {
		v.add(IssueMappingIncomplete, "reference.number_parameter", "must name a path template parameter")
	}
	if reference.RepositoryParameter != "" && reference.RepositoryParameter == reference.NumberParameter {
		v.add(IssueProfileContractInvalid, "reference.number_parameter", "repository and number parameters must differ")
	}
}

func validateAuthentication(v *profileValidator, authentication Authentication) {
	if authentication.Scheme != "header" {
		v.add(IssueProfileContractInvalid, "authentication.scheme", "must be \"header\"")
	}
	if authentication.HeaderName == "" || !isValidHeaderName(authentication.HeaderName) {
		v.add(IssueProfileContractInvalid, "authentication.header_name", "must be a valid HTTP header name")
	}
	if strings.ContainsAny(authentication.ValuePrefix, "\r\n") ||
		len(authentication.ValuePrefix) > 32 {
		v.add(IssueProfileContractInvalid, "authentication.value_prefix", "must be short and free of CR/LF")
	}
	if strings.EqualFold(authentication.HeaderName, "Authorization") {
		if !reviewedAuthorizationPrefixes[authentication.ValuePrefix] {
			v.add(IssueProfileContractInvalid, "authentication.value_prefix", "Authorization requires a reviewed prefix (\"Bearer \" or \"token \")")
		}
	} else if authentication.ValuePrefix != "" {
		v.add(IssueProfileContractInvalid, "authentication.value_prefix", "custom token headers must not carry a value prefix")
	}
	if strings.EqualFold(authentication.HeaderName, "Cookie") ||
		strings.EqualFold(authentication.HeaderName, "Host") ||
		strings.EqualFold(authentication.HeaderName, "Content-Length") {
		v.add(IssueProfileContractInvalid, "authentication.header_name", "header is not eligible for credential injection")
	}
	if _, ok := model.ParseCredentialReference(authentication.CredentialReference); !ok {
		v.add(IssueCredentialUnavailable, "authentication.credential_reference", "must be a valid keyring:/env: reference")
	}
}

func validateOperations(v *profileValidator, profile Profile) {
	if profile.Operations.ResolveChange == nil {
		v.add(IssueMappingIncomplete, "operations.resolve_change", "a change detail operation is required")
	}
	for _, id := range OperationIDs() {
		operation := profile.Operations.ForID(id)
		if operation == nil {
			continue
		}
		validateOperation(v, "operations."+string(id), id, operation, profile)
	}
}

func validateOperation(v *profileValidator, field string, id OperationID, operation *Operation, profile Profile) {
	if operation.Method != http.MethodGet && operation.Method != http.MethodHead {
		v.add(IssueProfileContractInvalid, field+".method", "only GET and HEAD are allowed")
	}
	origin := validateOriginValue(v, field+".origin", operation.Origin)
	if origin != "" {
		listed := false
		for _, rawEndpoint := range profile.EndpointOrigins {
			endpointOrigin, ok := normalizeOriginValue(rawEndpoint)
			if ok && endpointOrigin == origin {
				listed = true
				break
			}
		}
		if !listed {
			v.add(IssueProfileContractInvalid, field+".origin", "must be one of the approved endpoint origins")
		}
	}
	pathParameters := map[string]bool{}
	for _, name := range validatePathTemplate(v, field+".path_template", operation.PathTemplate) {
		pathParameters[name] = true
	}
	if len(operation.Parameters) > maxOperationParamCount {
		v.add(IssueProfileContractInvalid, field+".parameters", "declares too many parameters")
	}
	bound := map[string]bool{}
	for name, binding := range operation.Parameters {
		parameterField := fmt.Sprintf("%s.parameters.%s", field, name)
		if !templateParameterPattern.MatchString(name) {
			v.add(IssueProfileContractInvalid, parameterField, "invalid parameter name")
			continue
		}
		bound[name] = true
		validateParameterBinding(v, parameterField, id, binding, profile)
	}
	for name := range pathParameters {
		if !bound[name] {
			v.add(IssueMappingIncomplete, fmt.Sprintf("%s.parameters.%s", field, name), "every path template parameter must be bound")
		}
	}
	if len(operation.Headers) > maxOperationHeaderCount {
		v.add(IssueProfileContractInvalid, field+".headers", "declares too many headers")
	}
	for name, value := range operation.Headers {
		headerField := fmt.Sprintf("%s.headers.%s", field, name)
		if !isValidHeaderName(name) {
			v.add(IssueProfileContractInvalid, headerField, "invalid header name")
			continue
		}
		if strings.ContainsAny(value, "\r\n") || value == "" || len(value) > maxHeaderValueLength {
			v.add(IssueProfileContractInvalid, headerField, "header values must be non-empty, bounded, and free of CR/LF")
		}
		if strings.EqualFold(name, profile.Authentication.HeaderName) {
			v.add(IssueProfileContractInvalid, headerField, "operations must not override the credential header")
		}
		if strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "Cookie") {
			v.add(IssueProfileContractInvalid, headerField, "operations must not inject credential-bearing headers")
		}
	}
	validatePagination(v, field+".pagination", operation.Pagination)
	validateOperationResponse(v, field+".response", id, operation.Response)
}

// referenceParameterNames is the closed set of logical reference parameters
// the reference template declares: the repository locator and the change
// number. `reference.*` bindings may only name these.
var referenceParameterNames = map[string]bool{"repository": true, "number": true}

// validateParameterBinding enforces the closed binding grammar:
// reference.<path-parameter>, operation.<operation-id>.<field>, or
// literal:<fixed non-sensitive value>.
func validateParameterBinding(v *profileValidator, field string, owner OperationID, binding string, profile Profile) {
	if binding == "" || len(binding) > maxPathTemplateLength || strings.ContainsRune(binding, '\x00') {
		v.add(IssueProfileContractInvalid, field, "binding must be a bounded value without NUL")
		return
	}
	if name, ok := strings.CutPrefix(binding, "reference."); ok {
		if !templateParameterPattern.MatchString(name) {
			v.add(IssueProfileContractInvalid, field, "reference bindings must name a valid reference parameter")
			return
		}
		if !referenceParameterNames[name] {
			v.add(IssueMappingIncomplete, field, "reference bindings must name a declared reference parameter")
		}
		return
	}
	if rest, ok := strings.CutPrefix(binding, "operation."); ok {
		operationName, fieldName, found := strings.Cut(rest, ".")
		if !found || !IsKnownOperationID(OperationID(operationName)) || OperationID(operationName) == owner {
			v.add(IssueProfileContractInvalid, field, "operation bindings must reference another known operation")
			return
		}
		if profile.Operations.ForID(OperationID(operationName)) == nil {
			v.add(IssueMappingIncomplete, field, "operation binding references an undeclared operation")
		}
		if !standardFields[fieldName] {
			v.add(IssueProfileContractInvalid, field, "operation bindings must reference a standard field")
		}
		return
	}
	if value, ok := strings.CutPrefix(binding, "literal:"); ok {
		if value == "" || len(value) > maxFixedParameterLength || strings.ContainsAny(value, "\r\n") {
			v.add(IssueProfileContractInvalid, field, "literal bindings must be bounded and free of CR/LF")
		}
		return
	}
	v.add(IssueProfileContractInvalid, field, "must be a reference.*, operation.*, or literal: binding")
}

func validatePagination(v *profileValidator, field string, pagination Pagination) {
	if !IsKnownPaginationMode(pagination.Mode) {
		v.add(IssueProfileContractInvalid, field+".mode", "unknown pagination mode")
		return
	}
	requireName := func(name, value string) {
		if value == "" {
			v.add(IssueMappingIncomplete, field+"."+name, "required by the selected pagination mode")
		} else if !templateParameterPattern.MatchString(value) {
			v.add(IssueProfileContractInvalid, field+"."+name, "invalid query parameter name")
		}
	}
	// per_page is a query parameter name in every mode that allows it; when
	// set, it must always be a syntactically valid parameter name.
	if pagination.PerPageParameter != "" && !templateParameterPattern.MatchString(pagination.PerPageParameter) {
		v.add(IssueProfileContractInvalid, field+".per_page_parameter", "invalid query parameter name")
	}
	if pagination.PerPageParameter != "" &&
		(pagination.Mode == PaginationCursorBody || pagination.Mode == PaginationCursorHeader) {
		v.add(IssueProfileContractInvalid, field+".per_page_parameter", "cursor modes forbid a per-page parameter")
	}
	switch pagination.Mode {
	case PaginationNone:
		if pagination.PageParameter != "" || pagination.PerPageParameter != "" ||
			pagination.CursorParameter != "" || pagination.NextCursorPointer != "" ||
			pagination.NextCursorHeader != "" || pagination.TotalPagesPointer != "" {
			v.add(IssueProfileContractInvalid, field, "mode none forbids all pagination fields")
		}
	case PaginationPageNumber:
		requireName("page_parameter", pagination.PageParameter)
		if pagination.CursorParameter != "" || pagination.NextCursorPointer != "" || pagination.NextCursorHeader != "" {
			v.add(IssueProfileContractInvalid, field, "page_number mode forbids cursor fields")
		}
	case PaginationLinkHeader:
		if pagination.PageParameter != "" && !templateParameterPattern.MatchString(pagination.PageParameter) {
			v.add(IssueProfileContractInvalid, field+".page_parameter", "invalid query parameter name")
		}
		if pagination.CursorParameter != "" || pagination.NextCursorPointer != "" || pagination.NextCursorHeader != "" {
			v.add(IssueProfileContractInvalid, field, "link_header mode forbids cursor fields")
		}
	case PaginationCursorBody:
		requireName("cursor_parameter", pagination.CursorParameter)
		if pagination.NextCursorPointer == "" {
			v.add(IssueMappingIncomplete, field+".next_cursor_pointer", "cursor_body mode requires a next-cursor JSON pointer")
		} else {
			validateJSONPointer(v, field+".next_cursor_pointer", pagination.NextCursorPointer)
		}
		if pagination.NextCursorHeader != "" {
			v.add(IssueProfileContractInvalid, field, "cursor_body mode forbids a next-cursor header")
		}
	case PaginationCursorHeader:
		requireName("cursor_parameter", pagination.CursorParameter)
		if pagination.NextCursorHeader == "" {
			v.add(IssueMappingIncomplete, field+".next_cursor_header", "cursor_header mode requires a next-cursor header name")
		} else if !isValidHeaderName(pagination.NextCursorHeader) {
			v.add(IssueProfileContractInvalid, field+".next_cursor_header", "invalid header name")
		}
		if pagination.NextCursorPointer != "" {
			v.add(IssueProfileContractInvalid, field, "cursor_header mode forbids a next-cursor pointer")
		}
	}
	if pagination.TotalPagesPointer != "" && pagination.Mode != PaginationPageNumber {
		v.add(IssueProfileContractInvalid, field+".total_pages_pointer", "total pages are only meaningful for page_number mode")
	}
	if pagination.TotalPagesPointer != "" {
		validateJSONPointer(v, field+".total_pages_pointer", pagination.TotalPagesPointer)
	}
}

func validateOperationResponse(v *profileValidator, field string, id OperationID, response OperationResponse) {
	if response.ItemPointer != "" && response.ItemsPointer != "" {
		v.add(IssueProfileContractInvalid, field, "item_pointer and items_pointer are mutually exclusive")
	}
	if response.ItemPointer != "" {
		validateJSONPointer(v, field+".item_pointer", response.ItemPointer)
	}
	if response.ItemsPointer != "" {
		validateJSONPointer(v, field+".items_pointer", response.ItemsPointer)
	}
	listOperation := id == OperationListFiles || id == OperationListCommits
	if listOperation && response.ItemsPointer == "" {
		v.add(IssueMappingIncomplete, field+".items_pointer", "list operations must declare their array location")
	}
	if !listOperation && response.ItemsPointer != "" {
		v.add(IssueProfileContractInvalid, field+".items_pointer", "single-object operations must use item_pointer")
	}
	if len(response.Fields) == 0 {
		v.add(IssueMappingIncomplete, field+".fields", "an operation must map at least one standard field")
	}
	if len(response.Fields) > maxFieldSelectorCount {
		v.add(IssueProfileContractInvalid, field+".fields", "maps too many fields")
	}
	for name, selector := range response.Fields {
		selectorField := fmt.Sprintf("%s.fields.%s", field, name)
		if !standardFields[name] {
			v.add(IssueProfileContractInvalid, selectorField, "unknown standard field")
			continue
		}
		if selector.Pointer == "" {
			v.add(IssueMappingIncomplete, selectorField+".pointer", "a field selector requires a JSON pointer")
		} else {
			validateJSONPointer(v, selectorField+".pointer", selector.Pointer)
		}
		if selector.Transform != nil {
			if !IsKnownTransformName(selector.Transform.Name) {
				v.add(IssueProfileContractInvalid, selectorField+".transform.name", "unknown transform")
			}
			if selector.Transform.Name == TransformEnumMap && len(selector.Transform.Mapping) == 0 {
				v.add(IssueMappingIncomplete, selectorField+".transform.mapping", "enum_map requires a non-empty mapping")
			}
			if selector.Transform.Name != TransformEnumMap && len(selector.Transform.Mapping) > 0 {
				v.add(IssueProfileContractInvalid, selectorField+".transform.mapping", "only enum_map accepts a mapping")
			}
		}
	}
}

func validateCapabilities(v *profileValidator, profile Profile) {
	capabilities := profile.Capabilities
	// Iterate in a fixed order: ValidationIssues documents a stable order and
	// Error() reports the first issue, so map iteration is not allowed here.
	capabilityStates := []struct {
		field string
		state CapabilityState
	}{
		{"capabilities.metadata", capabilities.Metadata},
		{"capabilities.file_set", capabilities.FileSet},
		{"capabilities.patches", capabilities.Patches},
		{"capabilities.modes", capabilities.Modes},
		{"capabilities.commits", capabilities.Commits},
		{"capabilities.repository_id", capabilities.RepositoryID},
	}
	for _, capability := range capabilityStates {
		if !IsKnownCapabilityState(capability.state) {
			v.add(IssueProfileContractInvalid, capability.field, "unknown capability state")
		}
	}
	if capabilities.ContentAnchor != "" && !contentAnchorValues[capabilities.ContentAnchor] {
		v.add(IssueProfileContractInvalid, "capabilities.content_anchor", "must be head_sha, diff_version, or native_version")
	}
	if capabilities.Metadata == CapabilitySupported && capabilities.ContentAnchor == "" {
		v.add(IssueContentAnchorMissing, "capabilities.content_anchor", "an activated profile requires a stable content anchor")
	}
	if capabilities.Metadata == CapabilitySupported && profile.Operations.ResolveChange == nil {
		v.add(IssueMappingIncomplete, "capabilities.metadata", "metadata support requires the resolve_change operation")
	}
	if capabilities.FileSet == CapabilitySupported &&
		profile.Operations.ListFiles == nil && profile.Operations.GetDiff == nil {
		v.add(IssueMappingIncomplete, "capabilities.file_set", "file-set support requires list_files or get_diff")
	}
	if capabilities.Commits == CapabilitySupported && profile.Operations.ListCommits == nil {
		v.add(IssueMappingIncomplete, "capabilities.commits", "commit support requires the list_commits operation")
	}
	if capabilities.Patches == CapabilitySupported &&
		profile.Operations.GetDiff == nil && profile.Operations.ListFiles == nil {
		v.add(IssueMappingIncomplete, "capabilities.patches", "patch support requires get_diff or embedded file patches")
	}
	if capabilities.ContentAnchor == "head_sha" && profile.Operations.ResolveChange != nil {
		if _, mapped := profile.Operations.ResolveChange.Response.Fields["head_sha"]; !mapped {
			v.add(IssueContentAnchorMissing, "operations.resolve_change.fields.head_sha", "head_sha anchor requires the resolve_change head_sha field")
		}
	}
}

func validateLimits(v *profileValidator, limits Limits) {
	// Fixed order, same determinism rule as validateCapabilities.
	limitFields := []struct {
		field string
		value int64
	}{
		{"limits.maximum_files", limits.MaximumFiles},
		{"limits.maximum_commits", limits.MaximumCommits},
		{"limits.maximum_pages", limits.MaximumPages},
		{"limits.maximum_response_bytes", limits.MaximumResponseBytes},
	}
	for _, limit := range limitFields {
		if limit.value < 0 {
			v.add(IssueProfileContractInvalid, limit.field, "limits must not be negative")
		}
	}
}

// validateJSONPointer enforces the restricted pointer subset (design §5.5):
// RFC 6901 syntax, bounded depth and length, no traversal segments.
func validateJSONPointer(v *profileValidator, field, pointer string) {
	if !strings.HasPrefix(pointer, "/") || len(pointer) > maxJSONPointerLength {
		v.add(IssueProfileContractInvalid, field, "must be a bounded absolute JSON pointer")
		return
	}
	segments := strings.Split(pointer[1:], "/")
	if len(segments) > maxJSONPointerDepth {
		v.add(IssueProfileContractInvalid, field, "pointer is too deep")
		return
	}
	for _, segment := range segments {
		if segment == "" {
			v.add(IssueProfileContractInvalid, field, "empty pointer segments are forbidden")
			return
		}
		if segment == ".." || segment == "." {
			v.add(IssueProfileContractInvalid, field, "traversal segments are forbidden")
			return
		}
		for i := 0; i < len(segment); i++ {
			if segment[i] == '~' {
				if i+1 >= len(segment) || (segment[i+1] != '0' && segment[i+1] != '1') {
					v.add(IssueProfileContractInvalid, field, "invalid ~ escape (only ~0 and ~1 are allowed)")
					return
				}
				i++
			}
		}
	}
}

func isValidHeaderName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_|~", r):
		default:
			return false
		}
	}
	return true
}

func isLowerHex(value string) bool {
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return len(value) > 0
}
