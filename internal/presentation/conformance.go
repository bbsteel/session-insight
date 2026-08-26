package presentation

import (
	"strings"
	"testing"
)

// AssertConformance runs the shared declaration checks that every registered
// Agent must pass. Evidence-lock file pairing is Slice E; Slice B asserts
// that custom dimensions carry evidence IDs and that neutral declarations
// do not.
func AssertConformance(t *testing.T, decl Declaration) {
	t.Helper()
	t.Run("validate", func(t *testing.T) {
		if errs := Validate(decl); len(errs) != 0 {
			t.Fatalf("Validate failed: %v", errs)
		}
	})
	t.Run("catalog_complete", func(t *testing.T) {
		if len(decl.Features) != len(CanonicalFeatureIDs()) {
			t.Fatalf("features=%d want %d", len(decl.Features), len(CanonicalFeatureIDs()))
		}
		for _, id := range CanonicalFeatureIDs() {
			feature, ok := decl.Features[id]
			if !ok {
				t.Fatalf("missing feature %s", id)
			}
			want := FeatureDimensions(id)
			if len(feature.Dimensions) != len(want) {
				t.Fatalf("%s: dimensions=%d want %d", id, len(feature.Dimensions), len(want))
			}
			for _, dimID := range want {
				if _, ok := feature.Dimensions[dimID]; !ok {
					t.Fatalf("%s: missing dimension %s", id, dimID)
				}
			}
		}
	})
	resolved := Resolve(decl)
	t.Run("resolve_deterministic", func(t *testing.T) {
		again := Resolve(decl)
		if resolved.LayoutFingerprint != again.LayoutFingerprint {
			t.Fatalf("layout fingerprint drifted: %s vs %s", resolved.LayoutFingerprint, again.LayoutFingerprint)
		}
		if resolved.SkinFingerprint != again.SkinFingerprint {
			t.Fatalf("skin fingerprint drifted: %s vs %s", resolved.SkinFingerprint, again.SkinFingerprint)
		}
		if !strings.HasPrefix(resolved.LayoutFingerprint, "sha256:") || !strings.HasPrefix(resolved.SkinFingerprint, "sha256:") {
			t.Fatalf("fingerprints must use sha256: prefix, got %q / %q", resolved.LayoutFingerprint, resolved.SkinFingerprint)
		}
		if resolved.LayoutFingerprint == resolved.SkinFingerprint {
			t.Fatal("layout and skin fingerprints must not collapse to the same digest")
		}
	})
	t.Run("state_and_reason_codes", func(t *testing.T) {
		switch resolved.State {
		case StateNeutral, StatePartial, StateVerified:
		default:
			t.Fatalf("unknown profile state %q", resolved.State)
		}
		if resolved.FallbackReasonCode != "" && !IsKnownReasonCode(resolved.FallbackReasonCode) {
			t.Fatalf("unknown fallback reason %q", resolved.FallbackReasonCode)
		}
		for id, feature := range resolved.Features {
			if feature.ReasonCode != "" && !IsKnownReasonCode(feature.ReasonCode) {
				t.Errorf("%s: unknown reason %q", id, feature.ReasonCode)
			}
			for dimID, dimension := range feature.Dimensions {
				if dimension.ReasonCode != "" && !IsKnownReasonCode(dimension.ReasonCode) {
					t.Errorf("%s.%s: unknown reason %q", id, dimID, dimension.ReasonCode)
				}
				if dimension.State == DimensionStateVerified && dimension.ReasonCode != "" {
					t.Errorf("%s.%s: verified dimension should not carry a fallback reason", id, dimID)
				}
			}
		}
	})
	t.Run("neutral_has_no_custom_primitive", func(t *testing.T) {
		for id, feature := range decl.Features {
			if feature.Mode != FeatureModeNeutral {
				continue
			}
			for dimID, dimension := range feature.Dimensions {
				if dimension.Mode == DimensionModeCustom {
					t.Errorf("%s.%s: neutral feature has custom dimension", id, dimID)
				}
				if !dimension.Parameters.isZero() {
					t.Errorf("%s.%s: neutral dimension has parameters", id, dimID)
				}
				if len(dimension.EvidenceIDs) > 0 {
					t.Errorf("%s.%s: neutral dimension references evidence", id, dimID)
				}
			}
		}
	})
	t.Run("custom_has_evidence", func(t *testing.T) {
		for id, feature := range decl.Features {
			for dimID, dimension := range feature.Dimensions {
				if dimension.Mode != DimensionModeCustom {
					continue
				}
				if len(dimension.EvidenceIDs) == 0 {
					t.Errorf("%s.%s: custom dimension missing evidence IDs", id, dimID)
				}
			}
		}
	})
	t.Run("skins_and_density_defaults", func(t *testing.T) {
		if _, ok := resolved.Skins[ThemeDark]; !ok {
			t.Fatal("missing dark skin")
		}
		if _, ok := resolved.Skins[ThemeLight]; !ok {
			t.Fatal("missing light skin")
		}
		if resolved.TerminalDensity.LineHeight < minLineHeight || resolved.TerminalDensity.LineHeight > maxLineHeight {
			t.Fatalf("lineHeight %v out of range", resolved.TerminalDensity.LineHeight)
		}
	})
}
