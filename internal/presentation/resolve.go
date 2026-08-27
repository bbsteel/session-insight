package presentation

// Resolve validates decl, merges every feature and dimension from the
// independently runnable neutral.v1 baseline, and computes states plus
// fingerprints. Invalid declarations do not run as partial: they fall back
// to neutral.v1 with presentation_declaration_invalid.
//
// Resolve does not read session content, screenshots, or Agent lookup tables.
func Resolve(decl Declaration) Resolved {
	if errs := Validate(decl); len(errs) != 0 {
		resolved := resolveValid(NeutralDeclaration())
		resolved.FallbackReasonCode = ReasonDeclarationInvalid
		return resolved
	}
	return resolveValid(decl)
}

func resolveValid(decl Declaration) Resolved {
	features := make(map[FeatureID]ResolvedFeature, len(CanonicalFeatureIDs()))
	anyVerified := false
	anyNeutralOrPartial := false

	for _, featureID := range CanonicalFeatureIDs() {
		resolvedFeature := resolveFeature(featureID, decl.Features[featureID])
		features[featureID] = resolvedFeature
		switch resolvedFeature.State {
		case FeatureStateVerified:
			anyVerified = true
		case FeatureStateNeutral, FeatureStatePartial:
			anyNeutralOrPartial = true
		}
		for _, dimension := range resolvedFeature.Dimensions {
			if dimension.State == DimensionStateVerified {
				anyVerified = true
			}
		}
	}

	densityDecl := decl.ProfileDimensions[DimensionDensity]
	density := resolveDimension(DimensionDensity, densityDecl)
	switch density.State {
	case DimensionStateVerified:
		anyVerified = true
	case DimensionStateNeutral:
		anyNeutralOrPartial = true
	}
	profileDimensions := map[DimensionID]ResolvedDimension{
		DimensionDensity: density,
	}

	terminal := ResolvedTerminalDensity{LineHeight: NeutralLineHeight}
	if densityDecl.Mode == DimensionModeCustom && densityDecl.Parameters.TerminalDensity != nil {
		terminal.LineHeight = densityDecl.Parameters.TerminalDensity.LineHeight
	}

	skins := map[ThemeVariant]ResolvedSkin{
		ThemeDark:  resolveSkin(decl, DimensionSkinDark),
		ThemeLight: resolveSkin(decl, DimensionSkinLight),
	}

	state := StateNeutral
	switch {
	case anyVerified && anyNeutralOrPartial:
		state = StatePartial
	case anyVerified && !anyNeutralOrPartial:
		state = StateVerified
	}

	fallbackReason := ""
	if allNonNative(features) {
		fallbackReason = ReasonNonNativeSource
	}

	resolved := Resolved{
		State:              state,
		ProfileID:          decl.ProfileID,
		Features:           features,
		Skins:              skins,
		ProfileDimensions:  profileDimensions,
		FormatterDensity:   ResolvedFormatterDensity{},
		TerminalDensity:    terminal,
		FallbackProfileID:  ProfileNeutralV1,
		FallbackReasonCode: fallbackReason,
	}
	resolved.LayoutFingerprint = layoutFingerprint(resolved)
	resolved.SkinFingerprint = skinFingerprint(resolved)
	return resolved
}

func resolveFeature(id FeatureID, feature FeatureDeclaration) ResolvedFeature {
	dimensions := make(map[DimensionID]ResolvedDimension, len(feature.Dimensions))
	for _, dimensionID := range FeatureDimensions(id) {
		dimensions[dimensionID] = resolveDimension(dimensionID, feature.Dimensions[dimensionID])
	}

	if feature.Mode == FeatureModeNotApplicable {
		return ResolvedFeature{
			State:      FeatureStateNotApplicable,
			Dimensions: dimensions,
			ReasonCode: feature.ReasonCode,
		}
	}

	var verified, neutral int
	applicable := 0
	for _, dimension := range dimensions {
		switch dimension.State {
		case DimensionStateNotApplicable:
			continue
		case DimensionStateVerified:
			applicable++
			verified++
		case DimensionStateNeutral:
			applicable++
			neutral++
		}
	}

	state := FeatureStateNeutral
	switch {
	case feature.Mode == FeatureModeNeutral && verified == 0:
		state = FeatureStateNeutral
	case verified > 0 && neutral > 0:
		state = FeatureStatePartial
	case verified > 0 && neutral == 0:
		state = FeatureStateVerified
	}

	reason := feature.ReasonCode
	if state == FeatureStateVerified {
		reason = ""
	}
	return ResolvedFeature{
		State:      state,
		Dimensions: dimensions,
		ReasonCode: reason,
	}
}

func resolveDimension(id DimensionID, dimension DimensionDeclaration) ResolvedDimension {
	switch dimension.Mode {
	case DimensionModeNotApplicable:
		return ResolvedDimension{
			State:      DimensionStateNotApplicable,
			ReasonCode: dimension.ReasonCode,
		}
	case DimensionModeCustom:
		return ResolvedDimension{
			State:      DimensionStateVerified,
			Parameters: dimension.Parameters,
		}
	default:
		reason := dimension.ReasonCode
		if reason == "" {
			reason = ReasonEvidenceMissing
		}
		return ResolvedDimension{
			State:      DimensionStateNeutral,
			ReasonCode: reason,
		}
	}
}

func resolveSkin(decl Declaration, id DimensionID) ResolvedSkin {
	var overlay *SkinSpec
	for _, featureID := range CanonicalFeatureIDs() {
		feature := decl.Features[featureID]
		dimension, ok := feature.Dimensions[id]
		if !ok || dimension.Mode != DimensionModeCustom || dimension.Parameters.Skin == nil {
			continue
		}
		overlay = dimension.Parameters.Skin
	}
	if overlay == nil {
		return ResolvedSkin{}
	}
	return ResolvedSkin{
		Tool:          deref(overlay.Tool),
		Warning:       deref(overlay.Warning),
		Error:         deref(overlay.Error),
		Success:       deref(overlay.Success),
		Skill:         deref(overlay.Skill),
		Subagent:      deref(overlay.Subagent),
		Muted:         deref(overlay.Muted),
		User:          deref(overlay.User),
		Fg:            deref(overlay.Fg),
		Bg:            deref(overlay.Bg),
		Banner:        deref(overlay.Banner),
		DiffDel:       deref(overlay.DiffDel),
		DiffAdd:       deref(overlay.DiffAdd),
		SuccessBright: deref(overlay.SuccessBright),
		ErrorBright:   deref(overlay.ErrorBright),
	}
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func allNonNative(features map[FeatureID]ResolvedFeature) bool {
	if len(features) == 0 {
		return false
	}
	for _, feature := range features {
		if feature.ReasonCode != ReasonNonNativeSource {
			return false
		}
	}
	return true
}
