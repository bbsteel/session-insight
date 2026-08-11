package changehost

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/bbsteel/session-insight/internal/model"
)

type ValidationCode string

const (
	ValidationMissingField      ValidationCode = "missing_field"
	ValidationUnknownProvider   ValidationCode = "unknown_provider"
	ValidationInvalidOrigin     ValidationCode = "invalid_origin"
	ValidationDuplicateValue    ValidationCode = "duplicate_value"
	ValidationMissingCapability ValidationCode = "missing_capability"
	ValidationUnknownCapability ValidationCode = "unknown_capability"
	ValidationInvalidState      ValidationCode = "invalid_state"
	ValidationInvalidReason     ValidationCode = "invalid_reason"
	ValidationInvalidLimit      ValidationCode = "invalid_limit"
	ValidationContractMismatch  ValidationCode = "contract_mismatch"
)

type ValidationError struct {
	Field   string         `json:"field"`
	Code    ValidationCode `json:"code"`
	Message string         `json:"message"`
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s (%s)", e.Field, e.Message, e.Code)
}

type ValidationErrors []ValidationError

func (errs ValidationErrors) Error() string {
	parts := make([]string, len(errs))
	for i, err := range errs {
		parts[i] = err.Error()
	}
	return strings.Join(parts, "; ")
}

func (errs ValidationErrors) Has(code ValidationCode) bool {
	for _, err := range errs {
		if err.Code == code {
			return true
		}
	}
	return false
}

func sortValidationErrors(errs ValidationErrors) ValidationErrors {
	sort.SliceStable(errs, func(i, j int) bool {
		if errs[i].Field != errs[j].Field {
			return errs[i].Field < errs[j].Field
		}
		if errs[i].Code != errs[j].Code {
			return errs[i].Code < errs[j].Code
		}
		return errs[i].Message < errs[j].Message
	})
	return errs
}

func validateOrigin(field, raw string) *ValidationError {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return &ValidationError{Field: field, Code: ValidationInvalidOrigin, Message: "must be an absolute HTTP(S) origin"}
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return &ValidationError{Field: field, Code: ValidationInvalidOrigin, Message: "must contain only scheme, host, and optional port"}
	}
	return nil
}

// ValidateHostIdentity checks the explicit approved-origin boundary without
// resolving DNS or granting approval.
func ValidateHostIdentity(host HostIdentity) ValidationErrors {
	var errs ValidationErrors
	if strings.TrimSpace(host.Key) == "" || strings.TrimSpace(host.Key) != host.Key {
		errs = append(errs, ValidationError{Field: "key", Code: ValidationMissingField, Message: "host key must be non-empty and trimmed"})
	}
	if !model.IsKnownChangeProviderKind(host.Provider) {
		errs = append(errs, ValidationError{Field: "provider", Code: ValidationUnknownProvider, Message: fmt.Sprintf("unknown provider %q", host.Provider)})
	}
	if issue := validateOrigin("display_origin", host.DisplayOrigin); issue != nil {
		errs = append(errs, *issue)
	}
	if len(host.EndpointOrigins) == 0 {
		errs = append(errs, ValidationError{Field: "endpoint_origins", Code: ValidationMissingField, Message: "at least one explicit endpoint origin is required"})
	}
	seen := map[string]bool{}
	displayIncluded := false
	for i, origin := range host.EndpointOrigins {
		field := fmt.Sprintf("endpoint_origins[%d]", i)
		if issue := validateOrigin(field, origin); issue != nil {
			errs = append(errs, *issue)
		}
		if seen[origin] {
			errs = append(errs, ValidationError{Field: field, Code: ValidationDuplicateValue, Message: fmt.Sprintf("duplicate endpoint origin %q", origin)})
		}
		seen[origin] = true
		if origin == host.DisplayOrigin || origin == strings.TrimSuffix(host.DisplayOrigin, "/") || strings.TrimSuffix(origin, "/") == host.DisplayOrigin {
			displayIncluded = true
		}
	}
	if host.DisplayOrigin != "" && !displayIncluded {
		errs = append(errs, ValidationError{Field: "endpoint_origins", Code: ValidationInvalidOrigin, Message: "endpoint origins must include the display origin"})
	}
	return sortValidationErrors(errs)
}

func knownCapabilityID(id CapabilityID) bool {
	for _, known := range CapabilityIDs() {
		if id == known {
			return true
		}
	}
	return false
}

func knownCapabilityReason(reason CapabilityReasonCode) bool {
	return reason == CapabilityReasonProviderUnsupported || reason == CapabilityReasonEndpointUnsupported
}

// ValidateCapabilities ensures one provider declaration contains every frozen
// operation exactly once and truthfully explains unsupported operations.
func ValidateCapabilities(c ProviderCapabilities) ValidationErrors {
	var errs ValidationErrors
	for _, id := range CapabilityIDs() {
		decl, ok := c.Operations[id]
		field := "operations." + string(id)
		if !ok {
			errs = append(errs, ValidationError{Field: field, Code: ValidationMissingCapability, Message: "capability must be declared exactly once"})
			continue
		}
		switch decl.State {
		case CapabilitySupported:
			if decl.ReasonCode != "" {
				errs = append(errs, ValidationError{Field: field + ".reason_code", Code: ValidationInvalidReason, Message: "supported capability must not carry a reason"})
			}
		case CapabilityUnsupported:
			if !knownCapabilityReason(decl.ReasonCode) {
				errs = append(errs, ValidationError{Field: field + ".reason_code", Code: ValidationInvalidReason, Message: "unsupported capability requires a declared reason"})
			}
		default:
			errs = append(errs, ValidationError{Field: field + ".state", Code: ValidationInvalidState, Message: fmt.Sprintf("unknown capability state %q", decl.State)})
		}
	}
	for id := range c.Operations {
		if !knownCapabilityID(id) {
			errs = append(errs, ValidationError{Field: "operations." + string(id), Code: ValidationUnknownCapability, Message: "capability is not part of the frozen provider contract"})
		}
	}
	if len(c.HostModes) == 0 {
		errs = append(errs, ValidationError{Field: "host_modes", Code: ValidationMissingField, Message: "at least one host mode is required"})
	}
	seenHostModes := map[HostMode]bool{}
	for i, mode := range c.HostModes {
		field := fmt.Sprintf("host_modes[%d]", i)
		if mode != HostModePublicSaaS && mode != HostModeSelfHosted {
			errs = append(errs, ValidationError{Field: field, Code: ValidationInvalidState, Message: fmt.Sprintf("unknown host mode %q", mode)})
		}
		if seenHostModes[mode] {
			errs = append(errs, ValidationError{Field: field, Code: ValidationDuplicateValue, Message: fmt.Sprintf("duplicate host mode %q", mode)})
		}
		seenHostModes[mode] = true
	}
	if len(c.AuthenticationModes) == 0 {
		errs = append(errs, ValidationError{Field: "authentication_modes", Code: ValidationMissingField, Message: "at least one authentication mode is required"})
	}
	seenAuthModes := map[AuthenticationMode]bool{}
	for i, mode := range c.AuthenticationModes {
		field := fmt.Sprintf("authentication_modes[%d]", i)
		switch mode {
		case AuthAnonymous, AuthTokenEnvironment, AuthOSKeyring, AuthProviderCLI:
		default:
			errs = append(errs, ValidationError{Field: field, Code: ValidationInvalidState, Message: fmt.Sprintf("unknown authentication mode %q", mode)})
		}
		if seenAuthModes[mode] {
			errs = append(errs, ValidationError{Field: field, Code: ValidationDuplicateValue, Message: fmt.Sprintf("duplicate authentication mode %q", mode)})
		}
		seenAuthModes[mode] = true
	}
	limits := []struct {
		field string
		value int64
	}{
		{"limits.maximum_files", c.Limits.MaximumFiles},
		{"limits.maximum_commits", c.Limits.MaximumCommits},
		{"limits.maximum_pages", c.Limits.MaximumPages},
		{"limits.maximum_response_bytes", c.Limits.MaximumResponseBytes},
	}
	for _, limit := range limits {
		if limit.value < 0 {
			errs = append(errs, ValidationError{Field: limit.field, Code: ValidationInvalidLimit, Message: "limit cannot be negative"})
		}
	}
	return sortValidationErrors(errs)
}

// ValidateResultMetadata checks the transport-neutral accounting returned by
// providers. It cannot make an incomplete result exact.
func ValidateResultMetadata(metadata ResultMetadata) ValidationErrors {
	var errs ValidationErrors
	for _, issue := range model.ValidateGitEvidenceAssessment(metadata.Assessment).Issues {
		errs = append(errs, ValidationError{Field: "assessment." + issue.Field, Code: ValidationInvalidState, Message: issue.Detail})
	}
	if metadata.PageCount < 0 {
		errs = append(errs, ValidationError{Field: "page_count", Code: ValidationInvalidLimit, Message: "count cannot be negative"})
	}
	if metadata.ItemCount < 0 {
		errs = append(errs, ValidationError{Field: "item_count", Code: ValidationInvalidLimit, Message: "count cannot be negative"})
	}
	if metadata.BytesRead < 0 {
		errs = append(errs, ValidationError{Field: "bytes_read", Code: ValidationInvalidLimit, Message: "count cannot be negative"})
	}
	if metadata.RetryAfter != nil && *metadata.RetryAfter < 0 {
		errs = append(errs, ValidationError{Field: "retry_after", Code: ValidationInvalidLimit, Message: "duration cannot be negative"})
	}
	if metadata.RateLimit != nil && metadata.RateLimit.Remaining != nil && *metadata.RateLimit.Remaining < 0 {
		errs = append(errs, ValidationError{Field: "rate_limit.remaining", Code: ValidationInvalidLimit, Message: "remaining count cannot be negative"})
	}
	return sortValidationErrors(errs)
}

// ValidateHostStatus validates the credential-safe host DTO without touching
// an auth reference or making a request.
func ValidateHostStatus(status HostStatus) ValidationErrors {
	errs := ValidateHostIdentity(status.Host)
	errs = append(errs, ValidateCapabilities(status.Capabilities)...)
	for _, issue := range model.ValidateGitEvidenceAssessment(status.Assessment).Issues {
		errs = append(errs, ValidationError{Field: "assessment." + issue.Field, Code: ValidationInvalidState, Message: issue.Detail})
	}
	switch status.ApprovalState {
	case HostPendingApproval, HostApproved, HostRevoked:
	default:
		errs = append(errs, ValidationError{Field: "approval_state", Code: ValidationInvalidState, Message: fmt.Sprintf("unknown approval state %q", status.ApprovalState)})
	}
	if status.AuthenticationMode != nil {
		declared := false
		for _, mode := range status.Capabilities.AuthenticationModes {
			if mode == *status.AuthenticationMode {
				declared = true
				break
			}
		}
		switch *status.AuthenticationMode {
		case AuthAnonymous, AuthTokenEnvironment, AuthOSKeyring, AuthProviderCLI:
		default:
			errs = append(errs, ValidationError{Field: "authentication_mode", Code: ValidationInvalidState, Message: fmt.Sprintf("unknown authentication mode %q", *status.AuthenticationMode)})
		}
		if !declared {
			errs = append(errs, ValidationError{Field: "authentication_mode", Code: ValidationContractMismatch, Message: "selected authentication mode must be declared by the provider"})
		}
	}
	if status.AuthenticationConfigured && (status.AuthenticationMode == nil || *status.AuthenticationMode == AuthAnonymous) {
		errs = append(errs, ValidationError{Field: "authentication_configured", Code: ValidationContractMismatch, Message: "configured authentication requires a non-anonymous mode"})
	}
	return sortValidationErrors(errs)
}

// ValidateHostListResponse locks the future GET host-list contract to a
// credential-safe hosts[] array with unique opaque keys.
func ValidateHostListResponse(response HostListResponse) ValidationErrors {
	var errs ValidationErrors
	if response.Hosts == nil {
		errs = append(errs, ValidationError{Field: "hosts", Code: ValidationMissingField, Message: "must be an explicit JSON array, not null"})
	}
	seen := map[string]bool{}
	for i, status := range response.Hosts {
		field := fmt.Sprintf("hosts[%d]", i)
		for _, issue := range ValidateHostStatus(status) {
			errs = append(errs, ValidationError{Field: field + "." + issue.Field, Code: issue.Code, Message: issue.Message})
		}
		if seen[status.Host.Key] {
			errs = append(errs, ValidationError{Field: field + ".host.key", Code: ValidationDuplicateValue, Message: fmt.Sprintf("duplicate host key %q", status.Host.Key)})
		}
		seen[status.Host.Key] = true
	}
	return sortValidationErrors(errs)
}

// ValidateProvider checks static provider declarations only. It calls no
// resolver/discovery/snapshot method and therefore performs no network access.
func ValidateProvider(provider Provider) ValidationErrors {
	if provider == nil {
		return ValidationErrors{{Field: "provider", Code: ValidationMissingField, Message: "provider is required"}}
	}
	errs := ValidateHostIdentity(provider.Host())
	errs = append(errs, ValidateCapabilities(provider.Capabilities())...)
	if provider.Kind() != provider.Host().Provider {
		errs = append(errs, ValidationError{Field: "provider", Code: ValidationContractMismatch, Message: "provider kind must match its approved host identity"})
	}
	return sortValidationErrors(errs)
}
