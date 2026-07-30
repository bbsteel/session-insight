package chrys

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/model"
)

// ReadCollaboration implements reader.CollaborationReader for the Chrys
// embedded-child archetype: a sub-agent is a complete transcript sidecar
// under <session>/sub_agents/sessions/, joined to the parent by the exact
// two-sided key parent function_call.call_id == sidecar
// meta.parent_provider_call_id.
//
// Mapping (frozen contract):
//   - identity: meta.parent_provider_call_id namespaced by the root session,
//     with the contract-accepted native fallback meta.invocation_id when the
//     call join key is absent;
//   - anchors: trigger uses the parent's ToolInvocation render event
//     (call-<call_id>) and result the matching function_result, both exact;
//     a recorded trigger timestamp that post-dates the child it launched
//     (checkpoint-rewrite corruption) is withheld and the anchor precision
//     downgraded to missing/timestamp_contradiction — the join identity
//     stays exact;
//   - status/timing: meta.status, meta.created_at, and meta.updated_at are
//     parsed and normalized (the render path leaves them unused, but the
//     collaboration contract consumes them);
//   - content: the full embedded transcript — exact;
//   - no BackingSessionRef: an embedded transcript is not an independent
//     Session and is never resumable/deletable on its own.
//
// The delegated prompt in the parent's function_call arguments is never
// stored as TaskSummary; only a source-recorded summary would qualify, and
// Chrys records none.
func (r *ChrysReader) ReadCollaboration(ctx context.Context, root model.Session) (collaboration.CollaborationGraph, error) {
	if root.AgentType != "chrys" {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"chrys collaboration: root session agent type %q is not chrys", root.AgentType)
	}
	if !validSessionID(root.ID) {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"chrys collaboration: invalid root session id %q", root.ID)
	}
	if err := ctx.Err(); err != nil {
		return collaboration.CollaborationGraph{}, err
	}

	sessionDir := filepath.Join(r.sessionsDir, root.ID)
	sf, err := readEffectiveSession(sessionDir)
	if err != nil {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"chrys collaboration: root session %q: %w", root.ID, err)
	}

	// Index the parent's side of the join: function_call anchors (in message
	// order, so children order by launch) and function_result anchors.
	calls := map[string]chrysCallAnchor{}
	var callOrder []string
	results := map[string]chrysCallAnchor{}
	for _, m := range sf.State.Messages {
		ts := m.createdAt()
		for _, c := range m.Contents {
			switch c.Type {
			case "function_call":
				if c.CallID == "" {
					continue
				}
				if _, seen := calls[c.CallID]; !seen {
					callOrder = append(callOrder, c.CallID)
				}
				calls[c.CallID] = chrysCallAnchor{toolName: c.Name, ts: ts}
			case "function_result":
				if c.CallID != "" {
					results[c.CallID] = chrysCallAnchor{ts: ts}
				}
			}
		}
	}

	graph := collaboration.CollaborationGraph{
		RootAgentType: "chrys",
		RootSessionID: root.ID,
		Revision:      model.SessionRevision(root),
		Completeness:  collaboration.ExactFact(),
		Invocations:   []collaboration.AgentInvocation{chrysRootInvocation(root)},
	}
	rootInvID := graph.Invocations[0].ID

	// Discover embedded children. Iteration follows the parent's launch
	// order; children whose parent_provider_call_id has no matching parent
	// call (or only the invocation_id fallback) sort after, by native ID, so
	// output is deterministic.
	subIndex := buildSubagentIndex(sessionDir)
	children, err := r.discoverEmbeddedChildren(ctx, sessionDir, subIndex, callOrder)
	if err != nil {
		return collaboration.CollaborationGraph{}, err
	}
	for _, child := range children {
		inv, del := chrysChildCollaboration(root.ID, rootInvID, child, calls, results)
		graph.Invocations = append(graph.Invocations, inv)
		if del != nil {
			graph.Delegations = append(graph.Delegations, *del)
		}
	}

	if v := collaboration.Validate(&graph); !v.OK() {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"chrys collaboration: normalized graph violates the contract: %s", v.Issues[0].Detail)
	}
	return graph, nil
}

// chrysCallAnchor is one side of the two-sided call_id join: the parent
// function_call (with its timestamp) or the matching function_result.
type chrysCallAnchor struct {
	toolName string
	ts       time.Time
}

// chrysEmbeddedChild is one parsed sub-agent sidecar plus its join key.
type chrysEmbeddedChild struct {
	meta       sessionMeta
	nativeID   string // parent_provider_call_id, or invocation_id fallback
	launchRank int    // position in the parent's call order; len(order) when unjoined
}

// chrysRootInvocation builds the one deterministic root invocation. Status
// follows the same rule as the other adapters: running on a positive live
// signal, otherwise unknown (Chrys records no session-level completion).
func chrysRootInvocation(root model.Session) collaboration.AgentInvocation {
	status := collaboration.StatusUnknown
	if root.IsLive || model.IsSessionLive(root.UpdatedAt) {
		status = collaboration.StatusRunning
	}
	return collaboration.AgentInvocation{
		ID:               collaboration.RootInvocationID("chrys", root.ID),
		DisplayName:      "chrys main agent",
		AgentType:        "chrys",
		Status:           status,
		TimePrecision:    collaboration.ExactFact(),
		ContentPrecision: collaboration.ExactFact(),
		SourceIdentity: collaboration.SourceIdentity{
			Kind:     collaboration.IdentityRootSession,
			NativeID: root.ID,
		},
	}
}

// discoverEmbeddedChildren reads every sub-agent sidecar under the session
// directory. Cancellation-aware; malformed sidecars are skipped (a child
// without any stable native ID cannot become an invocation).
func (r *ChrysReader) discoverEmbeddedChildren(ctx context.Context, sessionDir string, subIndex map[string]string, callOrder []string) ([]chrysEmbeddedChild, error) {
	rank := map[string]int{}
	for i, callID := range callOrder {
		rank[callID] = i
	}

	// Candidate paths: the call-keyed index plus any sidecar the index
	// skipped (invocation_id-only fallback children).
	paths := map[string]bool{}
	for _, p := range subIndex {
		paths[p] = true
	}
	if entries, err := filepath.Glob(filepath.Join(sessionDir, "sub_agents", "sessions", "*.json")); err == nil {
		for _, p := range entries {
			paths[p] = true
		}
	}
	ordered := make([]string, 0, len(paths))
	for p := range paths {
		ordered = append(ordered, p)
	}
	sort.Strings(ordered)

	var children []chrysEmbeddedChild
	seenNative := map[string]bool{}
	for _, path := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sf, err := readSessionFile(path)
		if err != nil {
			continue // malformed sidecar: not a readable child transcript
		}
		nativeID := sf.Meta.ParentProviderCallID
		if nativeID == "" {
			// Contract-accepted native fallback: the source-recorded
			// invocation_id. Without either key the record has no stable
			// identity and is left out.
			nativeID = sf.Meta.InvocationID
		}
		if nativeID == "" {
			continue
		}
		// Two sidecars claiming one native ID are malformed source; keeping
		// both would collide on one invocation ID and fail the whole read.
		// The first in deterministic (sorted path) order wins, matching
		// Validate's first-occurrence-kept semantics.
		if seenNative[nativeID] {
			continue
		}
		seenNative[nativeID] = true
		r, ok := rank[sf.Meta.ParentProviderCallID]
		if !ok {
			r = len(callOrder)
		}
		children = append(children, chrysEmbeddedChild{meta: sf.Meta, nativeID: nativeID, launchRank: r})
	}
	sort.SliceStable(children, func(i, j int) bool {
		if children[i].launchRank != children[j].launchRank {
			return children[i].launchRank < children[j].launchRank
		}
		return children[i].nativeID < children[j].nativeID
	})
	return children, nil
}

// chrysChildCollaboration maps one embedded child to its invocation and —
// when the two-sided join holds — its delegation. A child whose
// parent_provider_call_id matches no parent function_call gets no delegation;
// validation attaches it to the Unlinked group and its transcript is
// preserved on the graph.
func chrysChildCollaboration(rootSessionID, rootInvID string, child chrysEmbeddedChild, calls, results map[string]chrysCallAnchor) (collaboration.AgentInvocation, *collaboration.Delegation) {
	meta := child.meta
	childInvID := collaboration.ChildInvocationID("chrys", rootSessionID, child.nativeID)

	displayName := meta.AgentDisplayName
	if displayName == "" {
		displayName = meta.ToolName
	}
	if displayName == "" {
		displayName = "chrys child agent"
	}

	startedAt := parseTS(meta.CreatedAt)
	endedAt := parseTS(meta.UpdatedAt)
	status := normalizeChrysStatus(meta.Status)
	// updated_at is an end boundary only for a terminal status; for a live or
	// unknown child it is just the last save.
	hasEnd := !endedAt.IsZero() && isTerminalChrysStatus(status)

	inv := collaboration.AgentInvocation{
		ID:               childInvID,
		DisplayName:      displayName,
		AgentType:        "chrys",
		RoleLabel:        meta.ToolName,
		Status:           status,
		TimePrecision:    chrysTimePrecision(!startedAt.IsZero(), hasEnd),
		ContentPrecision: collaboration.ExactFact(), // full embedded transcript
		SourceIdentity: collaboration.SourceIdentity{
			Kind:     collaboration.IdentityProviderCallID,
			NativeID: child.nativeID,
		},
	}
	if meta.InvocationID != "" {
		inv.SourceIdentity.Attributes = map[string]string{"invocation_id": meta.InvocationID}
	}
	if !startedAt.IsZero() {
		inv.StartedAt = &startedAt
	}
	if hasEnd {
		inv.EndedAt = &endedAt
	}

	callID := meta.ParentProviderCallID
	call, joined := calls[callID]
	if !joined {
		return inv, nil
	}

	trigger := &collaboration.SourceAnchor{
		AgentType:  "chrys",
		SessionID:  rootSessionID,
		EventID:    "call-" + callID, // stable: derived from the call_id, not positional
		ToolCallID: callID,
		Precision:  collaboration.ExactFact(),
	}
	if !call.ts.IsZero() {
		if chrysTriggerContradictsChild(call.ts, startedAt, endedAt, hasEnd) {
			// Chrys's checkpoint rewrite can collapse a message's
			// _chrys_created_at to the rewrite time; a launch anchor dated
			// after the child it launched is causally impossible. The join
			// identity stays exact, but the timestamp is withheld rather
			// than emitted as fact (and would otherwise stretch downstream
			// time domains).
			trigger.Precision = collaboration.FactEvidence{
				State:      collaboration.EvidenceMissing,
				ReasonCode: collaboration.ReasonTimestampContradiction,
			}
		} else {
			ts := call.ts
			trigger.Timestamp = &ts
		}
	}
	evidence := collaboration.DelegationEvidence{
		Trigger: collaboration.ExactFact(),
		Timing:  inv.TimePrecision,
		Task: collaboration.FactEvidence{
			// The delegated prompt in the call arguments is never stored as a
			// task summary; Chrys records no separate summary.
			State:      collaboration.EvidenceMissing,
			ReasonCode: collaboration.ReasonSourceNotRecorded,
		},
	}
	del := &collaboration.Delegation{
		ID:                 collaboration.DelegationIDFor(rootInvID, childInvID),
		ParentInvocationID: rootInvID,
		ChildInvocationID:  childInvID,
		Trigger:            trigger,
		ExecutionMode:      collaboration.ExecutionUnknown,
		Evidence:           evidence,
	}
	if result, ok := results[callID]; ok {
		anchor := &collaboration.SourceAnchor{
			AgentType:  "chrys",
			SessionID:  rootSessionID,
			ToolCallID: callID,
			Precision:  collaboration.ExactFact(),
		}
		if !result.ts.IsZero() {
			ts := result.ts
			anchor.Timestamp = &ts
		}
		del.Result = anchor
		del.Evidence.Result = collaboration.ExactFact()
	} else {
		del.Evidence.Result = collaboration.FactEvidence{
			State:      collaboration.EvidenceMissing,
			ReasonCode: collaboration.ReasonCompletionNotRecorded,
		}
	}
	return inv, del
}

// chrysTriggerContradictsChild reports whether a parent-side trigger
// timestamp is causally impossible: the launch must precede the child's
// recorded creation (meta.created_at), or — when no start is known — its
// recorded terminal end. Equality is sound (launch and creation can share
// one clock tick).
func chrysTriggerContradictsChild(trigger, startedAt, endedAt time.Time, hasEnd bool) bool {
	if !startedAt.IsZero() {
		return trigger.After(startedAt)
	}
	return hasEnd && trigger.After(endedAt)
}

// chrysTimePrecision derives the timing evidence state from which boundaries
// the source actually recorded.
func chrysTimePrecision(hasStart, hasEnd bool) collaboration.FactEvidence {
	switch {
	case hasStart && hasEnd:
		return collaboration.ExactFact()
	case hasStart:
		return collaboration.FactEvidence{
			State:      collaboration.EvidenceEstimated,
			ReasonCode: collaboration.ReasonCompletionNotRecorded,
		}
	default:
		return collaboration.FactEvidence{
			State:      collaboration.EvidenceMissing,
			ReasonCode: collaboration.ReasonSourceNotRecorded,
		}
	}
}

// normalizeChrysStatus maps the sidecar's recorded meta.status onto the
// normalized set. Unrecorded or unrecognized values stay unknown — never
// collapsed into completed or failed.
func normalizeChrysStatus(s string) collaboration.InvocationStatus {
	switch s {
	case "completed":
		return collaboration.StatusCompleted
	case "failed", "error":
		return collaboration.StatusFailed
	case "cancelled", "canceled", "interrupted", "aborted":
		return collaboration.StatusCancelled
	case "running", "in_progress":
		return collaboration.StatusRunning
	case "pending":
		return collaboration.StatusPending
	default:
		return collaboration.StatusUnknown
	}
}

func isTerminalChrysStatus(s collaboration.InvocationStatus) bool {
	switch s {
	case collaboration.StatusCompleted, collaboration.StatusFailed, collaboration.StatusCancelled:
		return true
	default:
		return false
	}
}
