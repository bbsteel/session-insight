package presentation

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ValidationError is a deterministic, actionable contract violation.
type ValidationError struct {
	Field   string
	Code    string
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

// Error codes for Validate.
const (
	CodeInvalidSchemaVersion    = "invalid_schema_version"
	CodeEmptyAgentType          = "empty_agent_type"
	CodeInvalidAgentType        = "invalid_agent_type"
	CodeEmptyProfileID          = "empty_profile_id"
	CodeInvalidFallbackProfile  = "invalid_fallback_profile"
	CodeMissingFeature          = "missing_feature"
	CodeUnknownFeature          = "unknown_feature"
	CodeUnknownFeatureMode      = "unknown_feature_mode"
	CodeMissingDimension        = "missing_dimension"
	CodeUnknownDimension        = "unknown_dimension"
	CodeUnknownDimensionMode    = "unknown_dimension_mode"
	CodeReasonRequired          = "reason_required"
	CodeUnknownReasonCode       = "unknown_reason_code"
	CodeEvidenceRequired        = "evidence_required"
	CodeEvidenceForbidden       = "evidence_forbidden"
	CodeUnknownPrimitive        = "unknown_primitive"
	CodeInvalidParameters       = "invalid_parameters"
	CodeInvalidGlyph            = "invalid_glyph"
	CodeInvalidRGB              = "invalid_rgb"
	CodeInvalidLineHeight       = "invalid_line_height"
	CodeCustomRequiresDimension = "custom_requires_dimension"
	CodeNeutralHasCustomParams  = "neutral_has_custom_params"
	CodeNotApplicableNoReason   = "not_applicable_no_reason"
)

const (
	maxGlyphRunes = 8
	maxLabelRunes = 32
	minLineHeight = 1.0
	maxLineHeight = 1.5
)

// Validate checks a static presentation declaration against the catalog,
// dimension applicability, typed parameter unions, and reason-code rules.
//
// Errors are returned in deterministic field order. Invalid declarations must
// not run as partial: Resolve falls back to neutral.v1 instead.
func Validate(decl Declaration) ValidationErrors {
	var errs ValidationErrors

	if decl.SchemaVersion != SchemaVersion {
		errs = append(errs, ValidationError{
			Field:   "schema_version",
			Code:    CodeInvalidSchemaVersion,
			Message: fmt.Sprintf("SchemaVersion must be %d", SchemaVersion),
		})
	}
	if strings.TrimSpace(decl.AgentType) == "" {
		errs = append(errs, ValidationError{
			Field:   "agent_type",
			Code:    CodeEmptyAgentType,
			Message: "AgentType must be a non-empty stable identifier",
		})
	} else if !isNormalizedAgentType(decl.AgentType) {
		errs = append(errs, ValidationError{
			Field:   "agent_type",
			Code:    CodeInvalidAgentType,
			Message: "AgentType must be trimmed lowercase (no leading/trailing space, no uppercase)",
		})
	}
	if strings.TrimSpace(decl.ProfileID) == "" {
		errs = append(errs, ValidationError{
			Field:   "profile_id",
			Code:    CodeEmptyProfileID,
			Message: "ProfileID must be non-empty",
		})
	}
	if decl.FallbackProfileID != ProfileNeutralV1 {
		errs = append(errs, ValidationError{
			Field:   "fallback_profile_id",
			Code:    CodeInvalidFallbackProfile,
			Message: fmt.Sprintf("FallbackProfileID must be %q", ProfileNeutralV1),
		})
	}

	errs = append(errs, validateFeatures(decl)...)
	errs = append(errs, validateProfileDimensions(decl.ProfileDimensions)...)
	return sortErrors(errs)
}

func validateFeatures(decl Declaration) ValidationErrors {
	var errs ValidationErrors
	if decl.Features == nil {
		for _, id := range CanonicalFeatureIDs() {
			errs = append(errs, ValidationError{
				Field:   "features." + string(id),
				Code:    CodeMissingFeature,
				Message: fmt.Sprintf("feature %q is required exactly once", id),
			})
		}
		return errs
	}

	var unknown []string
	for id := range decl.Features {
		if !IsCanonicalFeature(id) {
			unknown = append(unknown, string(id))
		}
	}
	sort.Strings(unknown)
	for _, id := range unknown {
		errs = append(errs, ValidationError{
			Field:   "features." + id,
			Code:    CodeUnknownFeature,
			Message: fmt.Sprintf("feature %q is not in the central catalog", id),
		})
	}

	for _, id := range CanonicalFeatureIDs() {
		feature, ok := decl.Features[id]
		if !ok {
			errs = append(errs, ValidationError{
				Field:   "features." + string(id),
				Code:    CodeMissingFeature,
				Message: fmt.Sprintf("feature %q is required exactly once", id),
			})
			continue
		}
		errs = append(errs, validateFeature(id, feature)...)
	}
	return errs
}

func validateFeature(id FeatureID, feature FeatureDeclaration) ValidationErrors {
	var errs ValidationErrors
	field := "features." + string(id)

	switch feature.Mode {
	case FeatureModeNeutral, FeatureModeCustom, FeatureModeNotApplicable:
	default:
		errs = append(errs, ValidationError{
			Field:   field + ".mode",
			Code:    CodeUnknownFeatureMode,
			Message: fmt.Sprintf("mode %q is not a known FeatureMode", feature.Mode),
		})
	}

	if feature.Mode != FeatureModeCustom {
		if strings.TrimSpace(feature.ReasonCode) == "" {
			code := CodeReasonRequired
			if feature.Mode == FeatureModeNotApplicable {
				code = CodeNotApplicableNoReason
			}
			errs = append(errs, ValidationError{
				Field:   field + ".reason_code",
				Code:    code,
				Message: fmt.Sprintf("mode %q requires a non-empty ReasonCode", feature.Mode),
			})
		} else if !IsKnownReasonCode(feature.ReasonCode) {
			errs = append(errs, ValidationError{
				Field:   field + ".reason_code",
				Code:    CodeUnknownReasonCode,
				Message: fmt.Sprintf("reason_code %q is not a first-edition reason code", feature.ReasonCode),
			})
		}
	} else if feature.ReasonCode != "" && !IsKnownReasonCode(feature.ReasonCode) {
		errs = append(errs, ValidationError{
			Field:   field + ".reason_code",
			Code:    CodeUnknownReasonCode,
			Message: fmt.Sprintf("reason_code %q is not a first-edition reason code", feature.ReasonCode),
		})
	}

	applicable := FeatureDimensions(id)
	if feature.Dimensions == nil {
		for _, dimID := range applicable {
			errs = append(errs, ValidationError{
				Field:   field + ".dimensions." + string(dimID),
				Code:    CodeMissingDimension,
				Message: fmt.Sprintf("dimension %q is required for feature %q", dimID, id),
			})
		}
		return errs
	}

	applicableSet := make(map[DimensionID]struct{}, len(applicable))
	for _, dimID := range applicable {
		applicableSet[dimID] = struct{}{}
	}
	var extra []string
	for dimID := range feature.Dimensions {
		if _, ok := applicableSet[dimID]; !ok {
			extra = append(extra, string(dimID))
		}
	}
	sort.Strings(extra)
	for _, dimID := range extra {
		errs = append(errs, ValidationError{
			Field:   field + ".dimensions." + dimID,
			Code:    CodeUnknownDimension,
			Message: fmt.Sprintf("dimension %q is not applicable to feature %q", dimID, id),
		})
	}

	customCount := 0
	for _, dimID := range applicable {
		dimension, ok := feature.Dimensions[dimID]
		if !ok {
			errs = append(errs, ValidationError{
				Field:   field + ".dimensions." + string(dimID),
				Code:    CodeMissingDimension,
				Message: fmt.Sprintf("dimension %q is required for feature %q", dimID, id),
			})
			continue
		}
		if dimension.Mode == DimensionModeCustom {
			customCount++
		}
		errs = append(errs, validateDimension(field+".dimensions."+string(dimID), dimID, dimension, feature.Mode)...)
	}

	if feature.Mode == FeatureModeCustom && customCount == 0 {
		errs = append(errs, ValidationError{
			Field:   field + ".mode",
			Code:    CodeCustomRequiresDimension,
			Message: "custom feature requires at least one custom dimension",
		})
	}
	if feature.Mode == FeatureModeNeutral && customCount > 0 {
		errs = append(errs, ValidationError{
			Field:   field + ".mode",
			Code:    CodeCustomRequiresDimension,
			Message: "neutral feature cannot declare a custom dimension",
		})
	}
	if feature.Mode == FeatureModeNotApplicable && customCount > 0 {
		errs = append(errs, ValidationError{
			Field:   field + ".mode",
			Code:    CodeCustomRequiresDimension,
			Message: "not_applicable feature cannot declare a custom dimension",
		})
	}
	return errs
}

func validateProfileDimensions(dims map[DimensionID]DimensionDeclaration) ValidationErrors {
	var errs ValidationErrors
	field := "profile_dimensions"
	required := ProfileDimensionIDs()
	if dims == nil {
		for _, id := range required {
			errs = append(errs, ValidationError{
				Field:   field + "." + string(id),
				Code:    CodeMissingDimension,
				Message: fmt.Sprintf("profile dimension %q is required", id),
			})
		}
		return errs
	}

	allowed := make(map[DimensionID]struct{}, len(required))
	for _, id := range required {
		allowed[id] = struct{}{}
	}
	var extra []string
	for id := range dims {
		if _, ok := allowed[id]; !ok {
			extra = append(extra, string(id))
		}
	}
	sort.Strings(extra)
	for _, id := range extra {
		errs = append(errs, ValidationError{
			Field:   field + "." + id,
			Code:    CodeUnknownDimension,
			Message: fmt.Sprintf("dimension %q is not a profile-level dimension", id),
		})
	}
	for _, id := range required {
		dimension, ok := dims[id]
		if !ok {
			errs = append(errs, ValidationError{
				Field:   field + "." + string(id),
				Code:    CodeMissingDimension,
				Message: fmt.Sprintf("profile dimension %q is required", id),
			})
			continue
		}
		errs = append(errs, validateDimension(field+"."+string(id), id, dimension, FeatureModeNeutral)...)
	}
	return errs
}

func validateDimension(field string, id DimensionID, dimension DimensionDeclaration, featureMode FeatureMode) ValidationErrors {
	var errs ValidationErrors
	switch dimension.Mode {
	case DimensionModeNeutral, DimensionModeCustom, DimensionModeNotApplicable:
	default:
		errs = append(errs, ValidationError{
			Field:   field + ".mode",
			Code:    CodeUnknownDimensionMode,
			Message: fmt.Sprintf("mode %q is not a known DimensionMode", dimension.Mode),
		})
	}

	if dimension.Mode != DimensionModeCustom {
		if strings.TrimSpace(dimension.ReasonCode) == "" {
			code := CodeReasonRequired
			if dimension.Mode == DimensionModeNotApplicable {
				code = CodeNotApplicableNoReason
			}
			errs = append(errs, ValidationError{
				Field:   field + ".reason_code",
				Code:    code,
				Message: fmt.Sprintf("mode %q requires a non-empty ReasonCode", dimension.Mode),
			})
		} else if !IsKnownReasonCode(dimension.ReasonCode) {
			errs = append(errs, ValidationError{
				Field:   field + ".reason_code",
				Code:    CodeUnknownReasonCode,
				Message: fmt.Sprintf("reason_code %q is not a first-edition reason code", dimension.ReasonCode),
			})
		}
	} else if dimension.ReasonCode != "" && !IsKnownReasonCode(dimension.ReasonCode) {
		errs = append(errs, ValidationError{
			Field:   field + ".reason_code",
			Code:    CodeUnknownReasonCode,
			Message: fmt.Sprintf("reason_code %q is not a first-edition reason code", dimension.ReasonCode),
		})
	}

	if dimension.Mode == DimensionModeCustom {
		if len(dimension.EvidenceIDs) == 0 {
			errs = append(errs, ValidationError{
				Field:   field + ".evidence_ids",
				Code:    CodeEvidenceRequired,
				Message: "custom dimension requires at least one evidence ID",
			})
		}
		for i, evidenceID := range dimension.EvidenceIDs {
			if strings.TrimSpace(string(evidenceID)) == "" {
				errs = append(errs, ValidationError{
					Field:   fmt.Sprintf("%s.evidence_ids[%d]", field, i),
					Code:    CodeEvidenceRequired,
					Message: "evidence ID must be non-empty",
				})
			}
		}
	} else if len(dimension.EvidenceIDs) > 0 {
		errs = append(errs, ValidationError{
			Field:   field + ".evidence_ids",
			Code:    CodeEvidenceForbidden,
			Message: "neutral and not_applicable dimensions cannot reference custom evidence",
		})
	}

	if dimension.Mode != DimensionModeCustom && !dimension.Parameters.isZero() {
		errs = append(errs, ValidationError{
			Field:   field + ".parameters",
			Code:    CodeNeutralHasCustomParams,
			Message: "neutral and not_applicable dimensions cannot carry custom parameters",
		})
	}

	if dimension.Mode == DimensionModeCustom {
		errs = append(errs, validateParameterUnion(field+".parameters", id, dimension.Parameters)...)
	}

	_ = featureMode
	return errs
}

func validateParameterUnion(field string, id DimensionID, params DimensionParameters) ValidationErrors {
	var errs ValidationErrors
	switch id {
	case DimensionLayout:
		if params.Layout == nil {
			errs = append(errs, ValidationError{
				Field:   field + ".layout",
				Code:    CodeInvalidParameters,
				Message: "layout dimension requires Layout parameters",
			})
			return errs
		}
		if params.Skin != nil || params.Fold != nil || params.FormatterDensity != nil || params.TerminalDensity != nil {
			errs = append(errs, ValidationError{
				Field:   field,
				Code:    CodeInvalidParameters,
				Message: "layout dimension may only set Layout parameters",
			})
		}
		if !IsKnownPrimitive(params.Layout.Primitive) {
			errs = append(errs, ValidationError{
				Field:   field + ".layout.primitive",
				Code:    CodeUnknownPrimitive,
				Message: fmt.Sprintf("primitive %q is not registered", params.Layout.Primitive),
			})
		}
		errs = append(errs, validatePrimitiveParameters(field+".layout.parameters", params.Layout.Parameters)...)
	case DimensionSkinDark, DimensionSkinLight:
		if params.Layout != nil || params.Fold != nil || params.FormatterDensity != nil || params.TerminalDensity != nil {
			errs = append(errs, ValidationError{
				Field:   field,
				Code:    CodeInvalidParameters,
				Message: "skin dimension may only set Skin parameters",
			})
		}
		if params.Skin != nil {
			errs = append(errs, validateSkinSpec(field+".skin", *params.Skin)...)
		}
	case DimensionFold:
		if params.Fold == nil {
			errs = append(errs, ValidationError{
				Field:   field + ".fold",
				Code:    CodeInvalidParameters,
				Message: "fold dimension requires Fold parameters",
			})
			return errs
		}
		if params.Layout != nil || params.Skin != nil || params.FormatterDensity != nil || params.TerminalDensity != nil {
			errs = append(errs, ValidationError{
				Field:   field,
				Code:    CodeInvalidParameters,
				Message: "fold dimension may only set Fold parameters",
			})
		}
		if params.Fold.HeaderPrimitive != "" && !IsKnownPrimitive(params.Fold.HeaderPrimitive) {
			errs = append(errs, ValidationError{
				Field:   field + ".fold.header_primitive",
				Code:    CodeUnknownPrimitive,
				Message: fmt.Sprintf("primitive %q is not registered", params.Fold.HeaderPrimitive),
			})
		}
		errs = append(errs, validateGlyph(field+".fold.expanded_glyph", params.Fold.ExpandedGlyph)...)
		errs = append(errs, validateGlyph(field+".fold.collapsed_glyph", params.Fold.CollapsedGlyph)...)
		switch params.Fold.DefaultState {
		case "", FoldCollapsed, FoldExpanded:
		default:
			errs = append(errs, ValidationError{
				Field:   field + ".fold.default_state",
				Code:    CodeInvalidParameters,
				Message: fmt.Sprintf("default_state %q must be collapsed or expanded", params.Fold.DefaultState),
			})
		}
	case DimensionDensity:
		if params.Layout != nil || params.Skin != nil || params.Fold != nil {
			errs = append(errs, ValidationError{
				Field:   field,
				Code:    CodeInvalidParameters,
				Message: "density dimension may only set FormatterDensity or TerminalDensity",
			})
		}
		if params.FormatterDensity == nil && params.TerminalDensity == nil {
			errs = append(errs, ValidationError{
				Field:   field,
				Code:    CodeInvalidParameters,
				Message: "custom density requires FormatterDensity or TerminalDensity",
			})
		}
		if params.TerminalDensity != nil {
			h := params.TerminalDensity.LineHeight
			if h < minLineHeight || h > maxLineHeight {
				errs = append(errs, ValidationError{
					Field:   field + ".terminal_density.line_height",
					Code:    CodeInvalidLineHeight,
					Message: fmt.Sprintf("lineHeight must be between %.1f and %.1f", minLineHeight, maxLineHeight),
				})
			}
		}
	default:
		errs = append(errs, ValidationError{
			Field:   field,
			Code:    CodeUnknownDimension,
			Message: fmt.Sprintf("dimension %q has no parameter union", id),
		})
	}
	return errs
}

func validatePrimitiveParameters(field string, params PrimitiveParameters) ValidationErrors {
	var errs ValidationErrors
	if params.Box != nil {
		for name, value := range map[string]string{
			"tl": params.Box.TL, "tr": params.Box.TR, "bl": params.Box.BL,
			"br": params.Box.BR, "h": params.Box.H, "v": params.Box.V,
		} {
			if value == "" {
				continue
			}
			errs = append(errs, validateGlyph(field+".box."+name, value)...)
		}
	}
	if params.Marker != nil && params.Marker.Char != "" {
		errs = append(errs, validateGlyph(field+".marker.char", params.Marker.Char)...)
	}
	if params.Labels != nil && params.Labels.UserHeader != "" {
		errs = append(errs, validateLabel(field+".labels.user_header", params.Labels.UserHeader)...)
	}
	if params.Spacing != nil && params.Spacing.ResultIndent != "" {
		if containsUnsafeText(params.Spacing.ResultIndent) {
			errs = append(errs, ValidationError{
				Field:   field + ".spacing.result_indent",
				Code:    CodeInvalidGlyph,
				Message: "result indent cannot contain control characters or ANSI",
			})
		}
		if utf8.RuneCountInString(params.Spacing.ResultIndent) > maxGlyphRunes {
			errs = append(errs, ValidationError{
				Field:   field + ".spacing.result_indent",
				Code:    CodeInvalidGlyph,
				Message: fmt.Sprintf("result indent exceeds %d runes", maxGlyphRunes),
			})
		}
	}
	return errs
}

func validateSkinSpec(field string, spec SkinSpec) ValidationErrors {
	var errs ValidationErrors
	tokens := []struct {
		name  string
		value *string
	}{
		{"tool", spec.Tool},
		{"warning", spec.Warning},
		{"error", spec.Error},
		{"success", spec.Success},
		{"skill", spec.Skill},
		{"subagent", spec.Subagent},
		{"muted", spec.Muted},
		{"user", spec.User},
		{"fg", spec.Fg},
		{"bg", spec.Bg},
		{"banner", spec.Banner},
		{"diff_del", spec.DiffDel},
		{"diff_add", spec.DiffAdd},
		{"success_bright", spec.SuccessBright},
		{"error_bright", spec.ErrorBright},
	}
	for _, token := range tokens {
		if token.value == nil {
			continue
		}
		if !isRGB(*token.value) {
			errs = append(errs, ValidationError{
				Field:   field + "." + token.name,
				Code:    CodeInvalidRGB,
				Message: fmt.Sprintf("token %q must be #RRGGBB", *token.value),
			})
		}
	}
	return errs
}

func validateGlyph(field, value string) ValidationErrors {
	if value == "" {
		return nil
	}
	if containsUnsafeText(value) || utf8.RuneCountInString(value) > maxGlyphRunes {
		return ValidationErrors{{
			Field:   field,
			Code:    CodeInvalidGlyph,
			Message: "glyph must be a short literal without control characters, ANSI, HTML, or scripts",
		}}
	}
	return nil
}

func validateLabel(field, value string) ValidationErrors {
	if value == "" {
		return nil
	}
	if containsUnsafeText(value) || strings.ContainsAny(value, "{}%") || utf8.RuneCountInString(value) > maxLabelRunes {
		return ValidationErrors{{
			Field:   field,
			Code:    CodeInvalidGlyph,
			Message: "label must be a short literal without format verbs, control characters, or ANSI",
		}}
	}
	return nil
}

func (p DimensionParameters) isZero() bool {
	return p.Layout == nil && p.Skin == nil && p.Fold == nil && p.FormatterDensity == nil && p.TerminalDensity == nil
}

func containsUnsafeText(s string) bool {
	if strings.Contains(s, "\x1b") || strings.Contains(s, "<") || strings.Contains(s, ">") {
		return true
	}
	for _, r := range s {
		if r == '\t' || r == ' ' {
			continue
		}
		if r < 32 || r == 127 {
			return true
		}
	}
	return false
}

func isRGB(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for i := 1; i < 7; i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func isNormalizedAgentType(s string) bool {
	if s == "" || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsUpper(r) {
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
