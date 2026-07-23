package capability

import (
	"fmt"
	"sort"
	"strings"
)

// ValidationError is a deterministic, actionable contract violation.
type ValidationError struct {
	// Field is a stable path-like locator (e.g. "agent_type", "capabilities.terminate.state").
	Field string
	// Code is a stable machine key for the rule that failed.
	Code string
	// Message is a human-readable explanation suitable for tests and logs.
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s (%s)", e.Field, e.Message, e.Code)
}

// ValidationErrors is a sorted list of ValidationError values.
type ValidationErrors []ValidationError

func (errs ValidationErrors) Error() string {
	if len(errs) == 0 {
		return ""
	}
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}

// Error codes for ValidateStatic rules.
const (
	CodeEmptyAgentType         = "empty_agent_type"
	CodeInvalidAgentType       = "invalid_agent_type"
	CodeEmptyDisplayName       = "empty_display_name"
	CodeInvalidAdapterRevision = "invalid_adapter_revision"
	CodeMissingCapability      = "missing_capability"
	CodeUnknownCapability      = "unknown_capability"
	CodeDuplicateCapability    = "duplicate_capability"
	CodeUnknownState           = "unknown_state"
	CodeStaticMissingForbidden = "static_missing_forbidden"
	CodeReasonRequired         = "reason_required"
	CodeTerminateEstimated     = "terminate_estimated"
)

// ValidateStatic checks a static Agent capability declaration against the
// baseline contract. Session-level status is out of scope; missing is rejected.
//
// Errors are returned in deterministic field order so tests and diagnostics
// stay stable across runs.
func ValidateStatic(ac AgentCapabilities) ValidationErrors {
	var errs ValidationErrors

	if strings.TrimSpace(ac.AgentType) == "" {
		errs = append(errs, ValidationError{
			Field:   "agent_type",
			Code:    CodeEmptyAgentType,
			Message: "AgentType must be a non-empty stable identifier",
		})
	} else if !isNormalizedAgentType(ac.AgentType) {
		// Must already be trimmed and lowercase so AgentDefinition(lookup)
		// matches reader.AgentType() without a second normalization pass.
		errs = append(errs, ValidationError{
			Field:   "agent_type",
			Code:    CodeInvalidAgentType,
			Message: "AgentType must be trimmed lowercase (no leading/trailing space, no uppercase)",
		})
	}
	if strings.TrimSpace(ac.DisplayName) == "" {
		errs = append(errs, ValidationError{
			Field:   "display_name",
			Code:    CodeEmptyDisplayName,
			Message: "DisplayName must be non-empty",
		})
	}
	if ac.AdapterRevision < 1 {
		errs = append(errs, ValidationError{
			Field:   "adapter_revision",
			Code:    CodeInvalidAdapterRevision,
			Message: "AdapterRevision must be >= 1",
		})
	}

	if ac.Capabilities == nil {
		for _, id := range BaselineIDs() {
			errs = append(errs, ValidationError{
				Field:   "capabilities." + string(id),
				Code:    CodeMissingCapability,
				Message: fmt.Sprintf("capability %q is required exactly once", id),
			})
		}
		return sortErrors(errs)
	}

	// Unknown / extra IDs.
	var unknown []string
	for id := range ac.Capabilities {
		if !isBaselineID(id) {
			unknown = append(unknown, string(id))
		}
	}
	sort.Strings(unknown)
	for _, id := range unknown {
		errs = append(errs, ValidationError{
			Field:   "capabilities." + id,
			Code:    CodeUnknownCapability,
			Message: fmt.Sprintf("capability %q is not a baseline capability ID", id),
		})
	}

	// Exactly the ten baseline IDs, each once (map already enforces once;
	// missing keys are reported here).
	for _, id := range BaselineIDs() {
		decl, ok := ac.Capabilities[id]
		if !ok {
			errs = append(errs, ValidationError{
				Field:   "capabilities." + string(id),
				Code:    CodeMissingCapability,
				Message: fmt.Sprintf("capability %q is required exactly once", id),
			})
			continue
		}
		errs = append(errs, validateDeclaration(id, decl)...)
	}

	return sortErrors(errs)
}

func validateDeclaration(id CapabilityID, decl CapabilityDeclaration) ValidationErrors {
	var errs ValidationErrors
	field := "capabilities." + string(id)

	if !IsKnownState(decl.State) {
		errs = append(errs, ValidationError{
			Field:   field + ".state",
			Code:    CodeUnknownState,
			Message: fmt.Sprintf("state %q is not a known CapabilityState", decl.State),
		})
		// Still apply further checks where possible.
	}

	if decl.State == CapabilityMissing {
		errs = append(errs, ValidationError{
			Field:   field + ".state",
			Code:    CodeStaticMissingForbidden,
			Message: "static Agent declarations cannot use state missing (session-level only)",
		})
	}

	if decl.State != CapabilityExact && decl.State != "" {
		// Require reason for every non-exact known state, including missing
		// (rejected above) and estimated/unsupported/not_applicable.
		if strings.TrimSpace(decl.ReasonCode) == "" {
			errs = append(errs, ValidationError{
				Field:   field + ".reason_code",
				Code:    CodeReasonRequired,
				Message: fmt.Sprintf("non-exact state %q requires a non-empty ReasonCode", decl.State),
			})
		}
	}

	if id == CapabilityTerminate && decl.State == CapabilityEstimated {
		errs = append(errs, ValidationError{
			Field:   field + ".state",
			Code:    CodeTerminateEstimated,
			Message: "terminate cannot be estimated; use exact (SessionProcessFinder) or unsupported",
		})
	}

	return errs
}

func isBaselineID(id CapabilityID) bool {
	for _, b := range BaselineIDs() {
		if b == id {
			return true
		}
	}
	return false
}

// isNormalizedAgentType reports whether s is already a stable lookup key:
// non-empty after trim, equal to its trimmed form, and fully lowercase.
func isNormalizedAgentType(s string) bool {
	if s == "" || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			return false
		}
	}
	return true
}

func sortErrors(errs ValidationErrors) ValidationErrors {
	if len(errs) < 2 {
		return errs
	}
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
