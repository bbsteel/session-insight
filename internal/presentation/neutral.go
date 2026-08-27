package presentation

// NeutralDeclaration returns the complete independently runnable fallback
// declaration (profile ID neutral.v1). Every catalog feature and the
// profile-level density dimension is explicit and neutral.
func NeutralDeclaration() Declaration {
	return NeutralDeclarationFor("neutral", ProfileNeutralV1)
}

// NativeNeutralDeclaration returns an all-neutral declaration for a native
// Agent. The profile ID is "<agent>.native.v1"; fallback remains neutral.v1.
func NativeNeutralDeclaration(agentType string) Declaration {
	return NeutralDeclarationFor(agentType, agentType+".native.v1")
}

// NonNativeDeclaration returns the fixed imported/non-native declaration:
// all-neutral, profile ID neutral.v1, reason presentation_non_native_source.
func NonNativeDeclaration(agentType string) Declaration {
	decl := NeutralDeclarationFor(agentType, ProfileNeutralV1)
	applyReason(&decl, ReasonNonNativeSource)
	return decl
}

// NeutralDeclarationFor builds a complete all-neutral declaration. Every
// applicable dimension is present so omission cannot mean "not applicable".
func NeutralDeclarationFor(agentType, profileID string) Declaration {
	features := make(map[FeatureID]FeatureDeclaration, len(CanonicalFeatureIDs()))
	for _, featureID := range CanonicalFeatureIDs() {
		dims := FeatureDimensions(featureID)
		dimensions := make(map[DimensionID]DimensionDeclaration, len(dims))
		for _, dimensionID := range dims {
			dimensions[dimensionID] = DimensionDeclaration{
				Mode:       DimensionModeNeutral,
				ReasonCode: ReasonEvidenceMissing,
			}
		}
		features[featureID] = FeatureDeclaration{
			Mode:       FeatureModeNeutral,
			Dimensions: dimensions,
			ReasonCode: ReasonEvidenceMissing,
		}
	}
	return Declaration{
		SchemaVersion:     SchemaVersion,
		AgentType:         agentType,
		ProfileID:         profileID,
		FallbackProfileID: ProfileNeutralV1,
		Features:          features,
		ProfileDimensions: map[DimensionID]DimensionDeclaration{
			DimensionDensity: {
				Mode:       DimensionModeNeutral,
				ReasonCode: ReasonEvidenceMissing,
			},
		},
	}
}

func applyReason(decl *Declaration, reason string) {
	for featureID, feature := range decl.Features {
		feature.ReasonCode = reason
		if feature.Dimensions != nil {
			for dimensionID, dimension := range feature.Dimensions {
				dimension.ReasonCode = reason
				feature.Dimensions[dimensionID] = dimension
			}
		}
		decl.Features[featureID] = feature
	}
	for dimensionID, dimension := range decl.ProfileDimensions {
		dimension.ReasonCode = reason
		decl.ProfileDimensions[dimensionID] = dimension
	}
}
