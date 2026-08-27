package presentation

// FeatureID is a canonical presentation feature. Screenshot filenames are
// evidence entries, not runtime feature IDs.
type FeatureID string

const (
	FeatureTurnBoundary       FeatureID = "turn_boundary"
	FeatureUserPrompt         FeatureID = "user_prompt"
	FeatureAssistantText      FeatureID = "assistant_text"
	FeatureThinking           FeatureID = "thinking"
	FeatureToolInvocation     FeatureID = "tool_invocation"
	FeatureToolRunning        FeatureID = "tool_running"
	FeatureToolResultSuccess  FeatureID = "tool_result_success"
	FeatureToolResultFailure  FeatureID = "tool_result_failure"
	FeatureToolResultTimeout  FeatureID = "tool_result_timeout"
	FeatureToolResultRejected FeatureID = "tool_result_rejected"
	FeatureFileChange         FeatureID = "file_change"
	FeatureSubagent           FeatureID = "subagent"
	FeatureContextBoundary    FeatureID = "context_boundary"
	FeaturePermissionRequest  FeatureID = "permission_request"
	FeatureLongOutput         FeatureID = "long_output"
	FeatureLiveStatus         FeatureID = "live_status"
	FeatureSessionCompleted   FeatureID = "session_completed"
	FeatureSessionInterrupted FeatureID = "session_interrupted"
	FeatureToolGroup          FeatureID = "tool_group"
	FeatureNestedFold         FeatureID = "nested_fold"
)

// CanonicalFeatureIDs returns the unique presentation catalog in stable order.
func CanonicalFeatureIDs() []FeatureID {
	return []FeatureID{
		FeatureTurnBoundary,
		FeatureUserPrompt,
		FeatureAssistantText,
		FeatureThinking,
		FeatureToolInvocation,
		FeatureToolRunning,
		FeatureToolResultSuccess,
		FeatureToolResultFailure,
		FeatureToolResultTimeout,
		FeatureToolResultRejected,
		FeatureFileChange,
		FeatureSubagent,
		FeatureContextBoundary,
		FeaturePermissionRequest,
		FeatureLongOutput,
		FeatureLiveStatus,
		FeatureSessionCompleted,
		FeatureSessionInterrupted,
		FeatureToolGroup,
		FeatureNestedFold,
	}
}

// IsCanonicalFeature reports whether id is in the central catalog.
func IsCanonicalFeature(id FeatureID) bool {
	for _, known := range CanonicalFeatureIDs() {
		if known == id {
			return true
		}
	}
	return false
}

// CanonicalDimensionIDs returns every presentation dimension in stable order.
func CanonicalDimensionIDs() []DimensionID {
	return []DimensionID{
		DimensionLayout,
		DimensionSkinDark,
		DimensionSkinLight,
		DimensionDensity,
		DimensionFold,
	}
}

// FeatureDimensions returns the dimensions a feature must declare. Density is
// a profile-level dimension and is never listed here. Missing a listed
// dimension is a validation error; extra dimensions are also an error.
func FeatureDimensions(id FeatureID) []DimensionID {
	dims := []DimensionID{DimensionLayout, DimensionSkinDark, DimensionSkinLight}
	if featureSupportsFold(id) {
		dims = append(dims, DimensionFold)
	}
	return dims
}

// ProfileDimensionIDs returns the dimensions declared on the profile itself.
func ProfileDimensionIDs() []DimensionID {
	return []DimensionID{DimensionDensity}
}

func featureSupportsFold(id FeatureID) bool {
	switch id {
	case FeatureThinking, FeatureToolInvocation, FeatureFileChange,
		FeatureSubagent, FeatureContextBoundary, FeatureLongOutput,
		FeatureToolGroup, FeatureNestedFold:
		return true
	default:
		return false
	}
}

// IsKnownDimension reports whether id is a presentation dimension.
func IsKnownDimension(id DimensionID) bool {
	switch id {
	case DimensionLayout, DimensionSkinDark, DimensionSkinLight,
		DimensionDensity, DimensionFold:
		return true
	default:
		return false
	}
}
