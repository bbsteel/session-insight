package reader

import (
	"fmt"
	"strings"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/capability"
)

// ResolveSessionCapabilities maps a static Agent declaration onto one
// already-loaded session. It is pure with respect to storage: it does not
// ListSessions, reparse transcripts, call SessionProcesses, or mutate files.
//
// source may implement optional structural interfaces (SessionLivenessProvider,
// LiveRevisionProvider). When absent, liveness falls back to the timestamp
// window and realtime stays at the static declaration.
//
// The returned value is always a new structure; static is never mutated.
func ResolveSessionCapabilities(
	source any,
	detail *model.SessionDetail,
	static capability.AgentCapabilities,
) (capability.SessionCapabilities, error) {
	if detail == nil {
		return capability.SessionCapabilities{}, fmt.Errorf("session detail is nil")
	}
	if errs := capability.ValidateStatic(static); len(errs) != 0 {
		return capability.SessionCapabilities{}, fmt.Errorf("static declaration invalid: %w", errs)
	}
	if detail.AgentType != "" && detail.AgentType != static.AgentType {
		return capability.SessionCapabilities{}, fmt.Errorf(
			"detail agent_type %q does not match static %q", detail.AgentType, static.AgentType)
	}

	// Snapshot static map keys so we never rely on shared map mutation.
	staticCaps := copyStaticCaps(static.Capabilities)

	status := make(map[capability.CapabilityID]capability.SessionCapabilityStatus, len(capability.BaselineIDs()))
	for _, id := range capability.BaselineIDs() {
		decl := staticCaps[id]
		status[id] = resolveOne(id, decl, source, detail, static.AgentType)
	}

	live := ResolveLiveness(source, detail.Session)
	actions := resolveActions(status, live, detail, static.AgentType)

	out := capability.SessionCapabilities{
		AgentType:       static.AgentType,
		AdapterRevision: static.AdapterRevision,
		Status:          status,
		Actions:         actions,
		Liveness:        live,
	}
	if errs := capability.ValidateResolved(out, static); len(errs) != 0 {
		return capability.SessionCapabilities{}, fmt.Errorf("resolved capabilities invalid: %w", errs)
	}
	return out, nil
}

func copyStaticCaps(in map[capability.CapabilityID]capability.CapabilityDeclaration) map[capability.CapabilityID]capability.CapabilityDeclaration {
	out := make(map[capability.CapabilityID]capability.CapabilityDeclaration, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func resolveOne(
	id capability.CapabilityID,
	decl capability.CapabilityDeclaration,
	source any,
	detail *model.SessionDetail,
	canonicalAgentType string,
) capability.SessionCapabilityStatus {
	// Unsupported / not_applicable never change at session level.
	switch decl.State {
	case capability.CapabilityUnsupported, capability.CapabilityNotApplicable:
		return capability.SessionFromStatic(decl)
	}

	switch id {
	case capability.CapabilityDiscovery, capability.CapabilityReplay:
		// Successfully loaded detail keeps the static state.
		return capability.SessionFromStatic(decl)

	case capability.CapabilityRealtime:
		return resolveRealtime(decl, source)

	case capability.CapabilityTokens:
		return resolveTokens(decl, detail)

	case capability.CapabilityToolResults, capability.CapabilityDiff, capability.CapabilitySubtasks:
		// Zero tools/edits/subtasks are valid exact results; do not invent missing.
		return capability.SessionFromStatic(decl)

	case capability.CapabilityResume:
		return resolveResume(decl, detail, canonicalAgentType)

	case capability.CapabilityDelete, capability.CapabilityTerminate:
		// Capability describes Agent support; liveness affects actions only.
		return capability.SessionFromStatic(decl)

	default:
		return capability.SessionFromStatic(decl)
	}
}

func resolveRealtime(decl capability.CapabilityDeclaration, source any) capability.SessionCapabilityStatus {
	// Realtime describes persisted content revision support, not process liveness.
	// If the reader cannot provide LiveRevision while static claims exact, the
	// session source cannot surface a usable revision cheaply — downgrade.
	if _, ok := source.(LiveRevisionProvider); ok {
		return capability.SessionFromStatic(decl)
	}
	if decl.State == capability.CapabilityExact {
		return capability.SessionEstimated(capability.ReasonRevisionUnavailable)
	}
	return capability.SessionFromStatic(decl)
}

func resolveTokens(decl capability.CapabilityDeclaration, detail *model.SessionDetail) capability.SessionCapabilityStatus {
	// Static estimated must never promote to exact (ValidateResolved forbids it).
	// Session billing/turn presence can only confirm or further downgrade.
	keepEstimated := func() capability.SessionCapabilityStatus {
		reason := decl.ReasonCode
		if reason == "" {
			reason = capability.ReasonTimestampHeuristic
		}
		return capability.SessionEstimated(reason)
	}
	maybeExact := func() capability.SessionCapabilityStatus {
		if decl.State == capability.CapabilityEstimated {
			return keepEstimated()
		}
		return capability.SessionExact()
	}

	if detail.Billing != nil {
		switch detail.Billing.Precision {
		case model.PrecisionExact:
			// Exact zero with PresenceExact is still exact when static allows it.
			return maybeExact()
		case model.PrecisionEstimated:
			return keepEstimated()
		case model.PrecisionMissing:
			// Agent normally records a bill but this session never wrote final usage.
			return capability.Missing(capability.ReasonSessionNotFinalized)
		}
	}

	// No session bill: turn-level exact presence (Claude-style) is exact only
	// when static is not estimated.
	if hasExactTurnTokenPresence(detail) {
		return maybeExact()
	}

	// Static estimated without stronger evidence stays estimated.
	if decl.State == capability.CapabilityEstimated {
		return keepEstimated()
	}

	// Static exact but no structured token evidence on this session.
	return capability.Missing(capability.ReasonSourceNotRecorded)
}

func hasExactTurnTokenPresence(detail *model.SessionDetail) bool {
	for _, tr := range detail.Turns {
		p := tr.TokenUsage.Present
		if p.Input == model.PresenceExact || p.Output == model.PresenceExact ||
			p.CacheRead == model.PresenceExact || p.CacheWrite == model.PresenceExact {
			return true
		}
	}
	return false
}

func resolveResume(decl capability.CapabilityDeclaration, detail *model.SessionDetail, canonicalAgentType string) capability.SessionCapabilityStatus {
	// Match frontend resumeCommands: resume_id || id for Agents whose CLI
	// accepts the session UUID (Claude, Grok, OpenCode, Chrys, …). Only Codex
	// forbids falling back to the storage/file id (rollout filename stem).
	if resumeCLIIdentity(detail, canonicalAgentType) == "" {
		return capability.Missing(capability.ReasonResumeIDMissing)
	}
	return capability.SessionFromStatic(decl)
}

// resumeCLIIdentity is the argument a user would pass to the Agent CLI to
// resume this session. Prefer an explicit ResumeID; otherwise use Session.ID
// except for Codex, where the UI id is not a valid CLI resume id.
//
// canonicalAgentType is the static declaration AgentType (preferred when
// detail.AgentType is empty — ResolveSessionCapabilities allows that).
func resumeCLIIdentity(detail *model.SessionDetail, canonicalAgentType string) string {
	if detail == nil {
		return ""
	}
	if detail.ResumeID != "" {
		return detail.ResumeID
	}
	agentType := detail.AgentType
	if agentType == "" {
		agentType = canonicalAgentType
	}
	if strings.EqualFold(agentType, "codex") {
		return ""
	}
	return detail.ID
}

// ResolveLiveness explains how IsLive was obtained without changing the
// boolean contract of IsSessionLive for existing callers.
func ResolveLiveness(source any, session model.Session) capability.SessionLivenessStatus {
	// Outside the window: never live. Quality is still the timestamp heuristic
	// when no provider is consulted (matches IsSessionLive short-circuit).
	if !model.IsSessionLive(session.UpdatedAt) {
		return capability.SessionLivenessStatus{
			IsLive:     false,
			State:      capability.CapabilityEstimated,
			ReasonCode: capability.ReasonTimestampHeuristic,
		}
	}

	if provider, ok := source.(SessionLivenessProvider); ok {
		live, err := provider.SessionLive(session.ID)
		if err == nil {
			if live {
				return capability.SessionLivenessStatus{
					IsLive: true,
					State:  capability.CapabilityExact,
				}
			}
			return capability.SessionLivenessStatus{
				IsLive:     false,
				State:      capability.CapabilityExact,
				ReasonCode: capability.ReasonSessionNotLive,
			}
		}
		// Provider failed: fall back to timestamp (still inside window).
	}

	return capability.SessionLivenessStatus{
		IsLive:     true,
		State:      capability.CapabilityEstimated,
		ReasonCode: capability.ReasonTimestampHeuristic,
	}
}

func resolveActions(
	status map[capability.CapabilityID]capability.SessionCapabilityStatus,
	live capability.SessionLivenessStatus,
	detail *model.SessionDetail,
	canonicalAgentType string,
) map[capability.CapabilityID]capability.SessionActionStatus {
	actions := make(map[capability.CapabilityID]capability.SessionActionStatus, 3)

	// Resume
	res := status[capability.CapabilityResume]
	switch res.State {
	case capability.CapabilityExact, capability.CapabilityEstimated:
		if resumeCLIIdentity(detail, canonicalAgentType) != "" {
			actions[capability.CapabilityResume] = capability.ActionAvailableStatus()
		} else {
			actions[capability.CapabilityResume] = capability.ActionUnavailableStatus(capability.ReasonResumeIDMissing)
		}
	default:
		reason := res.ReasonCode
		if reason == "" {
			reason = capability.ReasonAdapterNotImplemented
		}
		actions[capability.CapabilityResume] = capability.ActionUnavailableStatus(reason)
	}

	// Delete
	del := status[capability.CapabilityDelete]
	switch del.State {
	case capability.CapabilityUnsupported, capability.CapabilityNotApplicable:
		reason := del.ReasonCode
		if reason == "" {
			reason = capability.ReasonAdapterNotImplemented
		}
		actions[capability.CapabilityDelete] = capability.ActionUnavailableStatus(reason)
	case capability.CapabilityMissing:
		actions[capability.CapabilityDelete] = capability.ActionUnavailableStatus(del.ReasonCode)
	default:
		if live.IsLive {
			actions[capability.CapabilityDelete] = capability.ActionUnavailableStatus(capability.ReasonSessionRunning)
		} else {
			actions[capability.CapabilityDelete] = capability.ActionAvailableStatus()
		}
	}

	// Terminate — never scan PIDs here.
	term := status[capability.CapabilityTerminate]
	switch term.State {
	case capability.CapabilityUnsupported, capability.CapabilityNotApplicable:
		reason := term.ReasonCode
		if reason == "" {
			reason = capability.ReasonExactPIDUnavailable
		}
		actions[capability.CapabilityTerminate] = capability.ActionUnavailableStatus(reason)
	case capability.CapabilityMissing:
		actions[capability.CapabilityTerminate] = capability.ActionUnavailableStatus(term.ReasonCode)
	default:
		if !live.IsLive {
			actions[capability.CapabilityTerminate] = capability.ActionUnavailableStatus(capability.ReasonSessionNotLive)
		} else {
			actions[capability.CapabilityTerminate] = capability.ActionCheckRequiredStatus()
		}
	}

	return actions
}
