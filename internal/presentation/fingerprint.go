package presentation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// layoutFingerprint hashes the canonical encoding of layout-affecting
// resolved fields. Map iteration order is never hashed: features and
// dimensions are written as sorted slices.
//
// Renderer FormatVersion is mixed in by the render compiler (Slice C) so
// this package stays a dependency leaf. Declaration-level identity is still
// deterministic and changes only when effective layout, formatter density,
// fold presentation, or compiler/schema versions change.
func layoutFingerprint(resolved Resolved) string {
	payload := canonicalLayout{
		SchemaVersion:       SchemaVersion,
		CompilerVersion:     CompilerVersion,
		ProfileID:           resolved.ProfileID,
		Features:            canonicalFeatureLayouts(resolved.Features),
		FormatterDensity:    resolved.FormatterDensity,
		Folds:               canonicalFolds(resolved.Features),
		RenderViewTransform: "",
	}
	return sha256Prefixed(mustCanonicalJSON(payload))
}

// skinFingerprint hashes theme tokens and terminal density. Pure RGB and
// xterm lineHeight do not change formatter text, so this value must not
// enter the ANSI or positions cache.
func skinFingerprint(resolved Resolved) string {
	payload := canonicalSkin{
		SkinSchemaVersion: SkinSchemaVersion,
		Dark:              resolved.Skins[ThemeDark],
		Light:             resolved.Skins[ThemeLight],
		TerminalDensity:   resolved.TerminalDensity,
	}
	return sha256Prefixed(mustCanonicalJSON(payload))
}

type canonicalLayout struct {
	SchemaVersion       int                      `json:"schema_version"`
	CompilerVersion     int                      `json:"compiler_version"`
	ProfileID           string                   `json:"profile_id"`
	Features            []canonicalFeatureLayout `json:"features"`
	FormatterDensity    ResolvedFormatterDensity `json:"formatter_density"`
	Folds               []canonicalFold          `json:"folds"`
	RenderViewTransform string                   `json:"render_view_transform"`
}

type canonicalFeatureLayout struct {
	FeatureID FeatureID           `json:"feature_id"`
	Layout    DimensionParameters `json:"layout"`
}

type canonicalFold struct {
	FeatureID FeatureID           `json:"feature_id"`
	Fold      DimensionParameters `json:"fold"`
}

type canonicalSkin struct {
	SkinSchemaVersion int                     `json:"skin_schema_version"`
	Dark              ResolvedSkin            `json:"dark"`
	Light             ResolvedSkin            `json:"light"`
	TerminalDensity   ResolvedTerminalDensity `json:"terminal_density"`
}

func canonicalFeatureLayouts(features map[FeatureID]ResolvedFeature) []canonicalFeatureLayout {
	ids := make([]FeatureID, 0, len(features))
	for id := range features {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]canonicalFeatureLayout, 0, len(ids))
	for _, id := range ids {
		feature := features[id]
		layout := DimensionParameters{}
		if dim, ok := feature.Dimensions[DimensionLayout]; ok {
			layout = dim.Parameters
		}
		out = append(out, canonicalFeatureLayout{
			FeatureID: id,
			Layout:    layout,
		})
	}
	return out
}

func canonicalFolds(features map[FeatureID]ResolvedFeature) []canonicalFold {
	ids := make([]FeatureID, 0, len(features))
	for id := range features {
		if _, ok := features[id].Dimensions[DimensionFold]; ok {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]canonicalFold, 0, len(ids))
	for _, id := range ids {
		dim := features[id].Dimensions[DimensionFold]
		out = append(out, canonicalFold{
			FeatureID: id,
			Fold:      dim.Parameters,
		})
	}
	return out
}

func mustCanonicalJSON(v any) []byte {
	body, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("presentation: canonical JSON encode: %v", err))
	}
	return body
}

func sha256Prefixed(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
