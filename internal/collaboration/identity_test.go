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

// Components may contain ':' or "->" (Codex rollout stems contain both
// shapes of material). Escaping must keep every distinct input triple on a
// distinct ID; raw concatenation would not be injective.
func TestChildInvocationIDInjectiveWithSeparators(t *testing.T) {
	cases := [][3]string{
		{"a", "s:child:x", "y"},
		{"a", "s", "x:y"},
		{"a", "s:child:x:y", "z"},
		{"a", "s", "x->y"},
		{"a", "s:child:x", "y->z"},
		{"a:b", "s", "x"}, // adapter IDs are controlled, but must not collapse either
		{"a", "b:s", "x"},
		{"a", "s", "x%3Ay"}, // pre-escaped-looking content stays distinct
	}
	seen := map[string][3]string{}
	for _, c := range cases {
		id := ChildInvocationID(c[0], c[1], c[2])
		if prev, dup := seen[id]; dup {
			t.Fatalf("ID collision: %v and %v both produce %q", prev, c, id)
		}
		seen[id] = c
	}
	if got := ChildInvocationID("a", "s", "x:y"); got != "a:s:child:x%3Ay" {
		t.Fatalf("escaping shape changed: %q", got)
	}
}

func TestRootInvocationIDInjectiveWithSeparators(t *testing.T) {
	if RootInvocationID("a", "s:root") == RootInvocationID("a:root", "s") {
		t.Fatal("root IDs must not collapse across component boundaries")
	}
	if RootInvocationID("a", "s") != "a:s:root" {
		t.Fatal("plain components must stay unescaped")
	}
}

func TestDelegationIDForInjectiveWithSeparator(t *testing.T) {
	// A child native ID containing "->" is escaped inside the invocation
	// ID, so it cannot masquerade as the delegation separator.
	child := ChildInvocationID("a", "s", "x->y")
	root := RootInvocationID("a", "s")
	if got := DelegationIDFor(root, child); got != "a:s:root->a:s:child:x-%3Ey" {
		t.Fatalf("delegation ID = %q", got)
	}
	other := DelegationIDFor(RootInvocationID("a", "s"), ChildInvocationID("a", "s", "x"))
	if other == DelegationIDFor(root, child) {
		t.Fatal("delegation IDs must stay distinct")
	}
}
