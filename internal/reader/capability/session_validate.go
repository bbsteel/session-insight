package capability

import (
	"fmt"
	"sort"
	"strings"
)

// Additional validation codes for resolved session capability data.
const (
	CodeIllegalPromotion     = "illegal_promotion"
	CodeMissingReason        = "missing_reason"
	CodeAgentTypeMismatch    = "agent_type_mismatch"
	CodeAdapterRevMismatch   = "adapter_revision_mismatch"
	CodeUnknownAction        = "unknown_action"
	CodeActionNotActionable  = "action_not_actionable"
	CodeUnknownAvailability  = "unknown_availability"
	CodeLivenessUnknownState = "liveness_unknown_state"
	CodeStatusMapNil         = "status_map_nil"
)

// ValidateResolved checks a per-session capability resolution against the
// static Agent declaration. It never mutates either argument.
//
// Rules (summary):
//   - all ten baseline capabilities exactly once;
//   - AgentType / AdapterRevision match static;
//   - known states only; every non-exact needs a reason code;
//   - static unsupported / not_applicable cannot be promoted;
//   - estimated cannot promote to exact;
//   - missing is only legal at session level and always needs a reason;
//   - actions only for resume / delete / terminate with known availability.
func ValidateResolved(resolved SessionCapabilities, static AgentCapabilities) ValidationErrors {
	var errs ValidationErrors

	if strings.TrimSpace(resolved.AgentType) == "" {
		errs = append(errs, ValidationError{
			Field:   "agent_type",
			Code:    CodeEmptyAgentType,
			Message: "AgentType must be non-empty",
		})
	} else if resolved.AgentType != static.AgentType {
		errs = append(errs, ValidationError{
			Field:   "agent_type",
			Code:    CodeAgentTypeMismatch,
			Message: fmt.Sprintf("resolved AgentType %q != static %q", resolved.AgentType, static.AgentType),
		})
	}
	if resolved.AdapterRevision != static.AdapterRevision {
		errs = append(errs, ValidationError{
			Field:   "adapter_revision",
			Code:    CodeAdapterRevMismatch,
			Message: fmt.Sprintf("resolved AdapterRevision %d != static %d", resolved.AdapterRevision, static.AdapterRevision),
		})
	}

	if resolved.Status == nil {
		errs = append(errs, ValidationError{
			Field:   "status",
			Code:    CodeStatusMapNil,
			Message: "status map is required",
		})
		// Still report missing baseline keys below via empty lookup.
	}

	// Unknown status keys.
	if resolved.Status != nil {
		var unknown []string
		for id := range resolved.Status {
			if !isBaselineID(id) {
				unknown = append(unknown, string(id))
			}
		}
		// sort via existing pattern — reuse BaselineIDs loop after.
		for _, id := range sortedStrings(unknown) {
			errs = append(errs, ValidationError{
				Field:   "status." + id,
				Code:    CodeUnknownCapability,
				Message: fmt.Sprintf("capability %q is not a baseline capability ID", id),
			})
		}
	}

	for _, id := range BaselineIDs() {
		st, ok := resolved.Status[id]
		field := "status." + string(id)
		if !ok {
			errs = append(errs, ValidationError{
				Field:   field,
				Code:    CodeMissingCapability,
				Message: fmt.Sprintf("capability %q is required exactly once", id),
			})
			continue
		}
		errs = append(errs, validateResolvedStatus(id, st, static.Capabilities[id])...)
	}

	// Actions.
	for id, act := range resolved.Actions {
		field := "actions." + string(id)
		if !IsActionable(id) {
			errs = append(errs, ValidationError{
				Field:   field,
				Code:    CodeActionNotActionable,
				Message: fmt.Sprintf("capability %q cannot have an action entry", id),
			})
			continue
		}
		if !isKnownAvailability(act.Availability) {
			errs = append(errs, ValidationError{
				Field:   field + ".availability",
				Code:    CodeUnknownAvailability,
				Message: fmt.Sprintf("availability %q is not known", act.Availability),
			})
		}
		if act.Availability != ActionAvailable && strings.TrimSpace(act.ReasonCode) == "" {
			// runtime_check_required always has a reason from helpers; require for unavailable too.
			errs = append(errs, ValidationError{
				Field:   field + ".reason_code",
				Code:    CodeReasonRequired,
				Message: fmt.Sprintf("non-available action %q requires ReasonCode", act.Availability),
			})
		}
	}

	// Liveness.
	if !IsKnownState(resolved.Liveness.State) {
		errs = append(errs, ValidationError{
			Field:   "liveness.state",
			Code:    CodeLivenessUnknownState,
			Message: fmt.Sprintf("liveness state %q is not known", resolved.Liveness.State),
		})
	} else if resolved.Liveness.State != CapabilityExact && strings.TrimSpace(resolved.Liveness.ReasonCode) == "" {
		errs = append(errs, ValidationError{
			Field:   "liveness.reason_code",
			Code:    CodeReasonRequired,
			Message: "non-exact liveness requires ReasonCode",
		})
	}

	return sortErrors(errs)
}

func validateResolvedStatus(id CapabilityID, st SessionCapabilityStatus, staticDecl CapabilityDeclaration) ValidationErrors {
	var errs ValidationErrors
	field := "status." + string(id)

	if !IsKnownState(st.State) {
		errs = append(errs, ValidationError{
			Field:   field + ".state",
			Code:    CodeUnknownState,
			Message: fmt.Sprintf("state %q is not a known CapabilityState", st.State),
		})
	}

	if st.State != CapabilityExact && strings.TrimSpace(st.ReasonCode) == "" {
		errs = append(errs, ValidationError{
			Field:   field + ".reason_code",
			Code:    CodeReasonRequired,
			Message: fmt.Sprintf("non-exact state %q requires a non-empty ReasonCode", st.State),
		})
	}

	// Static unsupported / not_applicable must not be promoted.
	switch staticDecl.State {
	case CapabilityUnsupported:
		if st.State != CapabilityUnsupported {
			errs = append(errs, ValidationError{
				Field:   field + ".state",
				Code:    CodeIllegalPromotion,
				Message: fmt.Sprintf("static unsupported cannot resolve to %q", st.State),
			})
		}
	case CapabilityNotApplicable:
		if st.State != CapabilityNotApplicable {
			errs = append(errs, ValidationError{
				Field:   field + ".state",
				Code:    CodeIllegalPromotion,
				Message: fmt.Sprintf("static not_applicable cannot resolve to %q", st.State),
			})
		}
	case CapabilityEstimated:
		// May stay estimated or downgrade to missing; cannot become exact.
		if st.State == CapabilityExact {
			errs = append(errs, ValidationError{
				Field:   field + ".state",
				Code:    CodeIllegalPromotion,
				Message: "static estimated cannot promote to exact",
			})
		}
		if st.State == CapabilityUnsupported || st.State == CapabilityNotApplicable {
			errs = append(errs, ValidationError{
				Field:   field + ".state",
				Code:    CodeIllegalPromotion,
				Message: fmt.Sprintf("static estimated cannot resolve to %q", st.State),
			})
		}
	case CapabilityExact:
		// May stay exact, or downgrade to estimated / missing.
		if st.State == CapabilityUnsupported || st.State == CapabilityNotApplicable {
			errs = append(errs, ValidationError{
				Field:   field + ".state",
				Code:    CodeIllegalPromotion,
				Message: fmt.Sprintf("static exact cannot resolve to %q", st.State),
			})
		}
	}

	if id == CapabilityTerminate && st.State == CapabilityEstimated {
		errs = append(errs, ValidationError{
			Field:   field + ".state",
			Code:    CodeTerminateEstimated,
			Message: "terminate cannot be estimated",
		})
	}

	return errs
}

func isKnownAvailability(a ActionAvailability) bool {
	switch a {
	case ActionAvailable, ActionUnavailable, ActionCheckRequired:
		return true
	default:
		return false
	}
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
