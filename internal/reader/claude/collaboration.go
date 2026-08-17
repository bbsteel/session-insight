package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/model"
)

// ReadCollaboration implements reader.CollaborationReader for Claude Code's
// embedded-child archetype: a subagent is a sidecar transcript under
// <session>/subagents/agent-<agentId>.jsonl, joined to the parent by
// meta.toolUseId == Agent/Task tool_use.id and toolUseResult.agentId.
//
// Mapping (contract + IdentityAgentID):
//   - identity: native agentId (filename suffix / toolUseResult.agentId),
//     namespaced by the root session;
//   - lineage: exact toolUseId join when meta records it; otherwise the
//     documented Agent-result FIFO pair is estimated (fifo_join_heuristic);
//   - anchors: trigger is the parent Agent/Task ToolInvocation event id
//     (the tool_use id). Result is the TaskOutput completion when the
//     parent waited with task_id == agentId, or the non-async Agent
//     ToolResult for the older blocking form;
//   - status/timing: TaskOutput.task.status or the sync Agent result;
//     async_launched without a completion stays running while the root is
//     live and orphaned otherwise; file mtime is never used;
//   - content: exact when the sidecar jsonl is present and non-empty;
//   - task: source-recorded description only (never the delegated prompt);
//   - execution mode: background when isAsync or TaskOutput is recorded,
//     blocking for a sync Agent completion, otherwise unknown;
//   - no BackingSessionRef: the sidecar is not an independent Session.
//
// Graphs are defined for root Sessions only. Claude does not list sidecar
// files as Sessions; a child-as-root request is still a deterministic error.
func (r *ClaudeReader) ReadCollaboration(ctx context.Context, root model.Session) (collaboration.CollaborationGraph, error) {
	if root.AgentType != "claude" {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"claude collaboration: root session agent type %q is not claude", root.AgentType)
	}
	if !validSessionID(root.ID) {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"claude collaboration: invalid root session id %q", root.ID)
	}
	if root.IsSubagent || root.ParentSessionID != "" {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"claude collaboration: %s is a subagent child of %s; collaboration graphs are defined for root sessions only",
			root.ID, root.ParentSessionID)
	}
	if err := ctx.Err(); err != nil {
		return collaboration.CollaborationGraph{}, err
	}

	path := r.findSessionFile(root.ID)
	if path == "" {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"claude collaboration: root session %q not found", root.ID)
	}

	scan, truncated, err := scanClaudeParentCollaboration(ctx, path)
	if err != nil {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"claude collaboration: root session %q: %w", root.ID, err)
	}

	children, discTrunc, err := discoverClaudeChildren(ctx, path, scan)
	if err != nil {
		return collaboration.CollaborationGraph{}, err
	}

	live := root.IsLive || model.IsSessionLive(root.UpdatedAt)
	graph := collaboration.CollaborationGraph{
		RootAgentType: "claude",
		RootSessionID: root.ID,
		Revision:      model.SessionRevision(root),
		Completeness:  collaboration.ExactFact(),
		Invocations:   []collaboration.AgentInvocation{claudeRootInvocation(root)},
	}
	rootInvID := graph.Invocations[0].ID
	usedFIFO := false
	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return collaboration.CollaborationGraph{}, err
		}
		inv, del := mapClaudeChild(root.ID, rootInvID, child, live)
		graph.Invocations = append(graph.Invocations, inv)
		if del != nil {
			graph.Delegations = append(graph.Delegations, *del)
		}
		if child.triggerFIFO {
			usedFIFO = true
		}
	}

	switch {
	case truncated || discTrunc:
		graph.Completeness = collaboration.FactEvidence{
			State:      collaboration.EvidenceEstimated,
			ReasonCode: collaboration.ReasonSourceNotRecorded,
		}
	case usedFIFO:
		graph.Completeness = collaboration.FactEvidence{
			State:      collaboration.EvidenceEstimated,
			ReasonCode: collaboration.ReasonFIFOJoinHeuristic,
		}
	}

	if v := collaboration.Validate(&graph); !v.OK() {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"claude collaboration: normalized graph violates the contract: %s", v.Issues[0].Detail)
	}
	return graph, nil
}

func claudeRootInvocation(root model.Session) collaboration.AgentInvocation {
	status := collaboration.StatusUnknown
	if root.IsLive || model.IsSessionLive(root.UpdatedAt) {
		status = collaboration.StatusRunning
	}
	return collaboration.AgentInvocation{
		ID:               collaboration.RootInvocationID("claude", root.ID),
		DisplayName:      "claude main agent",
		AgentType:        "claude",
		Status:           status,
		TimePrecision:    collaboration.ExactFact(),
		ContentPrecision: collaboration.ExactFact(),
		SourceIdentity: collaboration.SourceIdentity{
			Kind:     collaboration.IdentityRootSession,
			NativeID: root.ID,
		},
	}
}

// claudeLaunch is one parent-stream Agent/Task tool_use.
type claudeLaunch struct {
	toolUseID    string
	description  string
	subagentType string
	ts           time.Time
}

// claudeAgentResult is a parent toolUseResult that carries agentId.
type claudeAgentResult struct {
	agentID     string
	isAsync     bool
	status      string
	description string
	ts          time.Time
	eventID     string
}

// claudeTaskWait is a TaskOutput tool_use whose input.task_id is the child.
type claudeTaskWait struct {
	toolUseID string
	taskID    string
	ts        time.Time
}

// claudeTaskResult is a TaskOutput toolUseResult whose task.task_id is the child.
type claudeTaskResult struct {
	taskID    string
	status    string
	ts        time.Time
	eventID   string
	toolUseID string
}

type claudeParentScan struct {
	launches     []claudeLaunch
	launchByID   map[string]claudeLaunch
	agentResults []claudeAgentResult
	taskWaits    []claudeTaskWait
	taskResults  []claudeTaskResult
}

type claudeEmbeddedChild struct {
	agentID            string
	agentType          string
	description        string
	toolUseID          string
	triggerFIFO        bool
	hasJSONL           bool
	launch             *claudeLaunch
	asyncLaunch        bool
	completeStatus     string
	completeTS         time.Time
	completeEventID    string
	completeToolCallID string
	hasCompletion      bool
}

func scanClaudeParentCollaboration(ctx context.Context, path string) (*claudeParentScan, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	scan := &claudeParentScan{launchByID: map[string]claudeLaunch{}}
	var pendingTaskWaits []claudeTaskWait

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		var line claudeCollabLine
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			continue
		}
		if line.IsMeta || line.IsSidechain {
			continue
		}
		ts := parseCollabTS(line.Timestamp)
		resultEventID := ""
		if line.UUID != "" {
			resultEventID = line.UUID + "-toolresult"
		}

		if line.Type == "assistant" && len(line.Message.Content) > 0 {
			var blocks []claudeContentBlock
			if json.Unmarshal(line.Message.Content, &blocks) == nil {
				for _, block := range blocks {
					if block.Type != "tool_use" || block.ID == "" {
						continue
					}
					input := map[string]any{}
					if block.Input != nil {
						_ = json.Unmarshal(block.Input, &input)
					}
					switch {
					case isClaudeDelegationTool(block.Name):
						launch := claudeLaunch{
							toolUseID:    block.ID,
							description:  collabStringField(input, "description"),
							subagentType: collabStringField(input, "subagent_type"),
							ts:           ts,
						}
						if _, seen := scan.launchByID[launch.toolUseID]; !seen {
							scan.launches = append(scan.launches, launch)
							scan.launchByID[launch.toolUseID] = launch
						}
					case block.Name == "TaskOutput":
						wait := claudeTaskWait{
							toolUseID: block.ID,
							taskID:    collabStringField(input, "task_id"),
							ts:        ts,
						}
						scan.taskWaits = append(scan.taskWaits, wait)
						pendingTaskWaits = append(pendingTaskWaits, wait)
					}
				}
			}
		}

		if line.Type == "user" && len(line.ToolUseResult) > 0 {
			var result claudeCollabToolResult
			if json.Unmarshal(line.ToolUseResult, &result) != nil {
				continue
			}
			if result.AgentID != "" && safeClaudeAgentID(result.AgentID) {
				scan.agentResults = append(scan.agentResults, claudeAgentResult{
					agentID:     result.AgentID,
					isAsync:     result.IsAsync,
					status:      result.Status,
					description: result.Description,
					ts:          ts,
					eventID:     resultEventID,
				})
			}
			if result.Task != nil && result.Task.TaskID != "" && safeClaudeAgentID(result.Task.TaskID) {
				tr := claudeTaskResult{
					taskID:  result.Task.TaskID,
					status:  result.Task.Status,
					ts:      ts,
					eventID: resultEventID,
				}
				pendingTaskWaits, tr.toolUseID = takeClaudeTaskWait(pendingTaskWaits, tr.taskID)
				scan.taskResults = append(scan.taskResults, tr)
			}
		}
	}
	if err := sc.Err(); err != nil {
		if err == bufio.ErrTooLong {
			return scan, true, nil
		}
		return nil, false, err
	}
	return scan, false, nil
}

type claudeCollabLine struct {
	Type        string `json:"type"`
	UUID        string `json:"uuid"`
	Timestamp   string `json:"timestamp"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

type claudeCollabToolResult struct {
	AgentID     string `json:"agentId"`
	IsAsync     bool   `json:"isAsync"`
	Status      string `json:"status"`
	Description string `json:"description"`
	Task        *struct {
		TaskID string `json:"task_id"`
		Status string `json:"status"`
	} `json:"task"`
}

type claudeSubagentMeta struct {
	AgentType   string `json:"agentType"`
	Description string `json:"description"`
	ToolUseID   string `json:"toolUseId"`
	AgentID     string `json:"agentId"`
}

func discoverClaudeChildren(ctx context.Context, mainPath string, scan *claudeParentScan) ([]claudeEmbeddedChild, bool, error) {
	byID := map[string]*claudeEmbeddedChild{}
	order := make([]string, 0)
	ensure := func(id string) *claudeEmbeddedChild {
		if c := byID[id]; c != nil {
			return c
		}
		c := &claudeEmbeddedChild{agentID: id}
		byID[id] = c
		order = append(order, id)
		return c
	}

	sessionDir := strings.TrimSuffix(mainPath, ".jsonl")
	subDir := filepath.Join(sessionDir, "subagents")
	truncated := false
	if entries, err := os.ReadDir(subDir); err == nil {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if !e.IsDir() {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
			id, kind := claudeSidecarIdentity(name)
			if id == "" || !safeClaudeAgentID(id) {
				continue
			}
			child := ensure(id)
			p := filepath.Join(subDir, name)
			switch kind {
			case "jsonl":
				if info, err := os.Stat(p); err != nil {
					if !os.IsNotExist(err) {
						truncated = true
					}
				} else if info.Mode().IsRegular() && info.Size() > 0 {
					child.hasJSONL = true
				}
			case "meta":
				meta, err := readClaudeSubagentMeta(p)
				if err != nil {
					truncated = true
					continue
				}
				if meta.AgentType != "" {
					child.agentType = meta.AgentType
				}
				if meta.Description != "" {
					child.description = meta.Description
				}
				if meta.ToolUseID != "" {
					child.toolUseID = meta.ToolUseID
				}
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, false, err
	}

	for _, res := range scan.agentResults {
		child := ensure(res.agentID)
		if res.description != "" && child.description == "" {
			child.description = res.description
		}
		if res.isAsync || res.status == "async_launched" {
			child.asyncLaunch = true
		} else if !child.hasCompletion {
			child.hasCompletion = true
			child.completeStatus = firstNonEmpty(res.status, "completed")
			child.completeTS = res.ts
			child.completeEventID = res.eventID
		}
	}
	waitByTask := map[string]claudeTaskWait{}
	for _, w := range scan.taskWaits {
		if w.taskID != "" {
			waitByTask[w.taskID] = w
			ensure(w.taskID).asyncLaunch = true
		}
	}
	for _, tr := range scan.taskResults {
		child := ensure(tr.taskID)
		child.asyncLaunch = true
		child.hasCompletion = true
		child.completeStatus = tr.status
		child.completeTS = tr.ts
		child.completeEventID = tr.eventID
		child.completeToolCallID = tr.toolUseID
		if child.completeToolCallID == "" {
			if w, ok := waitByTask[tr.taskID]; ok {
				child.completeToolCallID = w.toolUseID
			}
		}
	}

	usedLaunch := map[string]bool{}
	for _, child := range byID {
		if child.toolUseID != "" {
			if launch, ok := scan.launchByID[child.toolUseID]; ok {
				cp := launch
				child.launch = &cp
				usedLaunch[child.toolUseID] = true
			}
		}
	}

	// Remaining Agent/Task launches pair FIFO with agentId results that
	// still lack a toolUseId join. This is the documented estimated path.
	unpairedResults := make([]claudeAgentResult, 0, len(scan.agentResults))
	seenResult := map[string]bool{}
	for _, res := range scan.agentResults {
		if seenResult[res.agentID] {
			continue
		}
		seenResult[res.agentID] = true
		if child := byID[res.agentID]; child != nil && child.launch == nil {
			unpairedResults = append(unpairedResults, res)
		}
	}
	li := 0
	for _, res := range unpairedResults {
		for li < len(scan.launches) && usedLaunch[scan.launches[li].toolUseID] {
			li++
		}
		if li >= len(scan.launches) {
			break
		}
		launch := scan.launches[li]
		li++
		usedLaunch[launch.toolUseID] = true
		child := byID[res.agentID]
		cp := launch
		child.launch = &cp
		child.toolUseID = launch.toolUseID
		child.triggerFIFO = true
		if child.description == "" {
			child.description = launch.description
		}
		if child.agentType == "" {
			child.agentType = launch.subagentType
		}
	}

	for _, child := range byID {
		if child.launch != nil {
			if child.description == "" {
				child.description = child.launch.description
			}
			if child.agentType == "" {
				child.agentType = child.launch.subagentType
			}
			if child.completeToolCallID == "" && child.hasCompletion && !child.asyncLaunch {
				child.completeToolCallID = child.launch.toolUseID
			}
		}
	}

	children := make([]claudeEmbeddedChild, 0, len(order))
	for _, id := range order {
		children = append(children, *byID[id])
	}
	sort.SliceStable(children, func(i, j int) bool {
		ti, tj := childLaunchTime(children[i]), childLaunchTime(children[j])
		if !ti.Equal(tj) {
			if ti.IsZero() {
				return false
			}
			if tj.IsZero() {
				return true
			}
			return ti.Before(tj)
		}
		return children[i].agentID < children[j].agentID
	})
	return children, truncated, nil
}

func childLaunchTime(c claudeEmbeddedChild) time.Time {
	if c.launch != nil && !c.launch.ts.IsZero() {
		return c.launch.ts
	}
	return time.Time{}
}

func mapClaudeChild(rootSessionID, rootInvID string, child claudeEmbeddedChild, rootLive bool) (collaboration.AgentInvocation, *collaboration.Delegation) {
	childInvID := collaboration.ChildInvocationID("claude", rootSessionID, child.agentID)

	display := child.description
	if display == "" {
		display = child.agentType
	}
	if display == "" {
		display = "claude child agent"
	}

	status := normalizeClaudeChildStatus(child.completeStatus, child.hasCompletion, child.launch != nil || child.asyncLaunch, rootLive)

	var startedAt time.Time
	if child.launch != nil {
		startedAt = child.launch.ts
	}
	endedAt := time.Time{}
	hasEnd := child.hasCompletion && !child.completeTS.IsZero()
	if hasEnd {
		endedAt = child.completeTS
		if !startedAt.IsZero() && endedAt.Before(startedAt) {
			hasEnd = false
			endedAt = time.Time{}
		}
	}

	inv := collaboration.AgentInvocation{
		ID:            childInvID,
		DisplayName:   display,
		AgentType:     "claude",
		RoleLabel:     child.agentType,
		Status:        status,
		TimePrecision: claudeTimePrecision(!startedAt.IsZero(), hasEnd),
		SourceIdentity: collaboration.SourceIdentity{
			Kind:     collaboration.IdentityAgentID,
			NativeID: child.agentID,
		},
	}
	if child.toolUseID != "" {
		inv.SourceIdentity.Attributes = map[string]string{"tool_use_id": child.toolUseID}
	}
	if !startedAt.IsZero() {
		t := startedAt
		inv.StartedAt = &t
	}
	if hasEnd {
		t := endedAt
		inv.EndedAt = &t
	}
	if child.hasJSONL {
		inv.ContentPrecision = collaboration.ExactFact()
	} else {
		inv.ContentPrecision = collaboration.FactEvidence{
			State:      collaboration.EvidenceMissing,
			ReasonCode: collaboration.ReasonSourceNotRecorded,
		}
	}

	if child.launch == nil {
		return inv, nil
	}

	triggerPrec := collaboration.ExactFact()
	if child.triggerFIFO {
		triggerPrec = collaboration.FactEvidence{
			State:      collaboration.EvidenceEstimated,
			ReasonCode: collaboration.ReasonFIFOJoinHeuristic,
		}
	}
	trigger := &collaboration.SourceAnchor{
		AgentType:  "claude",
		SessionID:  rootSessionID,
		EventID:    child.launch.toolUseID,
		ToolCallID: child.launch.toolUseID,
		Precision:  triggerPrec,
	}
	if !child.launch.ts.IsZero() {
		if !startedAt.IsZero() && child.launch.ts.After(startedAt) {
			trigger.Precision = collaboration.FactEvidence{
				State:      collaboration.EvidenceMissing,
				ReasonCode: collaboration.ReasonTimestampContradiction,
			}
		} else {
			ts := child.launch.ts
			trigger.Timestamp = &ts
		}
	}

	mode := collaboration.ExecutionUnknown
	switch {
	case child.asyncLaunch:
		mode = collaboration.ExecutionBackground
	case child.hasCompletion:
		mode = collaboration.ExecutionBlocking
	}

	del := &collaboration.Delegation{
		ID:                 collaboration.DelegationIDFor(rootInvID, childInvID),
		ParentInvocationID: rootInvID,
		ChildInvocationID:  childInvID,
		Trigger:            trigger,
		ExecutionMode:      mode,
		Evidence: collaboration.DelegationEvidence{
			Trigger: triggerPrec,
			Timing:  inv.TimePrecision,
		},
	}
	if child.description != "" {
		del.TaskSummary = child.description
		del.Evidence.Task = collaboration.ExactFact()
	} else {
		del.Evidence.Task = collaboration.FactEvidence{
			State:      collaboration.EvidenceMissing,
			ReasonCode: collaboration.ReasonSourceNotRecorded,
		}
	}
	if child.hasCompletion {
		anchor := &collaboration.SourceAnchor{
			AgentType:  "claude",
			SessionID:  rootSessionID,
			EventID:    child.completeEventID,
			ToolCallID: child.completeToolCallID,
			Precision:  collaboration.ExactFact(),
		}
		if !child.completeTS.IsZero() {
			ts := child.completeTS
			anchor.Timestamp = &ts
		}
		del.Result = anchor
		del.Evidence.Result = collaboration.ExactFact()
	} else {
		// launch is non-nil here; an open child has a start but no completion.
		del.Evidence.Result = collaboration.FactEvidence{
			State:      collaboration.EvidenceMissing,
			ReasonCode: collaboration.ReasonCompletionNotRecorded,
		}
	}
	return inv, del
}

func normalizeClaudeChildStatus(raw string, hasCompletion, hasStart, rootLive bool) collaboration.InvocationStatus {
	if hasCompletion {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "", "completed", "success", "ok", "succeeded":
			return collaboration.StatusCompleted
		case "failed", "error", "errored":
			return collaboration.StatusFailed
		case "cancelled", "canceled", "rejected", "interrupted":
			return collaboration.StatusCancelled
		default:
			return collaboration.StatusUnknown
		}
	}
	if hasStart {
		if rootLive {
			return collaboration.StatusRunning
		}
		return collaboration.StatusOrphaned
	}
	return collaboration.StatusUnknown
}

func claudeTimePrecision(hasStart, hasEnd bool) collaboration.FactEvidence {
	switch {
	case hasStart && hasEnd:
		return collaboration.ExactFact()
	case hasStart:
		return collaboration.FactEvidence{
			State:      collaboration.EvidenceEstimated,
			ReasonCode: collaboration.ReasonCompletionNotRecorded,
		}
	case hasEnd:
		// Completion timestamp is recorded; the launch clock is not.
		return collaboration.FactEvidence{
			State:      collaboration.EvidenceEstimated,
			ReasonCode: collaboration.ReasonSourceNotRecorded,
		}
	default:
		return collaboration.FactEvidence{
			State:      collaboration.EvidenceMissing,
			ReasonCode: collaboration.ReasonSourceNotRecorded,
		}
	}
}

// takeClaudeTaskWait joins a TaskOutput result to its wait. An exact task_id
// match wins; otherwise the oldest unmatched wait is used (FIFO fallback).
func takeClaudeTaskWait(pending []claudeTaskWait, taskID string) ([]claudeTaskWait, string) {
	if len(pending) == 0 {
		return pending, ""
	}
	idx := 0
	for i, w := range pending {
		if w.taskID != "" && w.taskID == taskID {
			idx = i
			break
		}
	}
	id := pending[idx].toolUseID
	return append(pending[:idx], pending[idx+1:]...), id
}

func isClaudeDelegationTool(name string) bool {
	return name == "Agent" || name == "Task"
}

func safeClaudeAgentID(id string) bool {
	return id != "" && filepath.Base(id) == id && id != "." && id != ".."
}

func claudeSidecarIdentity(name string) (agentID, kind string) {
	switch {
	case strings.HasPrefix(name, "agent-") && strings.HasSuffix(name, ".meta.json"):
		return strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".meta.json"), "meta"
	case strings.HasPrefix(name, "agent-") && strings.HasSuffix(name, ".jsonl"):
		return strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl"), "jsonl"
	default:
		return "", ""
	}
}

func readClaudeSubagentMeta(path string) (claudeSubagentMeta, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return claudeSubagentMeta{}, err
	}
	var meta claudeSubagentMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		return claudeSubagentMeta{}, err
	}
	return meta, nil
}

func parseCollabTS(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func collabStringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
