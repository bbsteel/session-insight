package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/model"
)

// ReadCollaboration implements reader.CollaborationReader for the Codex
// standalone-child archetype: a subagent is a full standalone rollout whose
// session_meta carries parent_thread_id + thread_source=subagent lineage.
//
// Mapping (frozen contract):
//   - identity: native payload.id namespaced by the root session; the rollout
//     file stem stays in SourceIdentity.Attributes;
//   - lineage: child parent_thread_id == root payload.id (ResumeID);
//   - backing: the child rollout is a real native Session, so it carries
//     BackingSessionRef{agent_type: "codex", session_id: <rollout stem>};
//   - anchors: Codex records no launch/result correlation in the parent
//     stream, so Trigger/Result stay absent with source_not_recorded instead
//     of synthesized anchors;
//   - status: unknown — the source records no child completion signal, and
//     unknown beats inferring success from a closed file.
//
// The graph is defined for the root Session only. A standalone child remains
// renderable through its backing Session, but passing a child as root is an
// explicit deterministic error, never a second root graph.
func (r *CodexReader) ReadCollaboration(ctx context.Context, root model.Session) (collaboration.CollaborationGraph, error) {
	if root.AgentType != "codex" {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"codex collaboration: root session agent type %q is not codex", root.AgentType)
	}
	if root.IsSubagent || root.ParentSessionID != "" {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"codex collaboration: %s is a subagent child of %s; collaboration graphs are defined for root sessions only",
			root.ID, root.ParentSessionID)
	}
	if err := ctx.Err(); err != nil {
		return collaboration.CollaborationGraph{}, err
	}

	// Child lineage joins on the root's native payload.id. Trust the indexed
	// ResumeID; fall back to re-reading the root rollout when it is absent.
	rootNative := root.ResumeID
	if rootNative == "" {
		if path := r.findSessionFile(root.ID); path != "" {
			if sess, ok := readSessionMeta(path); ok {
				rootNative = sess.ResumeID
			}
		}
	}

	graph := collaboration.CollaborationGraph{
		RootAgentType: "codex",
		RootSessionID: root.ID,
		Revision:      model.SessionRevision(root),
		// Identity and lineage are exact, but the source records no delegation
		// anchors; the graph is as complete as the recorded evidence allows.
		Completeness: collaboration.FactEvidence{
			State:      collaboration.EvidenceEstimated,
			ReasonCode: collaboration.ReasonSourceNotRecorded,
		},
		Invocations: []collaboration.AgentInvocation{codexRootInvocation(root)},
	}

	if rootNative != "" {
		children, err := r.discoverChildren(ctx, rootNative)
		if err != nil {
			return collaboration.CollaborationGraph{}, err
		}
		rootID := graph.Invocations[0].ID
		for _, child := range children {
			inv, del := codexChildCollaboration(root.ID, rootID, child)
			graph.Invocations = append(graph.Invocations, inv)
			graph.Delegations = append(graph.Delegations, del)
		}
	}

	if v := collaboration.Validate(&graph); !v.OK() {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"codex collaboration: normalized graph violates the contract: %s", v.Issues[0].Detail)
	}
	return graph, nil
}

// codexRootInvocation builds the one deterministic root invocation. The root
// has exact session timing and content; its status is running only with a
// positive live signal, otherwise unknown (no session-level completion event
// exists in the Codex rollout format).
func codexRootInvocation(root model.Session) collaboration.AgentInvocation {
	status := collaboration.StatusUnknown
	if root.IsLive || model.IsSessionLive(root.UpdatedAt) {
		status = collaboration.StatusRunning
	}
	return collaboration.AgentInvocation{
		ID:               collaboration.RootInvocationID("codex", root.ID),
		DisplayName:      "codex main agent",
		AgentType:        "codex",
		Status:           status,
		TimePrecision:    collaboration.ExactFact(),
		ContentPrecision: collaboration.ExactFact(),
		SourceIdentity: collaboration.SourceIdentity{
			Kind:     collaboration.IdentityRootSession,
			NativeID: root.ID,
		},
	}
}

// discoverChildren finds subagent rollouts whose parent_thread_id equals the
// root's native payload.id. Discovery is bounded (readSessionMeta reads only
// the head/tail of each rollout) and cancellation-aware. Children are
// reported faithfully; hiding them from the root Session list is the shared
// backend's concern, not the adapter's.
func (r *CodexReader) discoverChildren(ctx context.Context, rootNative string) ([]model.Session, error) {
	var files []string
	err := filepath.WalkDir(r.sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files) // deterministic discovery order independent of WalkDir

	var children []model.Session
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sess, ok := readSessionMeta(path)
		if !ok || !sess.IsSubagent {
			continue
		}
		// A subagent rollout without its own payload.id has no stable native
		// identity; it cannot become an invocation (positional syntheses are
		// forbidden), so it is left out rather than guessed. The
		// readSessionMeta fallback reports the parent's session_id as
		// ResumeID for such rollouts — detect that case: a child's own
		// payload.id can never legitimately equal its parent_thread_id.
		if sess.ResumeID == "" || sess.ResumeID == sess.ParentSessionID {
			continue
		}
		if sess.ParentSessionID == rootNative {
			children = append(children, sess)
		}
	}
	sort.Slice(children, func(i, j int) bool {
		if !children[i].CreatedAt.Equal(children[j].CreatedAt) {
			return children[i].CreatedAt.Before(children[j].CreatedAt)
		}
		return children[i].ID < children[j].ID
	})
	return children, nil
}

// codexChildCollaboration maps one child rollout to its invocation and the
// root→child delegation. Missing facts stay missing: no launch/result anchor
// is synthesized, and the open end is expressed through precision plus
// status, never a guessed EndedAt.
func codexChildCollaboration(rootSessionID, rootInvocationID string, child model.Session) (collaboration.AgentInvocation, collaboration.Delegation) {
	childInvID := collaboration.ChildInvocationID("codex", rootSessionID, child.ResumeID)

	role := ""
	if child.AgentPath != "" {
		role = child.AgentPath[strings.LastIndex(strings.TrimSuffix(child.AgentPath, "/"), "/")+1:]
	}
	displayName := role
	if displayName == "" {
		displayName = child.Name
	}
	if displayName == "" {
		displayName = "codex child agent"
	}

	startedAt := child.CreatedAt
	inv := collaboration.AgentInvocation{
		ID:               childInvID,
		DisplayName:      displayName,
		AgentType:        "codex",
		RoleLabel:        role,
		Status:           collaboration.StatusUnknown,
		TimePrecision:    collaboration.ExactFact(),
		ContentPrecision: collaboration.ExactFact(), // full standalone transcript
		BackingSession:   &collaboration.BackingSessionRef{AgentType: "codex", SessionID: child.ID},
		SourceIdentity: collaboration.SourceIdentity{
			Kind:       collaboration.IdentityPayloadID,
			NativeID:   child.ResumeID,
			Attributes: map[string]string{"rollout_stem": child.ID},
		},
	}
	if !startedAt.IsZero() {
		inv.StartedAt = &startedAt
		// Start exact, completion not recorded: the end boundary stays open.
		inv.TimePrecision = collaboration.FactEvidence{
			State:      collaboration.EvidenceEstimated,
			ReasonCode: collaboration.ReasonCompletionNotRecorded,
		}
	}

	timing := collaboration.ExactFact()
	if inv.TimePrecision.State != collaboration.EvidenceExact {
		timing = inv.TimePrecision
	}
	del := collaboration.Delegation{
		ID:                 collaboration.DelegationIDFor(rootInvocationID, childInvID),
		ParentInvocationID: rootInvocationID,
		ChildInvocationID:  childInvID,
		ExecutionMode:      collaboration.ExecutionUnknown,
		Evidence: collaboration.DelegationEvidence{
			Trigger: collaboration.FactEvidence{
				State:      collaboration.EvidenceMissing,
				ReasonCode: collaboration.ReasonSourceNotRecorded,
			},
			Timing: timing,
			Task: collaboration.FactEvidence{
				State:      collaboration.EvidenceMissing,
				ReasonCode: collaboration.ReasonSourceNotRecorded,
			},
			Result: collaboration.FactEvidence{
				State:      collaboration.EvidenceMissing,
				ReasonCode: collaboration.ReasonSourceNotRecorded,
			},
		},
	}
	return inv, del
}

// childInvocationID resolves the deterministic collaboration invocation ID
// for a subagent rollout rendered through its backing Session, or "" when
// the session is not a resolvable child (root sessions render unmarked:
// absent InvocationID means the root invocation).
func (r *CodexReader) childInvocationID(sessionID string) string {
	path := r.findSessionFile(sessionID)
	if path == "" {
		return ""
	}
	sess, ok := readSessionMeta(path)
	if !ok || !sess.IsSubagent || sess.ParentSessionID == "" || sess.ResumeID == "" ||
		sess.ResumeID == sess.ParentSessionID {
		return ""
	}
	rootStem := r.findRolloutStemByNativeID(sess.ParentSessionID)
	if rootStem == "" {
		return ""
	}
	return collaboration.ChildInvocationID("codex", rootStem, sess.ResumeID)
}

// findRolloutStemByNativeID locates the rollout whose native payload.id is
// nativeID and returns its session ID (rollout file stem). Bounded like
// discoverChildren: readSessionMeta reads only head/tail lines.
func (r *CodexReader) findRolloutStemByNativeID(nativeID string) string {
	found := ""
	filepath.WalkDir(r.sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		if sess, ok := readSessionMeta(path); ok && sess.ResumeID == nativeID {
			found = sess.ID
		}
		return nil
	})
	return found
}
