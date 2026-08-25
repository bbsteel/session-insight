// Package openapi defines the declarative Provider Profile contract used to
// adapt previously unknown change hosts through their Swagger/OpenAPI
// documents. A profile is data only: it contains no executable code, and each
// activated revision is immutable so historical snapshots keep the exact
// mapping that produced them.
package openapi

import (
	"encoding/json"
	"fmt"
	"time"
)

// SchemaVersion is the only profile schema version this build can validate
// and execute.
const SchemaVersion = 1

// AdapterKind is the fixed adapter identifier for OpenAPI-derived profiles.
// It matches model.ChangeProviderOpenAPI; the constant is kept local so this
// package does not depend on provider runtime code.
const AdapterKind = "openapi"

// ProfileLifecycle is the profile state machine from the V1 design:
// draft -> verified -> active -> degraded, with invalid and revoked as
// terminal side states.
type ProfileLifecycle string

const (
	ProfileDraft    ProfileLifecycle = "draft"
	ProfileVerified ProfileLifecycle = "verified"
	ProfileActive   ProfileLifecycle = "active"
	ProfileDegraded ProfileLifecycle = "degraded"
	ProfileInvalid  ProfileLifecycle = "invalid"
	ProfileRevoked  ProfileLifecycle = "revoked"
)

// IsKnownProfileLifecycle reports whether lifecycle is part of the frozen
// profile state machine vocabulary.
func IsKnownProfileLifecycle(lifecycle ProfileLifecycle) bool {
	switch lifecycle {
	case ProfileDraft, ProfileVerified, ProfileActive,
		ProfileDegraded, ProfileInvalid, ProfileRevoked:
		return true
	default:
		return false
	}
}

// OperationID is the closed vocabulary of read operations a profile may
// declare. resolve_change is required for activation; the rest degrade
// independently when absent.
type OperationID string

const (
	OperationResolveChange     OperationID = "resolve_change"
	OperationResolveRepository OperationID = "resolve_repository"
	OperationListFiles         OperationID = "list_files"
	OperationListCommits       OperationID = "list_commits"
	OperationGetDiff           OperationID = "get_diff"
)

// OperationIDs returns the frozen operation IDs in stable order.
func OperationIDs() []OperationID {
	return []OperationID{
		OperationResolveChange,
		OperationResolveRepository,
		OperationListFiles,
		OperationListCommits,
		OperationGetDiff,
	}
}

// IsKnownOperationID reports whether id is part of the closed operation
// vocabulary.
func IsKnownOperationID(id OperationID) bool {
	switch id {
	case OperationResolveChange, OperationResolveRepository,
		OperationListFiles, OperationListCommits, OperationGetDiff:
		return true
	default:
		return false
	}
}

// PaginationMode is the closed set of pagination strategies a profile may
// declare. Every mode stays bounded by the limits block below.
type PaginationMode string

const (
	PaginationNone         PaginationMode = "none"
	PaginationPageNumber   PaginationMode = "page_number"
	PaginationLinkHeader   PaginationMode = "link_header"
	PaginationCursorBody   PaginationMode = "cursor_body"
	PaginationCursorHeader PaginationMode = "cursor_header"
)

// IsKnownPaginationMode reports whether mode is part of the closed pagination
// vocabulary.
func IsKnownPaginationMode(mode PaginationMode) bool {
	switch mode {
	case PaginationNone, PaginationPageNumber, PaginationLinkHeader,
		PaginationCursorBody, PaginationCursorHeader:
		return true
	default:
		return false
	}
}

// TransformName is the closed set of fixed value conversions a field selector
// may apply. Transforms are described by structured arguments only; expression
// strings and scripts are never accepted.
type TransformName string

const (
	TransformString         TransformName = "string"
	TransformIntegerToStr   TransformName = "integer_to_string"
	TransformBoolean        TransformName = "boolean"
	TransformRFC3339Time    TransformName = "rfc3339_time"
	TransformUnixTime       TransformName = "unix_time"
	TransformLowercase      TransformName = "lowercase"
	TransformCoalesce       TransformName = "coalesce"
	TransformEnumMap        TransformName = "enum_map"
	TransformGitSHA         TransformName = "git_sha"
	TransformRepositorySlug TransformName = "repository_slug"
	TransformChangeStatus   TransformName = "change_status"
	TransformFileStatus     TransformName = "file_status"
)

// IsKnownTransformName reports whether name is part of the closed transform
// vocabulary.
func IsKnownTransformName(name TransformName) bool {
	switch name {
	case TransformString, TransformIntegerToStr, TransformBoolean,
		TransformRFC3339Time, TransformUnixTime, TransformLowercase,
		TransformCoalesce, TransformEnumMap, TransformGitSHA,
		TransformRepositorySlug, TransformChangeStatus, TransformFileStatus:
		return true
	default:
		return false
	}
}

// Profile is the top-level declarative adapter configuration (design §5.1).
// CredentialReference points at a secret but never contains one.
type Profile struct {
	SchemaVersion   int               `json:"schema_version"`
	ProfileID       string            `json:"profile_id"`
	ProfileRevision int               `json:"profile_revision"`
	DisplayName     string            `json:"display_name"`
	Adapter         string            `json:"adapter"`
	HostID          string            `json:"host_id"`
	DisplayOrigin   string            `json:"display_origin"`
	EndpointOrigins []string          `json:"endpoint_origins"`
	Reference       ReferenceTemplate `json:"reference"`
	Authentication  Authentication    `json:"authentication"`
	Operations      ProfileOperations `json:"operations"`
	Capabilities    Capabilities      `json:"capabilities"`
	Limits          Limits            `json:"limits"`
	SpecDigest      string            `json:"spec_digest"`
	VerifiedAt      *time.Time        `json:"verified_at,omitempty"`
}

// ReferenceTemplate binds web change URLs of one host to the parameters later
// operations consume (design §5.2).
type ReferenceTemplate struct {
	Origin              string `json:"origin"`
	PathTemplate        string `json:"path_template"`
	RepositoryParameter string `json:"repository_parameter"`
	NumberParameter     string `json:"number_parameter"`
}

// Authentication describes how the resolved secret is placed on a request.
// The credential source returns only the raw secret; the verified profile
// alone decides the header name and value prefix (design §5.3).
type Authentication struct {
	Scheme              string `json:"scheme"`
	HeaderName          string `json:"header_name"`
	ValuePrefix         string `json:"value_prefix"`
	CredentialReference string `json:"credential_reference"`
}

// ProfileOperations groups the declared operations by their frozen ID. An
// absent operation means the corresponding capability is unsupported, never
// that it should be guessed.
type ProfileOperations struct {
	ResolveChange     *Operation `json:"resolve_change,omitempty"`
	ResolveRepository *Operation `json:"resolve_repository,omitempty"`
	ListFiles         *Operation `json:"list_files,omitempty"`
	ListCommits       *Operation `json:"list_commits,omitempty"`
	GetDiff           *Operation `json:"get_diff,omitempty"`
}

// ForID returns the operation declared for id, or nil when unsupported.
func (o ProfileOperations) ForID(id OperationID) *Operation {
	switch id {
	case OperationResolveChange:
		return o.ResolveChange
	case OperationResolveRepository:
		return o.ResolveRepository
	case OperationListFiles:
		return o.ListFiles
	case OperationListCommits:
		return o.ListCommits
	case OperationGetDiff:
		return o.GetDiff
	default:
		return nil
	}
}

// Operation is one read-only HTTP call template (design §5.4). Parameters may
// only bind reference parameters, stable identity outputs of earlier verified
// operations, fixed non-sensitive profile values, or paginator state.
type Operation struct {
	Method       string            `json:"method"`
	Origin       string            `json:"origin"`
	PathTemplate string            `json:"path_template"`
	Parameters   map[string]string `json:"parameters"`
	Headers      map[string]string `json:"headers"`
	Pagination   Pagination        `json:"pagination"`
	Response     OperationResponse `json:"response"`
}

// Pagination declares how an operation enumerates pages (design §5.6). Only
// the fields relevant to the selected mode may be set.
type Pagination struct {
	Mode              PaginationMode `json:"mode"`
	PageParameter     string         `json:"page_parameter,omitempty"`
	PerPageParameter  string         `json:"per_page_parameter,omitempty"`
	CursorParameter   string         `json:"cursor_parameter,omitempty"`
	NextCursorPointer string         `json:"next_cursor_pointer,omitempty"`
	NextCursorHeader  string         `json:"next_cursor_header,omitempty"`
	TotalPagesPointer string         `json:"total_pages_pointer,omitempty"`
}

// OperationResponse locates the payload within a response body and maps its
// fields. ItemPointer selects a single object ("" = body root); ItemsPointer
// selects the array of a list response, with each field selector then
// evaluated relative to one element (design §5.5).
type OperationResponse struct {
	ItemPointer  string                   `json:"item_pointer"`
	ItemsPointer string                   `json:"items_pointer,omitempty"`
	Fields       map[string]FieldSelector `json:"fields"`
}

// FieldSelector is a restricted JSON Pointer plus an optional fixed transform.
type FieldSelector struct {
	Pointer   string          `json:"pointer"`
	Transform *FieldTransform `json:"transform,omitempty"`
}

// FieldTransform names one fixed conversion and its structured arguments.
// enum_map uses Mapping; coalesce ignores arguments and relies on the runtime
// trying the pointer chain the profile declared.
type FieldTransform struct {
	Name    TransformName     `json:"name"`
	Mapping map[string]string `json:"mapping,omitempty"`
}

// Capabilities is the runtime capability declaration derived from the
// verified profile. It is the single source of truth the frontend consumes;
// no per-platform matrix may exist anywhere else.
type Capabilities struct {
	Metadata      CapabilityState `json:"metadata"`
	FileSet       CapabilityState `json:"file_set"`
	Patches       CapabilityState `json:"patches"`
	Modes         CapabilityState `json:"modes"`
	Commits       CapabilityState `json:"commits"`
	ContentAnchor string          `json:"content_anchor"`
	RepositoryID  CapabilityState `json:"repository_id"`
}

// CapabilityState mirrors the supported/unsupported vocabulary without
// importing the provider runtime package.
type CapabilityState string

const (
	CapabilitySupported   CapabilityState = "supported"
	CapabilityUnsupported CapabilityState = "unsupported"
)

// IsKnownCapabilityState reports whether state is part of the capability
// vocabulary.
func IsKnownCapabilityState(state CapabilityState) bool {
	return state == CapabilitySupported || state == CapabilityUnsupported
}

// Limits are the provider-native maxima the profile declares. The independent
// SessionInsight safety caps always take precedence (design §9.3).
type Limits struct {
	MaximumFiles         int64 `json:"maximum_files,omitempty"`
	MaximumCommits       int64 `json:"maximum_commits,omitempty"`
	MaximumPages         int64 `json:"maximum_pages,omitempty"`
	MaximumResponseBytes int64 `json:"maximum_response_bytes,omitempty"`
}

// EncodeProfile serializes a validated profile in its canonical form. Profiles
// are immutable once activated, so callers encode first and store the exact
// bytes they validated.
func EncodeProfile(profile Profile) ([]byte, error) {
	encoded, err := json.Marshal(profile)
	if err != nil {
		return nil, fmt.Errorf("encode provider profile: %w", err)
	}
	return encoded, nil
}

// DecodeProfile parses profile JSON without approving it. Callers must still
// run ValidateProfile before the value can influence any runtime decision.
func DecodeProfile(raw []byte) (Profile, error) {
	var profile Profile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return Profile{}, fmt.Errorf("decode provider profile: %w", err)
	}
	return profile, nil
}
