package changehost

import (
	"context"
	"fmt"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// HostIdentity is one approved logical change host. EndpointOrigins is the
// complete, explicit allowlist (for example github.com and api.github.com);
// providers cannot add origins after approval.
type HostIdentity struct {
	Key             string                   `json:"key"`
	Provider        model.ChangeProviderKind `json:"provider"`
	DisplayOrigin   string                   `json:"display_origin"`
	EndpointOrigins []string                 `json:"endpoint_origins"`
}

type HostApprovalState string

const (
	HostPendingApproval HostApprovalState = "pending_approval"
	HostApproved        HostApprovalState = "approved"
	HostRevoked         HostApprovalState = "revoked"
)

type HostMode string

const (
	HostModePublicSaaS HostMode = "public_saas"
	HostModeSelfHosted HostMode = "self_hosted"
)

type AuthenticationMode string

const (
	AuthAnonymous        AuthenticationMode = "anonymous"
	AuthTokenEnvironment AuthenticationMode = "token_environment"
	AuthOSKeyring        AuthenticationMode = "os_keyring"
	AuthProviderCLI      AuthenticationMode = "provider_cli"
)

// CapabilityID is a closed provider operation/dimension vocabulary. Provider
// declarations are the runtime source of truth; clients must not maintain a
// second provider matrix.
type CapabilityID string

const (
	CapabilityParseReference    CapabilityID = "parse_reference"
	CapabilityParseRemote       CapabilityID = "parse_remote"
	CapabilityResolveRepository CapabilityID = "resolve_repository"
	CapabilityResolveChange     CapabilityID = "resolve_change_request"
	CapabilityDiscoverHead      CapabilityID = "discover_for_head"
	CapabilityDiscoverCommit    CapabilityID = "discover_for_commit"
	CapabilitySnapshotMetadata  CapabilityID = "snapshot_metadata"
	CapabilitySnapshotFileSet   CapabilityID = "snapshot_file_set"
	CapabilitySnapshotPatches   CapabilityID = "snapshot_patches"
	CapabilitySnapshotModes     CapabilityID = "snapshot_modes"
	CapabilitySnapshotCommits   CapabilityID = "snapshot_commits"
)

// CapabilityIDs returns the frozen IDs in stable display/validation order.
func CapabilityIDs() []CapabilityID {
	return []CapabilityID{
		CapabilityParseReference,
		CapabilityParseRemote,
		CapabilityResolveRepository,
		CapabilityResolveChange,
		CapabilityDiscoverHead,
		CapabilityDiscoverCommit,
		CapabilitySnapshotMetadata,
		CapabilitySnapshotFileSet,
		CapabilitySnapshotPatches,
		CapabilitySnapshotModes,
		CapabilitySnapshotCommits,
	}
}

type CapabilityState string

const (
	CapabilitySupported   CapabilityState = "supported"
	CapabilityUnsupported CapabilityState = "unsupported"
)

type CapabilityReasonCode string

const (
	CapabilityReasonProviderUnsupported CapabilityReasonCode = "provider_unsupported"
	CapabilityReasonEndpointUnsupported CapabilityReasonCode = "endpoint_unsupported"
)

type CapabilityDeclaration struct {
	State      CapabilityState      `json:"state"`
	ReasonCode CapabilityReasonCode `json:"reason_code,omitempty"`
}

type ProviderLimits struct {
	MaximumFiles         int64 `json:"maximum_files,omitempty"`
	MaximumCommits       int64 `json:"maximum_commits,omitempty"`
	MaximumPages         int64 `json:"maximum_pages,omitempty"`
	MaximumResponseBytes int64 `json:"maximum_response_bytes,omitempty"`
	ReportsOverflow      bool  `json:"reports_overflow"`
}

// ProviderCapabilities freezes provider-owned discovery, authentication and
// hosted-snapshot declarations. Limits are provider-native maxima, not the
// independent SessionInsight safety caps enforced by the HTTP client.
type ProviderCapabilities struct {
	Operations          map[CapabilityID]CapabilityDeclaration `json:"operations"`
	HostModes           []HostMode                             `json:"host_modes"`
	AuthenticationModes []AuthenticationMode                   `json:"authentication_modes"`
	Limits              ProviderLimits                         `json:"limits"`
}

// HostStatus is the credential-safe DTO used by future host list/status APIs.
// It exposes only whether an authentication reference exists, never the
// reference name, token, cookie, or Authorization header.
type HostStatus struct {
	Host                     HostIdentity          `json:"host"`
	ApprovalState            HostApprovalState     `json:"approval_state"`
	AuthenticationMode       *AuthenticationMode   `json:"authentication_mode,omitempty"`
	AuthenticationConfigured bool                  `json:"authentication_configured"`
	Capabilities             ProviderCapabilities  `json:"capabilities"`
	Assessment               GitEvidenceAssessment `json:"assessment"`
	LastCheckedAt            *time.Time            `json:"last_checked_at,omitempty"`
}

// HostListResponse freezes the later GET list shape and guarantees a top-level
// hosts[] collection rather than a provider-keyed capability matrix.
type HostListResponse struct {
	Hosts []HostStatus `json:"hosts"`
}

// ReferenceParser performs local parsing only. It is safe to use before host
// approval and must return sanitized provisional references, never canonical
// provider identities.
type ReferenceParser interface {
	Kind() model.ChangeProviderKind
	ParseReference(raw string) (model.ChangeRequestReference, bool)
	ParseRemote(raw string) (model.HostedRepositoryReference, bool)
}

// Provider is a read-only adapter bound to one approved HostIdentity. Its
// implementation receives a separately hardened client in Phase 1; this
// interface deliberately exposes no mutation operation.
type Provider interface {
	ReferenceParser
	Host() HostIdentity
	Capabilities() ProviderCapabilities
	ResolveRepository(ctx context.Context, ref model.HostedRepositoryReference) (RepositoryResult, error)
	Resolve(ctx context.Context, ref model.ChangeRequestReference) (ResolveResult, error)
	DiscoverForHead(ctx context.Context, sourceRepo, targetRepo model.HostedRepositoryIdentity, branch, headSHA string) (DiscoveryResult, error)
	DiscoverForCommit(ctx context.Context, repo model.HostedRepositoryIdentity, sha string) (DiscoveryResult, error)
	GetSnapshot(ctx context.Context, key model.ChangeRequestIdentity, requestedVersion model.ContentVersionKey) (SnapshotResult, error)
}

type ResultMetadata struct {
	Assessment        GitEvidenceAssessment `json:"assessment"`
	PageCount         int                   `json:"page_count"`
	ItemCount         int                   `json:"item_count"`
	BytesRead         int64                 `json:"bytes_read"`
	RetryAfterSeconds *int64                `json:"retry_after_seconds,omitempty"`
	RateLimit         *RateLimit            `json:"rate_limit,omitempty"`
}

// GitEvidenceAssessment aliases the shared model type so provider result
// metadata and persisted Git evidence cannot drift into parallel vocabularies.
type GitEvidenceAssessment = model.GitEvidenceAssessment

type RateLimit struct {
	Remaining *int       `json:"remaining,omitempty"`
	ResetAt   *time.Time `json:"reset_at,omitempty"`
}

type RepositoryResult struct {
	Repository model.HostedRepositoryIdentity `json:"repository"`
	Metadata   ResultMetadata                 `json:"metadata"`
}

type ResolveResult struct {
	Change   model.ChangeRequestSummary `json:"change"`
	Metadata ResultMetadata             `json:"metadata"`
}

type DiscoveryMatch string

const (
	DiscoveryHeadSHA          DiscoveryMatch = "head_sha"
	DiscoveryCommitMembership DiscoveryMatch = "commit_membership"
	DiscoveryBranch           DiscoveryMatch = "branch"
)

type DiscoveryCandidate struct {
	Change     model.ChangeRequestSummary  `json:"change"`
	Match      DiscoveryMatch              `json:"match"`
	Assessment model.GitEvidenceAssessment `json:"assessment"`
}

type DiscoveryResult struct {
	Candidates []DiscoveryCandidate `json:"candidates"`
	Metadata   ResultMetadata       `json:"metadata"`
}

type SnapshotResult struct {
	Snapshot model.ChangeRequestSnapshot `json:"snapshot"`
	Metadata ResultMetadata              `json:"metadata"`
}

type Operation string

const (
	OperationResolveRepository Operation = "resolve_repository"
	OperationResolveChange     Operation = "resolve_change_request"
	OperationDiscoverHead      Operation = "discover_for_head"
	OperationDiscoverCommit    Operation = "discover_for_commit"
	OperationGetSnapshot       Operation = "get_snapshot"
)

func IsKnownOperation(operation Operation) bool {
	switch operation {
	case OperationResolveRepository, OperationResolveChange, OperationDiscoverHead,
		OperationDiscoverCommit, OperationGetSnapshot:
		return true
	default:
		return false
	}
}

type ErrorCode string

const (
	ErrorHostNotApproved ErrorCode = "host_not_approved"
	ErrorHostRevoked     ErrorCode = "host_revoked"
	ErrorAuthRequired    ErrorCode = "auth_required"
	ErrorUnsupported     ErrorCode = "unsupported"
	ErrorNotFound        ErrorCode = "not_found"
	ErrorPartial         ErrorCode = "partial"
	ErrorOverflow        ErrorCode = "overflow"
	ErrorRateLimited     ErrorCode = "rate_limited"
	ErrorCaptureRaced    ErrorCode = "capture_raced"
	ErrorInvalidResponse ErrorCode = "invalid_response"
	ErrorUnavailable     ErrorCode = "unavailable"
)

func IsKnownErrorCode(code ErrorCode) bool {
	switch code {
	case ErrorHostNotApproved, ErrorHostRevoked, ErrorAuthRequired,
		ErrorUnsupported, ErrorNotFound, ErrorPartial, ErrorOverflow,
		ErrorRateLimited, ErrorCaptureRaced, ErrorInvalidResponse,
		ErrorUnavailable:
		return true
	default:
		return false
	}
}

// Error is safe to surface as a typed provider failure. Error() intentionally
// excludes Cause so raw URLs, response bodies, credentials and provider error
// text cannot leak through normal API/log formatting. Cause remains available
// to trusted internal diagnostics via errors.Unwrap.
type Error struct {
	Code       ErrorCode
	Operation  Operation
	RetryAfter time.Duration
	Cause      error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("change host %s failed: %s", e.Operation, e.Code)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// EvidenceReason maps provider failures onto the stable Git evidence reason
// vocabulary without exposing provider response text.
func (e *Error) EvidenceReason() model.GitEvidenceReasonCode {
	if e == nil {
		return model.ReasonChangeRequestPartial
	}
	switch e.Code {
	case ErrorHostNotApproved:
		return model.ReasonChangeHostNotApproved
	case ErrorHostRevoked:
		return model.ReasonChangeHostRevoked
	case ErrorAuthRequired:
		return model.ReasonChangeHostAuthRequired
	case ErrorUnsupported:
		return model.ReasonChangeProviderUnsupported
	case ErrorNotFound:
		return model.ReasonChangeRequestNotFound
	case ErrorOverflow:
		return model.ReasonChangeRequestOverflow
	case ErrorCaptureRaced:
		return model.ReasonChangeRequestCaptureRaced
	default:
		return model.ReasonChangeRequestPartial
	}
}
