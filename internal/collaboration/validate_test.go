package collaboration

import (
	"strings"
	"testing"
)

// validGraph returns a minimal contract-valid graph for mutation in tests.
func validGraph() *CollaborationGraph {
	root := RootInvocationID("codex", "s-1")
	child := ChildInvocationID("codex", "s-1", "native-1")
	return &CollaborationGraph{
		RootAgentType: "codex",
		RootSessionID: "s-1",
		Revision:      1,
		Completeness:  ExactFact(),
		Invocations: []AgentInvocation{
			{
				ID:               root,
				DisplayName:      "main",
				AgentType:        "codex",
				Status:           StatusCompleted,
				TimePrecision:    ExactFact(),
				ContentPrecision: ExactFact(),
				SourceIdentity:   SourceIdentity{Kind: IdentityRootSession, NativeID: "s-1"},
			},
			{
				ID:               child,
				DisplayName:      "child",
				AgentType:        "codex",
				Status:           StatusUnknown,
				TimePrecision:    FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonCompletionNotRecorded},
				ContentPrecision: ExactFact(),
				SourceIdentity:   SourceIdentity{Kind: IdentityPayloadID, NativeID: "native-1"},
			},
		},
		Delegations: []Delegation{
			{
				ID:                 DelegationIDFor(root, child),
				ParentInvocationID: root,
				ChildInvocationID:  child,
				ExecutionMode:      ExecutionUnknown,
				Evidence: DelegationEvidence{
					Trigger: FactEvidence{State: EvidenceMissing, ReasonCode: ReasonSourceNotRecorded},
					Timing:  FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonCompletionNotRecorded},
					Task:    FactEvidence{State: EvidenceMissing, ReasonCode: ReasonSourceNotRecorded},
					Result:  FactEvidence{State: EvidenceMissing, ReasonCode: ReasonSourceNotRecorded},
				},
			},
		},
	}
}

func TestValidateValidGraph(t *testing.T) {
	v := Validate(validGraph())
	if !v.OK() {
		t.Fatalf("valid graph produced issues: %+v", v.Issues)
	}
	if v.RootID != RootInvocationID("codex", "s-1") {
		t.Errorf("RootID = %q", v.RootID)
	}
	if len(v.Unlinked) != 0 || len(v.Quarantined) != 0 {
		t.Errorf("unexpected unlinked/quarantined: %+v", v)
	}
}

func TestValidateNoRoot(t *testing.T) {
	g := validGraph()
	g.Invocations = g.Invocations[1:] // drop the root
	v := Validate(g)
	if !v.Has(IssueNoRoot) {
		t.Fatalf("want no_root issue: %+v", v.Issues)
	}
	if v.RootID != "" {
		t.Errorf("RootID = %q, want empty", v.RootID)
	}
}

func TestValidateDuplicateInvocation(t *testing.T) {
	g := validGraph()
	g.Invocations = append(g.Invocations, g.Invocations[1]) // same child twice
	v := Validate(g)
	if !v.Has(IssueDuplicateInvocation) {
		t.Fatalf("want duplicate_invocation: %+v", v.Issues)
	}
	// The first occurrence is kept and still linked.
	child := ChildInvocationID("codex", "s-1", "native-1")
	if _, ok := v.CanonicalParent[child]; !ok {
		t.Error("first occurrence must remain linkable")
	}
}

func TestValidateUnknownChild(t *testing.T) {
	g := validGraph()
	d := g.Delegations[0]
	d.ID = "codex:s-1:dangling"
	d.ChildInvocationID = ChildInvocationID("codex", "s-1", "ghost")
	g.Delegations = []Delegation{d}
	v := Validate(g)
	if !v.Has(IssueUnknownChild) {
		t.Fatalf("want unknown_child: %+v", v.Issues)
	}
	if len(v.Quarantined) != 1 || v.Quarantined[0] != "codex:s-1:dangling" {
		t.Errorf("quarantined = %v", v.Quarantined)
	}
}

func TestValidateRootHasParent(t *testing.T) {
	g := validGraph()
	root := RootInvocationID("codex", "s-1")
	child := ChildInvocationID("codex", "s-1", "native-1")
	d := Delegation{
		ID:                 "codex:s-1:bad-root-edge",
		ParentInvocationID: child,
		ChildInvocationID:  root,
		ExecutionMode:      ExecutionUnknown,
		Evidence: DelegationEvidence{
			Trigger: ExactFact(), Timing: ExactFact(), Task: ExactFact(), Result: ExactFact(),
		},
	}
	g.Delegations = append(g.Delegations, d)
	v := Validate(g)
	if !v.Has(IssueRootHasParent) {
		t.Fatalf("want root_has_parent: %+v", v.Issues)
	}
	if _, ok := v.CanonicalParent[root]; ok {
		t.Error("root must never have a canonical parent")
	}
}

func TestValidateInvalidStatus(t *testing.T) {
	g := validGraph()
	g.Invocations[1].Status = InvocationStatus("done")
	v := Validate(g)
	if !v.Has(IssueInvalidStatus) {
		t.Fatalf("want invalid_status: %+v", v.Issues)
	}
}

func TestValidateEmptyStatusIsInvalid(t *testing.T) {
	g := validGraph()
	g.Invocations[1].Status = ""
	v := Validate(g)
	if !v.Has(IssueInvalidStatus) {
		t.Fatal("status must never be omitted; empty is invalid")
	}
}

func TestValidateNonExactFactRequiresReason(t *testing.T) {
	g := validGraph()
	g.Invocations[1].TimePrecision = FactEvidence{State: EvidenceEstimated}
	v := Validate(g)
	if !v.Has(IssueInvalidEvidence) {
		t.Fatalf("want invalid_evidence for reason-less estimated fact: %+v", v.Issues)
	}
}

func TestValidateUnknownEvidenceState(t *testing.T) {
	g := validGraph()
	g.Completeness = FactEvidence{State: EvidenceState("guess")}
	v := Validate(g)
	if !v.Has(IssueInvalidEvidence) {
		t.Fatalf("want invalid_evidence for unknown state: %+v", v.Issues)
	}
}

func TestValidateMissingDisplayName(t *testing.T) {
	g := validGraph()
	g.Invocations[1].DisplayName = ""
	v := Validate(g)
	if !v.Has(IssueMissingField) {
		t.Fatalf("want missing_field for empty display name: %+v", v.Issues)
	}
	for _, i := range v.Issues {
		if i.Code == IssueMissingField && strings.Contains(i.Detail, "synthesize") {
			return
		}
	}
	t.Fatal("display_name finding should point at the synthesized-fallback requirement")
}

func TestValidateInvalidExecutionMode(t *testing.T) {
	g := validGraph()
	g.Delegations[0].ExecutionMode = ExecutionMode("parallel")
	v := Validate(g)
	if !v.Has(IssueInvalidExecutionMode) {
		t.Fatalf("want invalid_execution_mode: %+v", v.Issues)
	}
}

func TestValidateDuplicateDelegationID(t *testing.T) {
	g := validGraph()
	child2 := ChildInvocationID("codex", "s-1", "native-2")
	g.Invocations = append(g.Invocations, AgentInvocation{
		ID:               child2,
		DisplayName:      "second",
		AgentType:        "codex",
		Status:           StatusUnknown,
		TimePrecision:    FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonCompletionNotRecorded},
		ContentPrecision: ExactFact(),
		SourceIdentity:   SourceIdentity{Kind: IdentityPayloadID, NativeID: "native-2"},
	})
	dup := g.Delegations[0] // same ID, different child
	dup.ChildInvocationID = child2
	g.Delegations = append(g.Delegations, dup)
	v := Validate(g)
	if !v.Has(IssueDuplicateDelegation) {
		t.Fatalf("want duplicate_delegation: %+v", v.Issues)
	}
}

func TestValidateMultipleParentsPreservesExtraEvidence(t *testing.T) {
	g := validGraph()
	child := ChildInvocationID("codex", "s-1", "native-1")
	other := ChildInvocationID("codex", "s-1", "native-2")
	g.Invocations = append(g.Invocations, AgentInvocation{
		ID:               other,
		DisplayName:      "second parent",
		AgentType:        "codex",
		Status:           StatusUnknown,
		TimePrecision:    FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonCompletionNotRecorded},
		ContentPrecision: ExactFact(),
		SourceIdentity:   SourceIdentity{Kind: IdentityPayloadID, NativeID: "native-2"},
	})
	// The shared default strength rule is deterministic sorted delegation
	// ID order; adapters hand the validator IDs in their strength order.
	extra := Delegation{
		ID:                 "codex:s-1:zz-extra-parent-evidence",
		ParentInvocationID: other,
		ChildInvocationID:  child,
		ExecutionMode:      ExecutionUnknown,
		Evidence: DelegationEvidence{
			Trigger: ExactFact(), Timing: ExactFact(), Task: ExactFact(), Result: ExactFact(),
		},
	}
	g.Delegations = append(g.Delegations, extra)
	v := Validate(g)
	if !v.Has(IssueMultipleParents) {
		t.Fatalf("want multiple_parents: %+v", v.Issues)
	}
	// Extra evidence is preserved and not quarantined; exactly one canonical parent.
	if len(v.Quarantined) != 0 {
		t.Errorf("extra parent evidence must not be quarantined: %v", v.Quarantined)
	}
	if got := v.CanonicalParent[child]; got == "" || got == extra.ID {
		t.Errorf("canonical parent selection = %q, want the sorted-first delegation", got)
	}
	if len(g.Delegations) != 2 {
		t.Error("validation must not mutate the graph")
	}
}

func TestValidateNilGraph(t *testing.T) {
	v := Validate(nil)
	if !v.Has(IssueMissingField) {
		t.Fatalf("nil graph must be a finding, not a panic: %+v", v.Issues)
	}
	if v.OK() {
		t.Fatal("nil graph must not validate clean")
	}
}

func TestValidateUnknownReasonCode(t *testing.T) {
	g := validGraph()
	g.Invocations[1].TimePrecision = FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonCode("made_up_code")}
	v := Validate(g)
	if !v.Has(IssueInvalidEvidence) {
		t.Fatalf("undeclared reason codes must be rejected: %+v", v.Issues)
	}
	for _, i := range v.Issues {
		if i.Code == IssueInvalidEvidence && i.InvocationID == g.Invocations[1].ID {
			return
		}
	}
	t.Fatal("evidence finding must carry the owning invocation ID")
}

func TestValidateAnchorMissingReason(t *testing.T) {
	g := validGraph()
	g.Delegations[0].Trigger = &SourceAnchor{
		AgentType: "codex",
		SessionID: "s-1",
		Precision: FactEvidence{State: EvidenceEstimated},
	}
	v := Validate(g)
	if !v.Has(IssueInvalidEvidence) {
		t.Fatalf("want invalid_evidence for reason-less estimated anchor: %+v", v.Issues)
	}
}
