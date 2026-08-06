package adaptertest

import (
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/capability"
)

func baseDecl(agent string) capability.AgentCapabilities {
	caps := make(map[capability.CapabilityID]capability.CapabilityDeclaration, 10)
	for _, id := range capability.BaselineIDs() {
		caps[id] = capability.Exact()
	}
	// Ops that need interfaces left exact only if we provide fakes in tests.
	caps[capability.CapabilityRealtime] = capability.Unsupported("x")
	caps[capability.CapabilityDelete] = capability.Unsupported("x")
	caps[capability.CapabilityTerminate] = capability.Unsupported("x")
	return capability.AgentCapabilities{
		AgentType: agent, DisplayName: "Agent", AdapterRevision: 1, Capabilities: caps,
		ResumeCommand: &capability.ResumeCommandDeclaration{Executable: agent, StandardArgs: []string{"resume", "{id}"}},
	}
}

func TestCheckCoverageFailsWhenExactMissingEvidence(t *testing.T) {
	decl := baseDecl("x")
	// tokens exact but no evidence and not in basic
	err := CheckCoverage(decl, nil, CoverageOptions{BasicSatisfied: DefaultBasicSatisfied()})
	if err == nil {
		t.Fatal("expected coverage failure")
	}
	if !strings.Contains(err.Error(), "tokens") {
		t.Fatalf("want tokens in error, got %v", err)
	}
}

func TestCheckCoverageRejectsUnsanitized(t *testing.T) {
	decl := baseDecl("x")
	// Mark all non-basic as unsupported so only sanitization fails if we add a bad case
	for _, id := range capability.BaselineIDs() {
		if id == capability.CapabilityDiscovery || id == capability.CapabilityReplay {
			continue
		}
		decl.Capabilities[id] = capability.Unsupported("n/a")
	}
	decl.ResumeCommand = nil
	err := CheckCoverage(decl, []EvidenceCase{{
		Scenario: "bad", Sanitized: false, Synthetic: true,
		Covers:    []capability.CapabilityID{capability.CapabilityTokens},
		NewReader: func(t *testing.T) Reader { return &fakeReader{agentType: "x", displayName: "X"} },
		Assert:    func(t *testing.T, r Reader) {},
	}}, CoverageOptions{BasicSatisfied: DefaultBasicSatisfied()})
	if err == nil || !strings.Contains(err.Error(), "Sanitized") {
		t.Fatalf("want sanitized error, got %v", err)
	}
}

func TestCheckCoverageRejectsUnknownCapability(t *testing.T) {
	decl := baseDecl("x")
	for _, id := range capability.BaselineIDs() {
		if id == capability.CapabilityDiscovery || id == capability.CapabilityReplay {
			continue
		}
		decl.Capabilities[id] = capability.Unsupported("n/a")
	}
	decl.ResumeCommand = nil
	err := CheckCoverage(decl, []EvidenceCase{{
		Scenario: "x", Sanitized: true, Synthetic: true,
		Covers:    []capability.CapabilityID{"not_a_real_cap"},
		NewReader: func(t *testing.T) Reader { return &fakeReader{agentType: "x", displayName: "X"} },
		Assert:    func(t *testing.T, r Reader) {},
	}}, CoverageOptions{BasicSatisfied: DefaultBasicSatisfied()})
	if err == nil || !strings.Contains(err.Error(), "unknown capability") {
		t.Fatalf("want unknown capability error, got %v", err)
	}
}

func TestCheckCoveragePassesWithFullEvidence(t *testing.T) {
	decl := baseDecl("x")
	// Provide one case per remaining exact cap
	var cases []EvidenceCase
	for _, id := range capability.BaselineIDs() {
		if id == capability.CapabilityDiscovery || id == capability.CapabilityReplay {
			continue
		}
		if decl.Capabilities[id].State != capability.CapabilityExact {
			continue
		}
		id := id
		cases = append(cases, EvidenceCase{
			Scenario: string(id) + "-case", Sanitized: true, Synthetic: true,
			Covers:    []capability.CapabilityID{id},
			NewReader: func(t *testing.T) Reader { return &fakeReader{agentType: "x", displayName: "X"} },
			Assert:    func(t *testing.T, r Reader) {},
		})
	}
	if err := CheckCoverage(decl, cases, CoverageOptions{BasicSatisfied: DefaultBasicSatisfied()}); err != nil {
		t.Fatal(err)
	}
}

func TestInt64Helper(t *testing.T) {
	p := Int64(42)
	if *p != 42 {
		t.Fatal(p)
	}
	_ = model.PresenceExact
}
