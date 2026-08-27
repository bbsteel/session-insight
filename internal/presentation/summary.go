package presentation

// PublicSummary is the screenshot-free Agent catalog payload. It omits
// evidence hashes, logical filenames, session IDs, and local paths.
type PublicSummary struct {
	State                  State                        `json:"state"`
	ProfileID              string                       `json:"profile_id"`
	SchemaVersion          int                          `json:"schema_version"`
	VerifiedFeatureCount   int                          `json:"verified_feature_count"`
	ApplicableFeatureCount int                          `json:"applicable_feature_count"`
	Dimensions             map[DimensionID]DimSummary   `json:"dimensions"`
	Features               map[FeatureID]FeatureSummary `json:"features"`
	FallbackProfileID      string                       `json:"fallback_profile_id,omitempty"`
	FallbackReasonCode     string                       `json:"fallback_reason_code,omitempty"`
}

// FeatureSummary is one feature's public state.
type FeatureSummary struct {
	State      FeatureState               `json:"state"`
	ReasonCode string                     `json:"reason_code,omitempty"`
	Dimensions map[DimensionID]DimSummary `json:"dimensions"`
}

// DimSummary is one dimension's public state.
type DimSummary struct {
	State      DimensionState `json:"state"`
	ReasonCode string         `json:"reason_code,omitempty"`
}

// NewPublicSummary derives the /api/agents presentation object from a
// resolved declaration. Fingerprints belong on session detail, not here.
func NewPublicSummary(resolved Resolved) PublicSummary {
	features := make(map[FeatureID]FeatureSummary, len(resolved.Features))
	verified := 0
	applicable := 0
	for _, id := range CanonicalFeatureIDs() {
		feature := resolved.Features[id]
		dims := make(map[DimensionID]DimSummary, len(feature.Dimensions))
		for _, dimID := range FeatureDimensions(id) {
			dimension := feature.Dimensions[dimID]
			dims[dimID] = DimSummary{State: dimension.State, ReasonCode: dimension.ReasonCode}
		}
		features[id] = FeatureSummary{
			State:      feature.State,
			ReasonCode: feature.ReasonCode,
			Dimensions: dims,
		}
		if feature.State != FeatureStateNotApplicable {
			applicable++
		}
		if feature.State == FeatureStateVerified {
			verified++
		}
	}

	dimensions := map[DimensionID]DimSummary{}
	if density, ok := resolved.ProfileDimensions[DimensionDensity]; ok {
		dimensions[DimensionDensity] = DimSummary{State: density.State, ReasonCode: density.ReasonCode}
	}

	return PublicSummary{
		State:                  resolved.State,
		ProfileID:              resolved.ProfileID,
		SchemaVersion:          SchemaVersion,
		VerifiedFeatureCount:   verified,
		ApplicableFeatureCount: applicable,
		Dimensions:             dimensions,
		Features:               features,
		FallbackProfileID:      resolved.FallbackProfileID,
		FallbackReasonCode:     resolved.FallbackReasonCode,
	}
}
