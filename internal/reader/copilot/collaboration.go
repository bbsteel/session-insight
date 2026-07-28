package copilot

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/model"
)

// ReadCollaboration implements reader.CollaborationReader for the Copilot
// lifecycle-only archetype: a child invocation exists only as
// subagent.started / subagent.completed lifecycle events plus the delegating
// task tool call in the parent's events.jsonl. There is no independent child
// transcript, resume, or delete.
//
// Mapping (frozen contract):
//   - identity: the parent task call's toolCallId, namespaced by the root
//     session; subagent_id/agentDisplayName are labels, never identity;
//   - anchors: trigger is the task tool.execution_start, result the
//     subagent.completed lifecycle event, both exact via toolCallId;
//   - timing: lifecycle timestamps are exact; a started-without-completed
//     invocation keeps an open end (no guessed EndedAt);
//   - status: completed on subagent.completed; otherwise orphaned only when
//     the root is no longer live, running/pending while it is;
//   - content: reconstructed aggregate windows only — estimated with reason
//     aggregate_window, never presented as an exact child transcript;
//   - task: the source-recorded description may become TaskSummary; the full
//     delegation prompt may not;
//   - no BackingSessionRef.
func (r *CopilotReader) ReadCollaboration(ctx context.Context, root model.Session) (collaboration.CollaborationGraph, error) {
	if root.AgentType != "copilot" {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"copilot collaboration: root session agent type %q is not copilot", root.AgentType)
	}
	if !validSessionID(root.ID) {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"copilot collaboration: invalid root session id %q", root.ID)
	}
	if err := ctx.Err(); err != nil {
		return collaboration.CollaborationGraph{}, err
	}

	eventsPath := filepath.Join(r.sessionDir, root.ID, "events.jsonl")
	parsed, err := parseCollaborationEvents(ctx, eventsPath)
	if err != nil {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"copilot collaboration: root session %q: %w", root.ID, err)
	}

	live := root.IsLive || model.IsSessionLive(root.UpdatedAt)

	graph := collaboration.CollaborationGraph{
		RootAgentType: "copilot",
		RootSessionID: root.ID,
		Revision:      model.SessionRevision(root),
		Completeness:  collaboration.ExactFact(),
		Invocations:   []collaboration.AgentInvocation{copilotRootInvocation(root, parsed.shutdown, live)},
	}
	rootInvID := graph.Invocations[0].ID

	for _, child := range parsed.children {
		inv, del := copilotChildCollaboration(root.ID, rootInvID, child, live)
		graph.Invocations = append(graph.Invocations, inv)
		graph.Delegations = append(graph.Delegations, del)
	}

	if v := collaboration.Validate(&graph); !v.OK() {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"copilot collaboration: normalized graph violates the contract: %s", v.Issues[0].Detail)
	}
	return graph, nil
}

// copilotLifecycleChild holds the lifecycle and delegation facts for one
// toolCallId, in first-observed order.
type copilotLifecycleChild struct {
	toolCallID  string
	displayName string // agentDisplayName label from subagent.started
	roleName    string // task arguments.name
	description string // task arguments.description (source-recorded summary)
	mode        string // task arguments.mode (sync/async)
	hasTask     bool
	taskTS      time.Time
	hasStart    bool
	started     time.Time
	hasComplete bool
	completed   time.Time
}

// copilotCollaborationEvents is the single-scan parse of the parent stream.
type copilotCollaborationEvents struct {
	children []*copilotLifecycleChild
	shutdown bool
}

// parseCollaborationEvents scans events.jsonl once, keying all subagent
// facts on toolCallId. Malformed lines are skipped, matching the other
// Copilot parse paths. Cancellation-aware.
func parseCollaborationEvents(ctx context.Context, path string) (*copilotCollaborationEvents, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	byID := map[string]*copilotLifecycleChild{}
	var order []string
	childFor := func(tc string) *copilotLifecycleChild {
		c := byID[tc]
		if c == nil {
			c = &copilotLifecycleChild{toolCallID: tc}
			byID[tc] = c
			order = append(order, tc)
		}
		return c
	}

	out := &copilotCollaborationEvents{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var evt jsonlEvent
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			continue
		}
		switch evt.Type {
		case "session.shutdown":
			out.shutdown = true
		case "tool.execution_start":
			if name, _ := extractString(evt.Data, "toolName"); name != "task" {
				continue
			}
			tc, _ := extractString(evt.Data, "toolCallId")
			if tc == "" {
				continue
			}
			c := childFor(tc)
			c.hasTask = true
			if ts, ok := parseTS(evt.Timestamp); ok {
				c.taskTS = ts
			}
			if args := nestedMap(evt.Data, "arguments"); args != nil {
				c.description, _ = extractString(args, "description")
				c.roleName, _ = extractString(args, "name")
				c.mode, _ = extractString(args, "mode")
			}
		case "subagent.started":
			tc, _ := extractString(evt.Data, "toolCallId")
			if tc == "" {
				continue
			}
			c := childFor(tc)
			c.displayName, _ = extractString(evt.Data, "agentDisplayName")
			if ts, ok := parseTS(evt.Timestamp); ok {
				c.started = ts
				c.hasStart = true
			}
		case "subagent.completed":
			tc, _ := extractString(evt.Data, "toolCallId")
			if tc == "" {
				continue
			}
			c := childFor(tc)
			if ts, ok := parseTS(evt.Timestamp); ok {
				c.completed = ts
				c.hasComplete = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	for _, tc := range order {
		out.children = append(out.children, byID[tc])
	}
	return out, nil
}

// copilotRootInvocation builds the one deterministic root invocation. A
// recorded session.shutdown is explicit completion evidence; otherwise the
// root is running on a positive live signal and unknown past it.
func copilotRootInvocation(root model.Session, shutdown, live bool) collaboration.AgentInvocation {
	status := collaboration.StatusUnknown
	switch {
	case shutdown:
		status = collaboration.StatusCompleted
	case live:
		status = collaboration.StatusRunning
	}
	return collaboration.AgentInvocation{
		ID:               collaboration.RootInvocationID("copilot", root.ID),
		DisplayName:      "copilot main agent",
		AgentType:        "copilot",
		Status:           status,
		TimePrecision:    collaboration.ExactFact(),
		ContentPrecision: collaboration.ExactFact(),
		SourceIdentity: collaboration.SourceIdentity{
			Kind:     collaboration.IdentityRootSession,
			NativeID: root.ID,
		},
	}
}

// copilotChildCollaboration maps one lifecycle child to its invocation and
// delegation. Liveness decides between running/pending (start observed, root
// still live) and orphaned (start observed, root closed) — and stays
// deterministic for a given root snapshot.
func copilotChildCollaboration(rootSessionID, rootInvID string, child *copilotLifecycleChild, live bool) (collaboration.AgentInvocation, collaboration.Delegation) {
	childInvID := collaboration.ChildInvocationID("copilot", rootSessionID, child.toolCallID)

	displayName := child.displayName
	if displayName == "" {
		displayName = child.roleName
	}
	if displayName == "" {
		displayName = "copilot child agent"
	}

	status := collaboration.StatusUnknown
	switch {
	case child.hasComplete:
		status = collaboration.StatusCompleted
	case child.hasStart && live:
		status = collaboration.StatusRunning
	case child.hasStart:
		status = collaboration.StatusOrphaned
	case live:
		status = collaboration.StatusPending
	}

	inv := collaboration.AgentInvocation{
		ID:            childInvID,
		DisplayName:   displayName,
		AgentType:     "copilot",
		RoleLabel:     child.roleName,
		Status:        status,
		TimePrecision: copilotTimingPrecision(child),
		ContentPrecision: collaboration.FactEvidence{
			State:      collaboration.EvidenceEstimated,
			ReasonCode: collaboration.ReasonAggregateWindow,
		},
		SourceIdentity: collaboration.SourceIdentity{
			Kind:     collaboration.IdentityToolCallID,
			NativeID: child.toolCallID,
		},
	}
	if child.hasStart {
		started := child.started
		inv.StartedAt = &started
	}
	if child.hasComplete {
		completed := child.completed
		inv.EndedAt = &completed
	}

	del := collaboration.Delegation{
		ID:                 collaboration.DelegationIDFor(rootInvID, childInvID),
		ParentInvocationID: rootInvID,
		ChildInvocationID:  childInvID,
		ExecutionMode:      copilotExecutionMode(child.mode),
		Evidence: collaboration.DelegationEvidence{
			Timing: inv.TimePrecision,
			Task: collaboration.FactEvidence{
				State:      collaboration.EvidenceMissing,
				ReasonCode: collaboration.ReasonSourceNotRecorded,
			},
		},
	}

	// Trigger: the task tool.execution_start when present (it carries the
	// delegation arguments), else the subagent.started lifecycle event. The
	// anchor is exact via the stable toolCallId; the timestamp is attached
	// only when the source line carried a parseable one.
	triggerTS := child.taskTS
	if !child.hasTask {
		triggerTS = child.started
	}
	if child.hasTask || child.hasStart {
		del.Trigger = &collaboration.SourceAnchor{
			AgentType:  "copilot",
			SessionID:  rootSessionID,
			ToolCallID: child.toolCallID,
			Precision:  collaboration.ExactFact(),
		}
		if !triggerTS.IsZero() {
			ts := triggerTS
			del.Trigger.Timestamp = &ts
		}
		del.Evidence.Trigger = collaboration.ExactFact()
	} else {
		del.Evidence.Trigger = collaboration.FactEvidence{
			State:      collaboration.EvidenceMissing,
			ReasonCode: collaboration.ReasonSourceNotRecorded,
		}
	}

	if child.hasComplete {
		completed := child.completed
		del.Result = &collaboration.SourceAnchor{
			AgentType:  "copilot",
			SessionID:  rootSessionID,
			ToolCallID: child.toolCallID,
			Timestamp:  &completed,
			Precision:  collaboration.ExactFact(),
		}
		del.Evidence.Result = collaboration.ExactFact()
	} else {
		del.Evidence.Result = collaboration.FactEvidence{
			State:      collaboration.EvidenceMissing,
			ReasonCode: collaboration.ReasonCompletionNotRecorded,
		}
	}

	// Only the source-recorded description may surface as a task summary; the
	// full delegation prompt is never stored.
	if child.description != "" {
		del.TaskSummary = child.description
		del.Evidence.Task = collaboration.ExactFact()
	}
	return inv, del
}

// copilotTimingPrecision derives timing evidence from the observed lifecycle
// boundaries: exact when both are recorded, estimated with an open end when
// only the start is, missing when neither is.
func copilotTimingPrecision(child *copilotLifecycleChild) collaboration.FactEvidence {
	switch {
	case child.hasStart && child.hasComplete:
		return collaboration.ExactFact()
	case child.hasStart:
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

// copilotExecutionMode maps the source-recorded task mode onto the
// normalized set; anything unrecorded or unrecognized stays unknown.
func copilotExecutionMode(mode string) collaboration.ExecutionMode {
	switch mode {
	case "sync":
		return collaboration.ExecutionBlocking
	case "async", "background":
		return collaboration.ExecutionBackground
	default:
		return collaboration.ExecutionUnknown
	}
}
