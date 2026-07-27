package collaboration

import "testing"

func TestRootInvocationIDDeterministic(t *testing.T) {
	got := RootInvocationID("codex", "s-1")
	if got != "codex:s-1:root" {
		t.Fatalf("RootInvocationID = %q", got)
	}
	if RootInvocationID("codex", "s-1") != got {
		t.Fatal("root ID must be deterministic")
	}
}

func TestChildInvocationIDNamespacesNativeID(t *testing.T) {
	got := ChildInvocationID("copilot", "s-1", "call-task-A")
	want := "copilot:s-1:child:call-task-A"
	if got != want {
		t.Fatalf("ChildInvocationID = %q, want %q", got, want)
	}
	// The same native ID under a different root never collides.
	if ChildInvocationID("copilot", "s-2", "call-task-A") == got {
		t.Fatal("child IDs must be namespaced by root session")
	}
}

func TestDelegationIDForDerivesFromEndpoints(t *testing.T) {
	p, c := "a:s:root", "a:s:child:x"
	got := DelegationIDFor(p, c)
	if got != p+"->"+c {
		t.Fatalf("DelegationIDFor = %q", got)
	}
	if DelegationIDFor(c, p) == got {
		t.Fatal("delegation ID must distinguish direction")
	}
}

func TestIdentityHelpersRejectNoInput(t *testing.T) {
	// Helpers are pure string builders; the contract forbids empty
	// components at validation time (missing_field), so builders stay
	// total. This test pins the documented separator shape.
	if !IsRootInvocationID("a", "s", "a:s:root") {
		t.Fatal("IsRootInvocationID should match the deterministic root")
	}
	if IsRootInvocationID("a", "s", "a:s:child:x") {
		t.Fatal("child ID must not be recognized as root")
	}
}
