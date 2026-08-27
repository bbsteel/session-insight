package presentation

import (
	"strings"
	"testing"
)

func TestCanonicalCatalogMatchesReferenceManagerIDs(t *testing.T) {
	want := []FeatureID{
		"turn_boundary", "user_prompt", "assistant_text", "thinking",
		"tool_invocation", "tool_running", "tool_result_success", "tool_result_failure",
		"tool_result_timeout", "tool_result_rejected", "file_change", "subagent",
		"context_boundary", "permission_request", "long_output", "live_status",
		"session_completed", "session_interrupted", "tool_group", "nested_fold",
	}
	got := CanonicalFeatureIDs()
	if len(got) != len(want) {
		t.Fatalf("catalog len=%d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("catalog[%d]=%s want %s", i, got[i], want[i])
		}
	}
}

func TestFeatureDimensionApplicability(t *testing.T) {
	for _, id := range CanonicalFeatureIDs() {
		dims := FeatureDimensions(id)
		hasLayout, hasDark, hasLight, hasFold, hasDensity := false, false, false, false, false
		for _, dim := range dims {
			switch dim {
			case DimensionLayout:
				hasLayout = true
			case DimensionSkinDark:
				hasDark = true
			case DimensionSkinLight:
				hasLight = true
			case DimensionFold:
				hasFold = true
			case DimensionDensity:
				hasDensity = true
			}
		}
		if !hasLayout || !hasDark || !hasLight {
			t.Errorf("%s: missing layout/skin dimensions: %v", id, dims)
		}
		if hasDensity {
			t.Errorf("%s: density must be profile-level, not a feature dimension", id)
		}
		wantFold := false
		switch id {
		case FeatureThinking, FeatureToolInvocation, FeatureFileChange,
			FeatureSubagent, FeatureContextBoundary, FeatureLongOutput,
			FeatureToolGroup, FeatureNestedFold:
			wantFold = true
		}
		if hasFold != wantFold {
			t.Errorf("%s: fold applicability=%v want %v", id, hasFold, wantFold)
		}
	}
	profile := ProfileDimensionIDs()
	if len(profile) != 1 || profile[0] != DimensionDensity {
		t.Fatalf("profile dimensions=%v want [density]", profile)
	}
}

func TestNeutralDeclarationValidatesAndResolvesNeutral(t *testing.T) {
	decl := NeutralDeclaration()
	if errs := Validate(decl); len(errs) != 0 {
		t.Fatalf("Validate: %v", errs)
	}
	AssertConformance(t, decl)
	resolved := Resolve(decl)
	if resolved.State != StateNeutral {
		t.Fatalf("state=%s want neutral", resolved.State)
	}
	if resolved.ProfileID != ProfileNeutralV1 {
		t.Fatalf("profile_id=%s", resolved.ProfileID)
	}
	if resolved.FallbackProfileID != ProfileNeutralV1 {
		t.Fatalf("fallback=%s", resolved.FallbackProfileID)
	}
	if resolved.TerminalDensity.LineHeight != NeutralLineHeight {
		t.Fatalf("lineHeight=%v want %v", resolved.TerminalDensity.LineHeight, NeutralLineHeight)
	}
	if len(resolved.Features) != 20 {
		t.Fatalf("features=%d want 20", len(resolved.Features))
	}
	for id, feature := range resolved.Features {
		if feature.State != FeatureStateNeutral {
			t.Errorf("%s state=%s", id, feature.State)
		}
	}
	summary := NewPublicSummary(resolved)
	if summary.VerifiedFeatureCount != 0 || summary.ApplicableFeatureCount != 20 {
		t.Fatalf("counts verified=%d applicable=%d", summary.VerifiedFeatureCount, summary.ApplicableFeatureCount)
	}
	if summary.Dimensions[DimensionDensity].State != DimensionStateNeutral {
		t.Fatalf("density state=%s", summary.Dimensions[DimensionDensity].State)
	}
}

func TestNonNativeDeclarationReason(t *testing.T) {
	decl := NonNativeDeclaration("imported")
	if errs := Validate(decl); len(errs) != 0 {
		t.Fatalf("Validate: %v", errs)
	}
	resolved := Resolve(decl)
	if resolved.State != StateNeutral {
		t.Fatalf("state=%s", resolved.State)
	}
	if resolved.FallbackReasonCode != ReasonNonNativeSource {
		t.Fatalf("fallback reason=%s", resolved.FallbackReasonCode)
	}
	if resolved.ProfileID != ProfileNeutralV1 {
		t.Fatalf("profile_id=%s", resolved.ProfileID)
	}
}

func TestOmittedDimensionFailsValidation(t *testing.T) {
	decl := NeutralDeclaration()
	thinking := decl.Features[FeatureThinking]
	delete(thinking.Dimensions, DimensionFold)
	decl.Features[FeatureThinking] = thinking
	errs := Validate(decl)
	if len(errs) == 0 {
		t.Fatal("expected missing fold dimension error")
	}
	found := false
	for _, err := range errs {
		if err.Code == CodeMissingDimension && strings.Contains(err.Field, "fold") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing fold not reported: %v", errs)
	}
}

func TestExtraDimensionFailsValidation(t *testing.T) {
	decl := NeutralDeclaration()
	prompt := decl.Features[FeatureUserPrompt]
	prompt.Dimensions[DimensionFold] = DimensionDeclaration{Mode: DimensionModeNeutral, ReasonCode: ReasonEvidenceMissing}
	decl.Features[FeatureUserPrompt] = prompt
	errs := Validate(decl)
	if len(errs) == 0 {
		t.Fatal("expected extra fold dimension error")
	}
}

func TestUnknownPrimitiveFailsValidation(t *testing.T) {
	decl := customThinkingLayout("not_a_real_primitive")
	errs := Validate(decl)
	if len(errs) == 0 {
		t.Fatal("expected unknown primitive error")
	}
	found := false
	for _, err := range errs {
		if err.Code == CodeUnknownPrimitive {
			found = true
		}
	}
	if !found {
		t.Fatalf("unknown primitive not reported: %v", errs)
	}
}

func TestInvalidDeclarationResolvesToNeutralFallback(t *testing.T) {
	got := Resolve(Declaration{AgentType: "codex"})
	if got.State != StateNeutral {
		t.Fatalf("state=%s", got.State)
	}
	if got.ProfileID != ProfileNeutralV1 {
		t.Fatalf("profile_id=%s want %s", got.ProfileID, ProfileNeutralV1)
	}
	if got.FallbackReasonCode != ReasonDeclarationInvalid {
		t.Fatalf("reason=%s", got.FallbackReasonCode)
	}
	want := Resolve(NeutralDeclaration())
	if got.LayoutFingerprint != want.LayoutFingerprint {
		t.Fatalf("fallback layout fingerprint drifted from neutral.v1")
	}
}

func TestPartialStateWhenOneDimensionCustom(t *testing.T) {
	decl := customThinkingLayout(string(PrimitiveThinkingSidebar))
	if errs := Validate(decl); len(errs) != 0 {
		t.Fatalf("Validate: %v", errs)
	}
	resolved := Resolve(decl)
	if resolved.State != StatePartial {
		t.Fatalf("profile state=%s want partial", resolved.State)
	}
	thinking := resolved.Features[FeatureThinking]
	if thinking.State != FeatureStatePartial {
		t.Fatalf("thinking state=%s want partial", thinking.State)
	}
	if thinking.Dimensions[DimensionLayout].State != DimensionStateVerified {
		t.Fatalf("layout state=%s", thinking.Dimensions[DimensionLayout].State)
	}
	if thinking.Dimensions[DimensionSkinDark].State != DimensionStateNeutral {
		t.Fatalf("skin.dark should stay neutral")
	}
	if thinking.Dimensions[DimensionFold].State != DimensionStateNeutral {
		t.Fatalf("fold should stay neutral without a pair")
	}
}

func TestCustomWithoutEvidenceFails(t *testing.T) {
	decl := NeutralDeclarationFor("codex", "codex.native.v1")
	thinking := decl.Features[FeatureThinking]
	thinking.Mode = FeatureModeCustom
	thinking.Dimensions[DimensionLayout] = DimensionDeclaration{
		Mode: DimensionModeCustom,
		Parameters: DimensionParameters{
			Layout: &FeatureLayoutSpec{Primitive: PrimitiveThinkingSidebar},
		},
	}
	decl.Features[FeatureThinking] = thinking
	errs := Validate(decl)
	found := false
	for _, err := range errs {
		if err.Code == CodeEvidenceRequired {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected evidence_required, got %v", errs)
	}
}

func TestFingerprintsDeterministicAndInsensitiveToMapOrder(t *testing.T) {
	a := NeutralDeclarationFor("codex", "codex.native.v1")
	b := NeutralDeclarationFor("codex", "codex.native.v1")
	// Re-insert features in reverse to change map iteration if the runtime
	// preserves insertion order.
	reversed := make(map[FeatureID]FeatureDeclaration, len(b.Features))
	ids := CanonicalFeatureIDs()
	for i := len(ids) - 1; i >= 0; i-- {
		reversed[ids[i]] = b.Features[ids[i]]
	}
	b.Features = reversed

	ra := Resolve(a)
	rb := Resolve(b)
	if ra.LayoutFingerprint != rb.LayoutFingerprint {
		t.Fatalf("layout fingerprint depends on map order")
	}
	if ra.SkinFingerprint != rb.SkinFingerprint {
		t.Fatalf("skin fingerprint depends on map order")
	}
	if ra.LayoutFingerprint == ra.SkinFingerprint {
		t.Fatal("layout and skin fingerprints collided")
	}
}

func TestLayoutChangeDoesNotChangeSkinFingerprint(t *testing.T) {
	base := Resolve(NeutralDeclarationFor("codex", "codex.native.v1"))
	custom := Resolve(customThinkingLayout(string(PrimitiveThinkingSidebar)))
	if custom.LayoutFingerprint == base.LayoutFingerprint {
		t.Fatal("custom layout must change layout fingerprint")
	}
	if custom.SkinFingerprint != base.SkinFingerprint {
		t.Fatal("layout-only change must not change skin fingerprint")
	}
}

func TestSkinChangeDoesNotChangeLayoutFingerprint(t *testing.T) {
	baseDecl := NeutralDeclarationFor("codex", "codex.native.v1")
	color := "#ff8800"
	skinDecl := NeutralDeclarationFor("codex", "codex.native.v1")
	thinking := skinDecl.Features[FeatureThinking]
	thinking.Mode = FeatureModeCustom
	thinking.Dimensions[DimensionSkinDark] = DimensionDeclaration{
		Mode:        DimensionModeCustom,
		EvidenceIDs: []EvidenceID{"codex.04-thinking.0123456789ab"},
		Parameters:  DimensionParameters{Skin: &SkinSpec{Tool: &color}},
	}
	skinDecl.Features[FeatureThinking] = thinking
	if errs := Validate(skinDecl); len(errs) != 0 {
		t.Fatalf("Validate: %v", errs)
	}
	base := Resolve(baseDecl)
	got := Resolve(skinDecl)
	if got.SkinFingerprint == base.SkinFingerprint {
		t.Fatal("skin token override must change skin fingerprint")
	}
	if got.LayoutFingerprint != base.LayoutFingerprint {
		t.Fatal("skin-only change must not change layout fingerprint")
	}
}

func TestTerminalDensityChangesSkinNotLayoutFingerprint(t *testing.T) {
	base := Resolve(NeutralDeclarationFor("codex", "codex.native.v1"))
	decl := NeutralDeclarationFor("codex", "codex.native.v1")
	decl.ProfileDimensions[DimensionDensity] = DimensionDeclaration{
		Mode:        DimensionModeCustom,
		EvidenceIDs: []EvidenceID{"codex.01-session-overview.0123456789ab"},
		Parameters:  DimensionParameters{TerminalDensity: &TerminalDensitySpec{LineHeight: 1.05}},
	}
	if errs := Validate(decl); len(errs) != 0 {
		t.Fatalf("Validate: %v", errs)
	}
	got := Resolve(decl)
	if got.TerminalDensity.LineHeight != 1.05 {
		t.Fatalf("lineHeight=%v", got.TerminalDensity.LineHeight)
	}
	if got.SkinFingerprint == base.SkinFingerprint {
		t.Fatal("terminal density must change skin fingerprint")
	}
	if got.LayoutFingerprint != base.LayoutFingerprint {
		t.Fatal("terminal density must not change layout fingerprint")
	}
}

func TestNativeNeutralDeclarationProfileID(t *testing.T) {
	decl := NativeNeutralDeclaration("claude")
	if decl.ProfileID != "claude.native.v1" {
		t.Fatalf("profile_id=%s", decl.ProfileID)
	}
	if decl.FallbackProfileID != ProfileNeutralV1 {
		t.Fatalf("fallback=%s", decl.FallbackProfileID)
	}
	if errs := Validate(decl); len(errs) != 0 {
		t.Fatalf("Validate: %v", errs)
	}
}

func customThinkingLayout(primitive string) Declaration {
	decl := NeutralDeclarationFor("codex", "codex.native.v1")
	thinking := decl.Features[FeatureThinking]
	thinking.Mode = FeatureModeCustom
	thinking.Dimensions[DimensionLayout] = DimensionDeclaration{
		Mode:        DimensionModeCustom,
		EvidenceIDs: []EvidenceID{"codex.04-thinking.0123456789ab"},
		Parameters: DimensionParameters{
			Layout: &FeatureLayoutSpec{Primitive: PrimitiveID(primitive)},
		},
	}
	decl.Features[FeatureThinking] = thinking
	return decl
}
