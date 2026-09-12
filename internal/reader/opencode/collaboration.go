package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/shared"
)

// ReadCollaboration implements reader.CollaborationReader for OpenCode's
// standalone-child archetype. OpenCode stores each task in its own native
// Session and links it to the delegating Session with session.parent_id.
//
// The child Session ID is the native stable identity and resume ID. Parent
// task parts provide optional launch/result anchors, the source-recorded
// description, the foreground/background execution mode, and the task
// lifecycle. The child message stream proves transcript availability and
// supplies a terminal state only when it records a finish/error marker; a
// parent task result fills lifecycle gaps without guessing from mtime.
// Graphs are defined for root Sessions only, while child Sessions remain
// renderable through their BackingSessionRef.
func (r *OpenCodeReader) ReadCollaboration(ctx context.Context, root model.Session) (collaboration.CollaborationGraph, error) {
	if root.AgentType != "opencode" {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"opencode collaboration: root session agent type %q is not opencode", root.AgentType)
	}
	if root.IsSubagent || root.ParentSessionID != "" {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"opencode collaboration: %s is a subagent child of %s; collaboration graphs are defined for root sessions only",
			root.ID, root.ParentSessionID)
	}
	if err := ctx.Err(); err != nil {
		return collaboration.CollaborationGraph{}, err
	}

	// A legacy OpenCode database can predate session.parent_id. It still gets
	// a valid root-only graph, but the adapter must not claim that it proved a
	// complete child inventory from a schema that cannot represent lineage.
	if !r.hasParentID {
		if _, err := r.readSessionMeta(root.ID); err != nil {
			return collaboration.CollaborationGraph{}, err
		}
		graph := collaboration.CollaborationGraph{
			RootAgentType: "opencode",
			RootSessionID: root.ID,
			Revision:      model.SessionRevision(root),
			Completeness: collaboration.FactEvidence{
				State:      collaboration.EvidenceEstimated,
				ReasonCode: collaboration.ReasonSourceNotRecorded,
			},
			Invocations: []collaboration.AgentInvocation{openCodeRootInvocation(root)},
		}
		if v := collaboration.Validate(&graph); !v.OK() {
			return collaboration.CollaborationGraph{}, fmt.Errorf(
				"opencode collaboration: normalized graph violates the contract: %s", v.Issues[0].Detail)
		}
		return graph, nil
	}

	sessions, err := r.listCollaborationSessions(ctx)
	if err != nil {
		return collaboration.CollaborationGraph{}, err
	}
	bySessionID := make(map[string]openCodeCollaborationSession, len(sessions))
	childrenByParentID := make(map[string][]openCodeCollaborationSession)
	for _, session := range sessions {
		bySessionID[session.ID] = session
		if session.ParentSessionID != "" {
			childrenByParentID[session.ParentSessionID] = append(
				childrenByParentID[session.ParentSessionID], session)
		}
	}

	rootRecord, found := bySessionID[root.ID]
	if !found {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"opencode collaboration: root session %q not found", root.ID)
	}
	if rootRecord.IsSubagent {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"opencode collaboration: %s is a subagent child of %s; collaboration graphs are defined for root sessions only",
			root.ID, rootRecord.ParentSessionID)
	}

	graph := collaboration.CollaborationGraph{
		RootAgentType: "opencode",
		RootSessionID: root.ID,
		Revision:      model.SessionRevision(root),
		Completeness:  collaboration.ExactFact(),
		Invocations:   []collaboration.AgentInvocation{openCodeRootInvocation(root)},
	}
	if !r.hasParentID || !openCodeLineageComplete(sessions) {
		graph.Completeness = collaboration.FactEvidence{
			State:      collaboration.EvidenceEstimated,
			ReasonCode: collaboration.ReasonSourceNotRecorded,
		}
	}

	rootInvocationID := graph.Invocations[0].ID
	visitedSessionIDs := map[string]bool{root.ID: true}
	var appendChildren func(string, string) error
	appendChildren = func(parentSessionID, parentInvocationID string) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		taskRelations, err := r.readTaskRelations(ctx, parentSessionID)
		if err != nil {
			return err
		}
		relationByChildID := make(map[string]openCodeTaskRelation, len(taskRelations))
		for _, relation := range taskRelations {
			relationByChildID[relation.childSessionID] = relation
		}
		renderAnchors, err := r.readTaskRenderAnchors(parentSessionID)
		if err != nil {
			return err
		}

		children := childrenByParentID[parentSessionID]
		sortOpenCodeChildren(children)
		childIDs := make(map[string]struct{}, len(children))
		for _, child := range children {
			childIDs[child.ID] = struct{}{}
		}
		for _, relation := range taskRelations {
			if _, found := childIDs[relation.childSessionID]; !found {
				// A task result names a child that is not present in the
				// session inventory. Do not synthesize a backing Session, but
				// make the incomplete join visible at graph level.
				graph.Completeness = collaboration.FactEvidence{
					State:      collaboration.EvidenceEstimated,
					ReasonCode: collaboration.ReasonSourceNotRecorded,
				}
			}
		}
		for _, child := range children {
			if err := ctx.Err(); err != nil {
				return err
			}
			if visitedSessionIDs[child.ID] {
				// A malformed parent graph must not duplicate an invocation or
				// recurse forever. The global completeness flag already records
				// malformed lineage when this is a cycle.
				graph.Completeness = collaboration.FactEvidence{
					State:      collaboration.EvidenceEstimated,
					ReasonCode: collaboration.ReasonSourceNotRecorded,
				}
				continue
			}
			visitedSessionIDs[child.ID] = true

			childState, err := r.readChildMessageState(ctx, child.ID)
			if err != nil {
				return err
			}
			relation, hasRelation := relationByChildID[child.ID]
			var relationPointer *openCodeTaskRelation
			if hasRelation {
				relationPointer = &relation
			}
			anchor := renderAnchors[relation.toolCallID]
			invocation, delegation := openCodeChildCollaboration(
				root.ID, parentInvocationID, child, childState, relationPointer, anchor)
			graph.Invocations = append(graph.Invocations, invocation)
			graph.Delegations = append(graph.Delegations, delegation)

			if err := appendChildren(child.ID, invocation.ID); err != nil {
				return err
			}
		}
		return nil
	}

	if err := appendChildren(root.ID, rootInvocationID); err != nil {
		return collaboration.CollaborationGraph{}, err
	}

	if v := collaboration.Validate(&graph); !v.OK() {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"opencode collaboration: normalized graph violates the contract: %s", v.Issues[0].Detail)
	}
	return graph, nil
}

// openCodeCollaborationSession is the source projection needed to build the
// graph. The public model intentionally does not expose OpenCode's agent-name
// column, so the adapter keeps that source-only field package-local.
type openCodeCollaborationSession struct {
	model.Session
	title     string
	agentName string
}

func (r *OpenCodeReader) listCollaborationSessions(ctx context.Context) ([]openCodeCollaborationSession, error) {
	if !r.hasParentID {
		// Verify the root through the regular metadata path in the caller's
		// subsequent lookup while avoiding a query that cannot discover any
		// lineage.
		return nil, nil
	}
	agentExpression := "''"
	if r.hasAgentName {
		agentExpression = "COALESCE(s.agent, '')"
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.id, COALESCE(s.parent_id, ''), s.directory, s.title,
		       s.time_created, s.time_updated, s.model,
		       `+agentExpression+` AS agent_name,
		       (SELECT COUNT(*) FROM message WHERE session_id = s.id) AS message_count
		FROM session s
		ORDER BY s.time_created ASC, s.id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("opencode collaboration: list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []openCodeCollaborationSession
	for rows.Next() {
		var (
			id, parentSessionID, directory, title, agentName string
			timeCreated, timeUpdated                         int64
			modelJSON                                        sql.NullString
			messageCount                                     int
		)
		if err := rows.Scan(&id, &parentSessionID, &directory, &title,
			&timeCreated, &timeUpdated, &modelJSON, &agentName, &messageCount); err != nil {
			return nil, fmt.Errorf("opencode collaboration: scan session: %w", err)
		}

		modelName, modelProvider := "", ""
		if modelJSON.Valid && modelJSON.String != "" {
			modelName, modelProvider = extractModelMeta(modelJSON.String)
		}
		sessions = append(sessions, openCodeCollaborationSession{
			Session: model.Session{
				ID:              id,
				AgentType:       "opencode",
				CWD:             directory,
				Project:         shared.ResolveProject(directory, ""),
				Name:            title,
				ModelName:       modelName,
				ModelProvider:   modelProvider,
				ResumeID:        id,
				ParentSessionID: parentSessionID,
				IsSubagent:      parentSessionID != "",
				MessageCount:    messageCount,
				CreatedAt:       openCodeMillis(timeCreated),
				UpdatedAt:       openCodeMillis(timeUpdated),
			},
			title:     title,
			agentName: strings.TrimSpace(agentName),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("opencode collaboration: iterate sessions: %w", err)
	}
	return sessions, nil
}

func openCodeLineageComplete(sessions []openCodeCollaborationSession) bool {
	byID := make(map[string]openCodeCollaborationSession, len(sessions))
	for _, session := range sessions {
		byID[session.ID] = session
	}
	for _, session := range sessions {
		seen := map[string]bool{}
		current := session
		for current.ParentSessionID != "" {
			if seen[current.ID] {
				return false
			}
			seen[current.ID] = true
			parent, ok := byID[current.ParentSessionID]
			if !ok {
				return false
			}
			current = parent
		}
	}
	return true
}

func sortOpenCodeChildren(children []openCodeCollaborationSession) {
	sort.SliceStable(children, func(i, j int) bool {
		if !children[i].CreatedAt.Equal(children[j].CreatedAt) {
			return children[i].CreatedAt.Before(children[j].CreatedAt)
		}
		return children[i].ID < children[j].ID
	})
}

type openCodeTaskRelation struct {
	childSessionID   string
	toolCallID       string
	taskSummary      string
	status           string
	isBackground     bool
	hasExecutionMode bool
	hasTerminal      bool
	startedAt        time.Time
	endedAt          time.Time
}

// readTaskRelations reads only parent-side task tool parts. The child
// session.parent_id remains the authoritative topology; these parts enrich
// the edge with source-recorded task details and lifecycle anchors.
func (r *OpenCodeReader) readTaskRelations(ctx context.Context, parentSessionID string) ([]openCodeTaskRelation, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT p.time_created, m.time_created, p.data
		FROM part p
		JOIN message m ON m.id = p.message_id
		WHERE p.session_id = ?
		ORDER BY m.time_created ASC, p.id ASC
	`, parentSessionID)
	if err != nil {
		return nil, fmt.Errorf("opencode collaboration: read task parts for %s: %w", parentSessionID, err)
	}
	defer rows.Close()

	byChildID := make(map[string]*openCodeTaskRelation)
	for rows.Next() {
		var (
			data                            string
			partCreatedAt, messageCreatedAt int64
		)
		if err := rows.Scan(&partCreatedAt, &messageCreatedAt, &data); err != nil {
			return nil, fmt.Errorf("opencode collaboration: scan task part: %w", err)
		}

		var part partData
		if err := json.Unmarshal([]byte(data), &part); err != nil || !isOpenCodeTaskPart(part) {
			continue
		}
		metadata := openCodeTaskMetadata(part)
		status := ""
		taskSummary := openCodeSessionDescription(part.Description)
		var stateTime *openCodePartTime
		if part.State != nil {
			status = strings.TrimSpace(part.State.Status)
			if part.State.Title != "" {
				taskSummary = openCodeSessionDescription(part.State.Title)
			}
			stateTime = part.State.Time
		}

		childSessionID := openCodeMetadataString(metadata,
			"sessionId", "sessionID", "session_id",
			"childSessionId", "childSessionID", "child_session_id")
		if childSessionID == "" && part.State != nil {
			childSessionID = extractOpenCodeTaskSessionID(part.State.Output)
		}
		if childSessionID == "" {
			// A task part without a stable child session ID cannot be joined
			// to a native child. It remains ordinary tool evidence.
			continue
		}

		relation := byChildID[childSessionID]
		if relation == nil {
			relation = &openCodeTaskRelation{
				childSessionID: childSessionID,
				toolCallID:     part.CallID,
			}
			byChildID[childSessionID] = relation
		}
		if relation.toolCallID == "" {
			relation.toolCallID = part.CallID
		}
		if relation.taskSummary == "" {
			relation.taskSummary = taskSummary
		}
		if isBackground, hasExecutionMode := openCodeMetadataBool(metadata, "background"); hasExecutionMode {
			relation.isBackground = isBackground
			relation.hasExecutionMode = true
		}
		if relation.status == "" || isOpenCodeTerminalTaskStatus(status) {
			relation.status = status
		}
		if relation.startedAt.IsZero() {
			relation.startedAt = openCodeTaskStart(stateTime, partCreatedAt, messageCreatedAt)
		}
		if isOpenCodeTerminalTaskStatus(status) {
			relation.hasTerminal = true
			if stateTime != nil && stateTime.End != nil {
				relation.endedAt = openCodeMillis(*stateTime.End)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("opencode collaboration: iterate task parts: %w", err)
	}

	relations := make([]openCodeTaskRelation, 0, len(byChildID))
	for _, relation := range byChildID {
		relations = append(relations, *relation)
	}
	sort.Slice(relations, func(i, j int) bool {
		if !relations[i].startedAt.Equal(relations[j].startedAt) {
			if relations[i].startedAt.IsZero() {
				return false
			}
			if relations[j].startedAt.IsZero() {
				return true
			}
			return relations[i].startedAt.Before(relations[j].startedAt)
		}
		return relations[i].childSessionID < relations[j].childSessionID
	})
	return relations, nil
}

func isOpenCodeTaskPart(part partData) bool {
	return part.Type == "tool" && strings.EqualFold(strings.TrimSpace(part.Tool), "task")
}

func openCodeTaskMetadata(part partData) map[string]any {
	metadata := make(map[string]any, len(part.Metadata))
	for key, value := range part.Metadata {
		metadata[key] = value
	}
	if part.State != nil {
		for key, value := range part.State.Metadata {
			metadata[key] = value
		}
	}
	return metadata
}

func openCodeTaskStart(stateTime *openCodePartTime, partCreatedAt, messageCreatedAt int64) time.Time {
	if stateTime != nil && stateTime.Start > 0 {
		return openCodeMillis(stateTime.Start)
	}
	if partCreatedAt > 0 {
		return openCodeMillis(partCreatedAt)
	}
	return openCodeMillis(messageCreatedAt)
}

type openCodeTaskRenderAnchor struct {
	triggerEventID string
	resultEventID  string
}

func (r *OpenCodeReader) readTaskRenderAnchors(parentSessionID string) (map[string]openCodeTaskRenderAnchor, error) {
	events, err := r.GetRenderEvents(parentSessionID)
	if err != nil {
		return nil, fmt.Errorf("opencode collaboration: read parent render for %s: %w", parentSessionID, err)
	}
	byCallID := make(map[string]openCodeTaskRenderAnchor)
	invocationByEventID := make(map[string]model.RenderEvent)
	for _, event := range events {
		switch event.Type {
		case "ToolInvocation":
			if event.ToolCallID == "" {
				continue
			}
			byCallID[event.ToolCallID] = openCodeTaskRenderAnchor{
				triggerEventID: event.EventID,
			}
			invocationByEventID[event.EventID] = event
		case "ToolResult":
			callID := event.ToolCallID
			if callID == "" {
				callID = invocationByEventID[event.ParentEventID].ToolCallID
			}
			if callID == "" {
				continue
			}
			anchor := byCallID[callID]
			anchor.resultEventID = event.EventID
			byCallID[callID] = anchor
		}
	}
	return byCallID, nil
}

type openCodeChildMessageState struct {
	hasAssistantContent bool
	startedAt           time.Time
	endedAt             time.Time
	terminal            bool
	status              collaboration.InvocationStatus
	isOpen              bool
}

func (r *OpenCodeReader) readChildMessageState(ctx context.Context, sessionID string) (openCodeChildMessageState, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT m.id, m.time_created, m.data,
		       (SELECT COUNT(*) FROM part p WHERE p.message_id = m.id) AS part_count
		FROM message m
		WHERE m.session_id = ?
		ORDER BY m.time_created ASC, m.id ASC
	`, sessionID)
	if err != nil {
		return openCodeChildMessageState{}, fmt.Errorf("opencode collaboration: read child messages for %s: %w", sessionID, err)
	}
	defer rows.Close()

	var state openCodeChildMessageState
	var lastAssistant *openCodeAssistantState
	for rows.Next() {
		var messageID, data string
		var messageCreatedAt int64
		var partCount int
		if err := rows.Scan(&messageID, &messageCreatedAt, &data, &partCount); err != nil {
			return openCodeChildMessageState{}, fmt.Errorf("opencode collaboration: scan child message: %w", err)
		}
		var base msgBase
		if json.Unmarshal([]byte(data), &base) != nil || base.Role != "assistant" {
			continue
		}
		var assistant assistantMsgData
		if json.Unmarshal([]byte(data), &assistant) != nil {
			continue
		}
		state.hasAssistantContent = state.hasAssistantContent || partCount > 0
		assistantState := openCodeAssistantStateFromMessage(assistant, messageCreatedAt)
		if state.startedAt.IsZero() && !assistantState.startedAt.IsZero() {
			state.startedAt = assistantState.startedAt
		}
		lastAssistant = &assistantState
	}
	if err := rows.Err(); err != nil {
		return openCodeChildMessageState{}, fmt.Errorf("opencode collaboration: iterate child messages: %w", err)
	}
	if lastAssistant == nil {
		return state, nil
	}
	state.startedAt = firstNonZeroOpenCodeTime(state.startedAt, lastAssistant.startedAt)
	if lastAssistant.terminal {
		state.endedAt = lastAssistant.endedAt
	}
	state.terminal = lastAssistant.terminal
	state.status = lastAssistant.status
	state.isOpen = !lastAssistant.terminal
	return state, nil
}

type openCodeAssistantState struct {
	startedAt time.Time
	endedAt   time.Time
	terminal  bool
	status    collaboration.InvocationStatus
}

// A completed assistant response is not by itself a completed child task:
// OpenCode can persist time.completed for an intermediate model turn while
// the session still has tool work left. Finish/error is the explicit child
// terminal marker; parent task state remains the stronger lifecycle fact when
// it is available.
func openCodeAssistantStateFromMessage(message assistantMsgData, messageCreatedAt int64) openCodeAssistantState {
	state := openCodeAssistantState{status: collaboration.StatusUnknown}
	if message.Time != nil {
		state.startedAt = openCodeMillis(message.Time.Created)
		if message.Time.Completed != nil {
			state.endedAt = openCodeMillis(*message.Time.Completed)
		}
	}
	if state.startedAt.IsZero() {
		state.startedAt = openCodeMillis(messageCreatedAt)
	}
	if message.Error != nil {
		state.terminal = true
		state.status = openCodeErrorStatus(message.Error)
	}
	if !state.terminal && message.Finish != "" {
		finishStatus := normalizeOpenCodeStatus(message.Finish)
		switch finishStatus {
		case collaboration.StatusCompleted, collaboration.StatusFailed, collaboration.StatusCancelled:
			state.terminal = true
			state.status = finishStatus
		}
	}
	if state.status == collaboration.StatusUnknown && state.terminal {
		state.status = collaboration.StatusCompleted
	}
	return state
}

func openCodeRootInvocation(root model.Session) collaboration.AgentInvocation {
	status := collaboration.StatusUnknown
	if root.IsLive || model.IsSessionLive(root.UpdatedAt) {
		status = collaboration.StatusRunning
	}
	return collaboration.AgentInvocation{
		ID:               collaboration.RootInvocationID("opencode", root.ID),
		DisplayName:      "opencode main agent",
		AgentType:        "opencode",
		Status:           status,
		TimePrecision:    collaboration.ExactFact(),
		ContentPrecision: collaboration.ExactFact(),
		SourceIdentity: collaboration.SourceIdentity{
			Kind:     collaboration.IdentityRootSession,
			NativeID: root.ID,
		},
	}
}

func openCodeChildCollaboration(
	rootSessionID, parentInvocationID string,
	child openCodeCollaborationSession,
	childState openCodeChildMessageState,
	taskRelation *openCodeTaskRelation,
	renderAnchor openCodeTaskRenderAnchor,
) (collaboration.AgentInvocation, collaboration.Delegation) {
	childInvocationID := collaboration.ChildInvocationID("opencode", rootSessionID, child.ID)
	childStartedAt := child.CreatedAt
	if childStartedAt.IsZero() {
		childStartedAt = childState.startedAt
	}
	if childStartedAt.IsZero() && taskRelation != nil {
		childStartedAt = taskRelation.startedAt
	}

	childEndedAt := childState.endedAt
	if childEndedAt.IsZero() && taskRelation != nil && taskRelation.hasTerminal {
		childEndedAt = taskRelation.endedAt
	}
	hasStart := !childStartedAt.IsZero()
	hasEnd := !childEndedAt.IsZero()
	if hasStart && hasEnd && childEndedAt.Before(childStartedAt) {
		childEndedAt = time.Time{}
		hasEnd = false
	}

	status := childState.status
	if status == "" {
		status = collaboration.StatusUnknown
	}
	if taskRelation != nil {
		relationStatus := normalizeOpenCodeStatus(taskRelation.status)
		switch {
		case taskRelation.hasTerminal:
			// The parent task is the lifecycle record for this delegation and
			// wins over a child model-turn marker when the two disagree.
			status = relationStatus
		case relationStatus == collaboration.StatusPending,
			relationStatus == collaboration.StatusRunning,
			relationStatus == collaboration.StatusWaiting:
			status = relationStatus
		}
	}
	if status == collaboration.StatusUnknown {
		status = openCodeInProgressStatus(child, childState, taskRelation, hasStart)
	}

	description := ""
	if taskRelation != nil {
		description = strings.TrimSpace(taskRelation.taskSummary)
	}
	if description == "" {
		description = openCodeSessionDescription(child.title)
	}
	displayName := description
	if displayName == "" {
		displayName = child.agentName
	}
	if displayName == "" {
		displayName = "opencode child agent"
	}

	invocation := collaboration.AgentInvocation{
		ID:               childInvocationID,
		DisplayName:      displayName,
		AgentType:        "opencode",
		RoleLabel:        child.agentName,
		Status:           status,
		TimePrecision:    openCodeTimePrecision(hasStart, hasEnd),
		ContentPrecision: collaboration.FactEvidence{State: collaboration.EvidenceMissing, ReasonCode: collaboration.ReasonSourceNotRecorded},
		BackingSession:   &collaboration.BackingSessionRef{AgentType: "opencode", SessionID: child.ID},
		SourceIdentity: collaboration.SourceIdentity{
			Kind:     collaboration.IdentitySessionID,
			NativeID: child.ID,
			Attributes: map[string]string{
				"parent_session_id": child.ParentSessionID,
			},
		},
	}
	if childState.hasAssistantContent {
		invocation.ContentPrecision = collaboration.ExactFact()
	}
	if hasStart {
		startedAt := childStartedAt
		invocation.StartedAt = &startedAt
	}
	if hasEnd {
		endedAt := childEndedAt
		invocation.EndedAt = &endedAt
	}

	delegation := collaboration.Delegation{
		ID:                 collaboration.DelegationIDFor(parentInvocationID, childInvocationID),
		ParentInvocationID: parentInvocationID,
		ChildInvocationID:  childInvocationID,
		ExecutionMode:      collaboration.ExecutionUnknown,
		Evidence: collaboration.DelegationEvidence{
			Timing: invocation.TimePrecision,
		},
	}
	if description != "" {
		delegation.TaskSummary = description
		delegation.Evidence.Task = collaboration.ExactFact()
	} else {
		delegation.Evidence.Task = collaboration.FactEvidence{
			State:      collaboration.EvidenceMissing,
			ReasonCode: collaboration.ReasonSourceNotRecorded,
		}
	}

	if taskRelation == nil {
		delegation.Evidence.Trigger = collaboration.FactEvidence{
			State:      collaboration.EvidenceMissing,
			ReasonCode: collaboration.ReasonSourceNotRecorded,
		}
		delegation.Evidence.Result = collaboration.FactEvidence{
			State:      collaboration.EvidenceMissing,
			ReasonCode: collaboration.ReasonSourceNotRecorded,
		}
		return invocation, delegation
	}

	if taskRelation.hasExecutionMode {
		if taskRelation.isBackground {
			delegation.ExecutionMode = collaboration.ExecutionBackground
		} else {
			delegation.ExecutionMode = collaboration.ExecutionBlocking
		}
	}
	anchorSessionID := child.ParentSessionID
	trigger := &collaboration.SourceAnchor{
		AgentType:  "opencode",
		SessionID:  anchorSessionID,
		EventID:    renderAnchor.triggerEventID,
		ToolCallID: taskRelation.toolCallID,
		Precision:  collaboration.ExactFact(),
	}
	triggerAt := taskRelation.startedAt
	if !triggerAt.IsZero() {
		if hasStart && triggerAt.After(childStartedAt) {
			trigger.Precision = collaboration.FactEvidence{
				State:      collaboration.EvidenceMissing,
				ReasonCode: collaboration.ReasonTimestampContradiction,
			}
		} else {
			timestamp := triggerAt
			trigger.Timestamp = &timestamp
		}
	}
	delegation.Trigger = trigger
	delegation.Evidence.Trigger = collaboration.ExactFact()

	if taskRelation.hasTerminal {
		result := &collaboration.SourceAnchor{
			AgentType:  "opencode",
			SessionID:  anchorSessionID,
			EventID:    renderAnchor.resultEventID,
			ToolCallID: taskRelation.toolCallID,
			Precision:  collaboration.ExactFact(),
		}
		resultAt := taskRelation.endedAt
		if !resultAt.IsZero() && (!hasStart || !resultAt.Before(childStartedAt)) {
			timestamp := resultAt
			result.Timestamp = &timestamp
		}
		delegation.Result = result
		delegation.Evidence.Result = collaboration.ExactFact()
	} else {
		delegation.Evidence.Result = collaboration.FactEvidence{
			State:      collaboration.EvidenceMissing,
			ReasonCode: collaboration.ReasonCompletionNotRecorded,
		}
	}
	return invocation, delegation
}

func openCodeInProgressStatus(
	child openCodeCollaborationSession,
	childState openCodeChildMessageState,
	taskRelation *openCodeTaskRelation,
	hasStart bool,
) collaboration.InvocationStatus {
	if childState.terminal {
		return childState.status
	}
	childLive := child.IsLive || model.IsSessionLive(child.UpdatedAt)
	if childState.isOpen {
		if childLive {
			return collaboration.StatusRunning
		}
		return collaboration.StatusOrphaned
	}
	if taskRelation != nil {
		switch normalizeOpenCodeStatus(taskRelation.status) {
		case collaboration.StatusPending:
			if childLive {
				return collaboration.StatusPending
			}
			return collaboration.StatusOrphaned
		case collaboration.StatusRunning, collaboration.StatusWaiting:
			if childLive {
				return normalizeOpenCodeStatus(taskRelation.status)
			}
			return collaboration.StatusOrphaned
		}
	}
	if hasStart {
		if childLive {
			return collaboration.StatusRunning
		}
		return collaboration.StatusOrphaned
	}
	return collaboration.StatusUnknown
}

func openCodeTimePrecision(hasStart, hasEnd bool) collaboration.FactEvidence {
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

func normalizeOpenCodeStatus(status string) collaboration.InvocationStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending", "queued":
		return collaboration.StatusPending
	case "running", "in_progress", "in-progress", "started":
		return collaboration.StatusRunning
	case "waiting", "blocked":
		return collaboration.StatusWaiting
	case "completed", "complete", "success", "succeeded", "ok", "stop", "end_turn", "length":
		return collaboration.StatusCompleted
	case "failed", "failure", "error", "errored":
		return collaboration.StatusFailed
	case "cancelled", "canceled", "aborted", "abort", "interrupted":
		return collaboration.StatusCancelled
	default:
		return collaboration.StatusUnknown
	}
}

func isOpenCodeTerminalTaskStatus(status string) bool {
	switch normalizeOpenCodeStatus(status) {
	case collaboration.StatusCompleted, collaboration.StatusFailed, collaboration.StatusCancelled:
		return true
	default:
		return false
	}
}

func openCodeErrorStatus(raw *json.RawMessage) collaboration.InvocationStatus {
	if raw == nil {
		return collaboration.StatusFailed
	}
	var detail struct {
		Name string `json:"name"`
	}
	if json.Unmarshal([]byte(*raw), &detail) == nil && detail.Name != "" {
		if status := normalizeOpenCodeStatus(detail.Name); status != collaboration.StatusUnknown {
			return status
		}
		return classifyOpenCodeErrorName(detail.Name)
	}
	var message string
	if json.Unmarshal([]byte(*raw), &message) == nil {
		if status := normalizeOpenCodeStatus(message); status != collaboration.StatusUnknown {
			return status
		}
		return classifyOpenCodeErrorName(message)
	}
	return collaboration.StatusFailed
}

func classifyOpenCodeErrorName(name string) collaboration.InvocationStatus {
	lowerName := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.Contains(lowerName, "abort"), strings.Contains(lowerName, "cancel"):
		return collaboration.StatusCancelled
	case strings.Contains(lowerName, "fail"), strings.Contains(lowerName, "error"):
		return collaboration.StatusFailed
	default:
		return collaboration.StatusFailed
	}
}

func openCodeMetadataString(metadata map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := metadata[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func openCodeMetadataBool(metadata map[string]any, key string) (bool, bool) {
	value, ok := metadata[key]
	if !ok {
		return false, false
	}
	if flag, ok := value.(bool); ok {
		return flag, true
	}
	parsed := strings.TrimSpace(fmt.Sprint(value))
	return strings.EqualFold(parsed, "true"), strings.EqualFold(parsed, "true") || strings.EqualFold(parsed, "false")
}

func extractOpenCodeTaskSessionID(output string) string {
	for _, quote := range []string{`"`, `'`} {
		marker := `<task id=` + quote
		start := strings.Index(output, marker)
		if start < 0 {
			continue
		}
		start += len(marker)
		end := strings.Index(output[start:], quote)
		if end > 0 {
			return strings.TrimSpace(output[start : start+end])
		}
	}
	return ""
}

func openCodeSessionDescription(title string) string {
	description := strings.TrimSpace(title)
	marker := strings.LastIndex(description, " (@")
	if marker < 0 || !strings.HasSuffix(description[marker:], " subagent)") {
		return description
	}
	return strings.TrimSpace(description[:marker])
}

func openCodeMillis(milliseconds int64) time.Time {
	if milliseconds <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(milliseconds).UTC()
}

func firstNonZeroOpenCodeTime(first, second time.Time) time.Time {
	if !first.IsZero() {
		return first
	}
	return second
}

// childInvocationID resolves a standalone child Session to the root namespace
// used by the collaboration graph. It is also used by the replay renderer so
// a child backing Session's events can be selected without a second transcript
// representation.
func (r *OpenCodeReader) childInvocationID(sessionID string) string {
	if !r.hasParentID || sessionID == "" {
		return ""
	}
	child, err := r.readSessionMeta(sessionID)
	if err != nil || child.ParentSessionID == "" {
		return ""
	}
	rootSessionID := child.ParentSessionID
	seen := map[string]bool{}
	current := child
	for current.ParentSessionID != "" {
		if seen[current.ID] {
			return ""
		}
		seen[current.ID] = true
		parent, err := r.readSessionMeta(current.ParentSessionID)
		if err != nil {
			return ""
		}
		rootSessionID = parent.ID
		current = parent
	}
	if rootSessionID == "" || rootSessionID == sessionID {
		return ""
	}
	return collaboration.ChildInvocationID("opencode", rootSessionID, sessionID)
}
