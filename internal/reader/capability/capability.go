// Package capability is a dependency-leaf model for Agent capability declarations.
//
// It must not import concrete adapters or the parent reader package so the
// registry can aggregate adapter-exported declarations without import cycles.
package capability

// CapabilityID is a stable API identifier for a baseline Agent capability.
type CapabilityID string

const (
	CapabilityDiscovery   CapabilityID = "discovery"
	CapabilityReplay      CapabilityID = "replay"
	CapabilityRealtime    CapabilityID = "realtime"
	CapabilityTokens      CapabilityID = "tokens"
	CapabilityToolResults CapabilityID = "tool_results"
	CapabilityDiff        CapabilityID = "diff"
	CapabilitySubtasks    CapabilityID = "subtasks"
	CapabilityResume      CapabilityID = "resume"
	CapabilityDelete      CapabilityID = "delete"
	CapabilityTerminate   CapabilityID = "terminate"
)

// BaselineIDs returns the ten v0.4.0 baseline capability IDs in stable order.
func BaselineIDs() []CapabilityID {
	return []CapabilityID{
		CapabilityDiscovery,
		CapabilityReplay,
		CapabilityRealtime,
		CapabilityTokens,
		CapabilityToolResults,
		CapabilityDiff,
		CapabilitySubtasks,
		CapabilityResume,
		CapabilityDelete,
		CapabilityTerminate,
	}
}

// CapabilityState is a user-facing availability state.
type CapabilityState string

const (
	// CapabilityExact: read from structured facts, or an operation whose
	// result can be confirmed exactly.
	CapabilityExact CapabilityState = "exact"
	// CapabilityEstimated: inferred from a heuristic, time window, or
	// incomplete evidence.
	CapabilityEstimated CapabilityState = "estimated"
	// CapabilityMissing: the Agent normally records the data, but this
	// session does not contain it. Never allowed on static Agent declarations.
	CapabilityMissing CapabilityState = "missing"
	// CapabilityNotApplicable: the concept does not exist for this Agent.
	CapabilityNotApplicable CapabilityState = "not_applicable"
	// CapabilityUnsupported: the concept exists, but SI cannot currently
	// read or manage it reliably.
	CapabilityUnsupported CapabilityState = "unsupported"
)

// KnownStates lists every valid CapabilityState value.
func KnownStates() []CapabilityState {
	return []CapabilityState{
		CapabilityExact,
		CapabilityEstimated,
		CapabilityMissing,
		CapabilityNotApplicable,
		CapabilityUnsupported,
	}
}

// IsKnownState reports whether s is a defined CapabilityState.
func IsKnownState(s CapabilityState) bool {
	switch s {
	case CapabilityExact, CapabilityEstimated, CapabilityMissing,
		CapabilityNotApplicable, CapabilityUnsupported:
		return true
	default:
		return false
	}
}

// IsStaticState reports whether s is allowed on a static Agent declaration.
// missing is session-level only.
func IsStaticState(s CapabilityState) bool {
	switch s {
	case CapabilityExact, CapabilityEstimated, CapabilityNotApplicable, CapabilityUnsupported:
		return true
	default:
		return false
	}
}

// CapabilityDeclaration is one capability's static or resolved state.
type CapabilityDeclaration struct {
	// State is the availability claim for this capability.
	State CapabilityState `json:"state"`
	// ReasonCode is a stable, non-localized machine key. Required for every
	// non-exact state. Used by tests, diagnostics, and future session overrides.
	ReasonCode string `json:"reason_code,omitempty"`
	// DetailKey is an optional localization key for richer UI copy.
	// The backend does not hard-code user-facing explanations.
	DetailKey string `json:"detail_key,omitempty"`
}

// AgentCapabilities is the adapter-owned static declaration for one Agent.
//
// It is exported by each adapter package and aggregated by the registry catalog.
// Do not attach this only to an instantiated reader: settings must describe
// every supported Agent even when local storage is absent.
type AgentCapabilities struct {
	// AgentType is the stable, lowercase, non-localized identifier
	// (matches BaseSessionReader.AgentType).
	AgentType string `json:"agent_type"`
	// DisplayName is the product name shown to users.
	DisplayName string `json:"display_name"`
	// AdapterRevision increments when capability semantics, parser mappings,
	// or supported scope changes. It is not the Agent's own version.
	AdapterRevision int `json:"adapter_revision"`
	// Capabilities maps each baseline ID to its declaration. Validation
	// requires exactly the ten baseline IDs, each once.
	Capabilities map[CapabilityID]CapabilityDeclaration `json:"capabilities"`
}

// Decl is a short constructor for a CapabilityDeclaration.
func Decl(state CapabilityState, reasonCode string) CapabilityDeclaration {
	return CapabilityDeclaration{State: state, ReasonCode: reasonCode}
}

// Exact is a CapabilityDeclaration with state exact and no reason code.
func Exact() CapabilityDeclaration {
	return CapabilityDeclaration{State: CapabilityExact}
}

// Estimated is a CapabilityDeclaration with state estimated.
func Estimated(reasonCode string) CapabilityDeclaration {
	return CapabilityDeclaration{State: CapabilityEstimated, ReasonCode: reasonCode}
}

// Unsupported is a CapabilityDeclaration with state unsupported.
func Unsupported(reasonCode string) CapabilityDeclaration {
	return CapabilityDeclaration{State: CapabilityUnsupported, ReasonCode: reasonCode}
}

// NotApplicable is a CapabilityDeclaration with state not_applicable.
func NotApplicable(reasonCode string) CapabilityDeclaration {
	return CapabilityDeclaration{State: CapabilityNotApplicable, ReasonCode: reasonCode}
}
