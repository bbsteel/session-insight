package capability

import (
	"strings"
	"testing"
)

// validBase returns a fully valid static declaration for tests to mutate.
func validBase() AgentCapabilities {
	caps := make(map[CapabilityID]CapabilityDeclaration, 10)
	for _, id := range BaselineIDs() {
		caps[id] = Exact()
	}
	return AgentCapabilities{
		AgentType:       "testagent",
		DisplayName:     "Test Agent",
		AdapterRevision: 1,
		Capabilities:    caps,
	}
}

func requireCode(t *testing.T, errs ValidationErrors, code string) {
	t.Helper()
	for _, e := range errs {
		if e.Code == code {
			return
		}
	}
	t.Fatalf("expected error code %q among %v", code, errs)
}

func requireNoCode(t *testing.T, errs ValidationErrors, code string) {
	t.Helper()
	for _, e := range errs {
		if e.Code == code {
			t.Fatalf("did not expect error code %q: %v", code, errs)
		}
	}
}

func TestValidateStaticAcceptsValidDeclaration(t *testing.T) {
	errs := ValidateStatic(validBase())
	if len(errs) != 0 {
		t.Fatalf("valid declaration: %v", errs)
	}
}

func TestValidateStaticEmptyAgentType(t *testing.T) {
	ac := validBase()
	ac.AgentType = "  "
	errs := ValidateStatic(ac)
	requireCode(t, errs, CodeEmptyAgentType)
}

func TestValidateStaticAgentTypeMustBeNormalized(t *testing.T) {
	for _, bad := range []string{"Claude", " claude", "claude ", " claude ", "CODEX"} {
		ac := validBase()
		ac.AgentType = bad
		errs := ValidateStatic(ac)
		requireCode(t, errs, CodeInvalidAgentType)
	}
	ac := validBase()
	ac.AgentType = "claude"
	errs := ValidateStatic(ac)
	requireNoCode(t, errs, CodeInvalidAgentType)
	requireNoCode(t, errs, CodeEmptyAgentType)
}

func TestValidateStaticEmptyDisplayName(t *testing.T) {
	ac := validBase()
	ac.DisplayName = ""
	errs := ValidateStatic(ac)
	requireCode(t, errs, CodeEmptyDisplayName)
}

func TestValidateStaticInvalidAdapterRevision(t *testing.T) {
	ac := validBase()
	ac.AdapterRevision = 0
	errs := ValidateStatic(ac)
	requireCode(t, errs, CodeInvalidAdapterRevision)

	ac.AdapterRevision = -1
	errs = ValidateStatic(ac)
	requireCode(t, errs, CodeInvalidAdapterRevision)
}

func TestValidateStaticMissingCapability(t *testing.T) {
	ac := validBase()
	delete(ac.Capabilities, CapabilityTokens)
	errs := ValidateStatic(ac)
	requireCode(t, errs, CodeMissingCapability)
	found := false
	for _, e := range errs {
		if e.Code == CodeMissingCapability && strings.Contains(e.Field, "tokens") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected missing tokens field in %v", errs)
	}
}

func TestValidateStaticNilCapabilities(t *testing.T) {
	ac := validBase()
	ac.Capabilities = nil
	errs := ValidateStatic(ac)
	if len(errs) < 10 {
		t.Fatalf("expected one error per baseline id, got %d: %v", len(errs), errs)
	}
	for _, e := range errs {
		if e.Code != CodeMissingCapability && e.Code != CodeEmptyAgentType {
			// Only capability-missing expected beyond meta (meta is filled).
			if strings.HasPrefix(e.Field, "capabilities.") && e.Code != CodeMissingCapability {
				t.Fatalf("unexpected code %s on %s", e.Code, e.Field)
			}
		}
	}
	// All ten baseline IDs must be reported.
	var missing int
	for _, e := range errs {
		if e.Code == CodeMissingCapability {
			missing++
		}
	}
	if missing != 10 {
		t.Fatalf("want 10 missing_capability errors, got %d", missing)
	}
}

func TestValidateStaticUnknownCapability(t *testing.T) {
	ac := validBase()
	ac.Capabilities[CapabilityID("memory.inspect")] = Exact()
	errs := ValidateStatic(ac)
	requireCode(t, errs, CodeUnknownCapability)
}

func TestValidateStaticUnknownState(t *testing.T) {
	ac := validBase()
	ac.Capabilities[CapabilityReplay] = CapabilityDeclaration{State: "maybe"}
	errs := ValidateStatic(ac)
	requireCode(t, errs, CodeUnknownState)
	// Unknown non-exact still needs a reason when treated as non-exact.
	requireCode(t, errs, CodeReasonRequired)
}

func TestValidateStaticMissingForbidden(t *testing.T) {
	ac := validBase()
	ac.Capabilities[CapabilityTokens] = CapabilityDeclaration{
		State:      CapabilityMissing,
		ReasonCode: "session_not_finalized",
	}
	errs := ValidateStatic(ac)
	requireCode(t, errs, CodeStaticMissingForbidden)
}

func TestValidateStaticReasonRequiredForNonExact(t *testing.T) {
	for _, state := range []CapabilityState{
		CapabilityEstimated,
		CapabilityUnsupported,
		CapabilityNotApplicable,
	} {
		ac := validBase()
		ac.Capabilities[CapabilityDiff] = CapabilityDeclaration{State: state}
		errs := ValidateStatic(ac)
		requireCode(t, errs, CodeReasonRequired)
	}

	// exact may omit reason.
	ac := validBase()
	ac.Capabilities[CapabilityDiff] = Exact()
	errs := ValidateStatic(ac)
	requireNoCode(t, errs, CodeReasonRequired)
}

func TestValidateStaticTerminateCannotBeEstimated(t *testing.T) {
	ac := validBase()
	ac.Capabilities[CapabilityTerminate] = Estimated("timestamp_heuristic")
	errs := ValidateStatic(ac)
	requireCode(t, errs, CodeTerminateEstimated)
}

func TestValidateStaticTerminateUnsupportedOK(t *testing.T) {
	ac := validBase()
	ac.Capabilities[CapabilityTerminate] = Unsupported("exact_pid_unavailable")
	errs := ValidateStatic(ac)
	if len(errs) != 0 {
		t.Fatalf("unsupported terminate should pass: %v", errs)
	}
}

func TestValidateStaticErrorsDeterministic(t *testing.T) {
	ac := AgentCapabilities{
		AgentType:       "",
		DisplayName:     "",
		AdapterRevision: 0,
		Capabilities: map[CapabilityID]CapabilityDeclaration{
			CapabilityDiscovery: Exact(),
			// omit rest; add unknown
			CapabilityID("zzz"): Exact(),
		},
	}
	a := ValidateStatic(ac)
	b := ValidateStatic(ac)
	if len(a) != len(b) {
		t.Fatalf("length mismatch %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("order/content mismatch at %d: %v vs %v", i, a[i], b[i])
		}
	}
	// Fields must be sorted.
	for i := 1; i < len(a); i++ {
		if a[i-1].Field > a[i].Field {
			// equal fields may differ by code; only require non-decreasing field
			if a[i-1].Field != a[i].Field {
				t.Fatalf("errors not sorted by field: %q after %q", a[i].Field, a[i-1].Field)
			}
		}
	}
}

func TestBaselineIDsCountAndUniqueness(t *testing.T) {
	ids := BaselineIDs()
	if len(ids) != 10 {
		t.Fatalf("want 10 baseline IDs, got %d", len(ids))
	}
	seen := map[CapabilityID]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate baseline id %q", id)
		}
		seen[id] = true
		if id == "" {
			t.Fatal("empty baseline id")
		}
	}
}

func TestIsStaticState(t *testing.T) {
	if IsStaticState(CapabilityMissing) {
		t.Fatal("missing must not be a static state")
	}
	if !IsStaticState(CapabilityExact) {
		t.Fatal("exact is static")
	}
	if !IsKnownState(CapabilityMissing) {
		t.Fatal("missing is still a known session state")
	}
}

func TestValidationErrorString(t *testing.T) {
	e := ValidationError{Field: "agent_type", Code: CodeEmptyAgentType, Message: "must be set"}
	if !strings.Contains(e.Error(), "agent_type") || !strings.Contains(e.Error(), CodeEmptyAgentType) {
		t.Fatalf("Error() = %q", e.Error())
	}
	var empty ValidationErrors
	if empty.Error() != "" {
		t.Fatalf("empty ValidationErrors.Error = %q", empty.Error())
	}
}
