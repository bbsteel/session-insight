package capability

import "testing"

func baseResolved(static AgentCapabilities) SessionCapabilities {
	status := make(map[CapabilityID]SessionCapabilityStatus, 10)
	for _, id := range BaselineIDs() {
		status[id] = SessionFromStatic(static.Capabilities[id])
	}
	return SessionCapabilities{
		AgentType:       static.AgentType,
		AdapterRevision: static.AdapterRevision,
		Status:          status,
		Actions: map[CapabilityID]SessionActionStatus{
			CapabilityResume:    ActionAvailableStatus(),
			CapabilityDelete:    ActionAvailableStatus(),
			CapabilityTerminate: ActionUnavailableStatus(ReasonSessionNotLive),
		},
		Liveness: SessionLivenessStatus{
			IsLive: false, State: CapabilityEstimated, ReasonCode: ReasonTimestampHeuristic,
		},
	}
}

func fullStatic() AgentCapabilities {
	caps := make(map[CapabilityID]CapabilityDeclaration, 10)
	for _, id := range BaselineIDs() {
		caps[id] = Exact()
	}
	return AgentCapabilities{
		AgentType: "claude", DisplayName: "Claude Code", AdapterRevision: 1, Capabilities: caps,
		ResumeCommand: &ResumeCommandDeclaration{Executable: "claude", StandardArgs: []string{"--resume", "{id}"}},
	}
}

func TestValidateResolvedHappyPath(t *testing.T) {
	static := fullStatic()
	resolved := baseResolved(static)
	if errs := ValidateResolved(resolved, static); len(errs) != 0 {
		t.Fatalf("%v", errs)
	}
}

func TestValidateResolvedMissingCapability(t *testing.T) {
	static := fullStatic()
	resolved := baseResolved(static)
	delete(resolved.Status, CapabilityTokens)
	errs := ValidateResolved(resolved, static)
	found := false
	for _, e := range errs {
		if e.Code == CodeMissingCapability {
			found = true
		}
	}
	if !found {
		t.Fatalf("%v", errs)
	}
}

func TestValidateResolvedMissingNeedsReason(t *testing.T) {
	static := fullStatic()
	resolved := baseResolved(static)
	resolved.Status[CapabilityTokens] = SessionCapabilityStatus{State: CapabilityMissing}
	errs := ValidateResolved(resolved, static)
	found := false
	for _, e := range errs {
		if e.Code == CodeReasonRequired {
			found = true
		}
	}
	if !found {
		t.Fatalf("%v", errs)
	}
}

func TestValidateResolvedAgentTypeMismatch(t *testing.T) {
	static := fullStatic()
	resolved := baseResolved(static)
	resolved.AgentType = "other"
	errs := ValidateResolved(resolved, static)
	found := false
	for _, e := range errs {
		if e.Code == CodeAgentTypeMismatch {
			found = true
		}
	}
	if !found {
		t.Fatalf("%v", errs)
	}
}

func TestValidateResolvedRejectsNonActionableAction(t *testing.T) {
	static := fullStatic()
	resolved := baseResolved(static)
	resolved.Actions[CapabilityTokens] = ActionAvailableStatus()
	errs := ValidateResolved(resolved, static)
	found := false
	for _, e := range errs {
		if e.Code == CodeActionNotActionable {
			found = true
		}
	}
	if !found {
		t.Fatalf("%v", errs)
	}
}

func TestMissingConstructor(t *testing.T) {
	m := Missing(ReasonSessionNotFinalized)
	if m.State != CapabilityMissing || m.ReasonCode != ReasonSessionNotFinalized {
		t.Fatalf("%+v", m)
	}
}
