package grok

import (
	"bufio"
	"bytes"
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

// ReadCollaboration implements reader.CollaborationReader for Grok's
// structured-subagent archetype: children are recorded under
// <session>/subagents/<id>/meta.json and matching parent-stream
// subagent_spawned / subagent_finished updates, joined on subagent_id.
//
// Mapping (contract + frozen identity extension IdentitySubagentID):
//   - identity: native subagent_id namespaced by the root session;
//   - lineage: meta.parent_session_id / spawn parent_session_id (exact IDs);
//   - backing: BackingSessionRef only when child_session_id resolves to a
//     readable standalone Grok Session (summary.json present);
//   - anchors: trigger/result from spawn/finish with source eventId when
//     present, else strongest exact timestamp from the lifecycle stream;
//   - status/timing: meta.started_at/completed_at and/or lifecycle events;
//     never mtime or duration_ms alone; unknown status stays unknown;
//   - task: source-recorded description only (never full prompt/output);
//   - execution mode: unknown (effective_context_source is not mode).
//
// Nesting: when a child session itself has subagents/, descendants are
// included recursively with cycle/duplicate guards. Graphs are defined for
// root Sessions only; a child-as-root request is a deterministic error.
func (r *GrokReader) ReadCollaboration(ctx context.Context, root model.Session) (collaboration.CollaborationGraph, error) {
	if root.AgentType != "grok" {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"grok collaboration: root session agent type %q is not grok", root.AgentType)
	}
	if !validSessionID(root.ID) {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"grok collaboration: invalid root session id %q", root.ID)
	}
	if root.IsSubagent || root.ParentSessionID != "" {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"grok collaboration: %s is a subagent child of %s; collaboration graphs are defined for root sessions only",
			root.ID, root.ParentSessionID)
	}
	if err := ctx.Err(); err != nil {
		return collaboration.CollaborationGraph{}, err
	}

	loc, err := r.findSession(root.ID)
	if err != nil {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"grok collaboration: root session %q: %w", root.ID, err)
	}

	live := root.IsLive || model.IsSessionLive(root.UpdatedAt)
	graph := collaboration.CollaborationGraph{
		RootAgentType: "grok",
		RootSessionID: root.ID,
		Revision:      model.SessionRevision(root),
		Completeness:  collaboration.ExactFact(),
		Invocations:   []collaboration.AgentInvocation{grokRootInvocation(root)},
	}
	rootInvID := graph.Invocations[0].ID

	seen := map[string]bool{} // native subagent_id already emitted
	partial, err := r.appendGrokChildren(ctx, &graph, root.ID, rootInvID, loc.Dir, root.ID, live, seen)
	if err != nil {
		return collaboration.CollaborationGraph{}, err
	}
	if partial {
		// Truncated lifecycle stream or non-missing FS errors during nest
		// discovery: keep the graph but do not claim exact completeness.
		graph.Completeness = collaboration.FactEvidence{
			State:      collaboration.EvidenceEstimated,
			ReasonCode: collaboration.ReasonSourceNotRecorded,
		}
	}

	if v := collaboration.Validate(&graph); !v.OK() {
		return collaboration.CollaborationGraph{}, fmt.Errorf(
			"grok collaboration: normalized graph violates the contract: %s", v.Issues[0].Detail)
	}
	return graph, nil
}

func grokRootInvocation(root model.Session) collaboration.AgentInvocation {
	status := collaboration.StatusUnknown
	if root.IsLive || model.IsSessionLive(root.UpdatedAt) {
		status = collaboration.StatusRunning
	}
	return collaboration.AgentInvocation{
		ID:               collaboration.RootInvocationID("grok", root.ID),
		DisplayName:      "grok main agent",
		AgentType:        "grok",
		Status:           status,
		TimePrecision:    collaboration.ExactFact(),
		ContentPrecision: collaboration.ExactFact(),
		SourceIdentity: collaboration.SourceIdentity{
			Kind:     collaboration.IdentityRootSession,
			NativeID: root.ID,
		},
	}
}

// appendGrokChildren discovers and merges children for one parent session
// directory into the graph under parentInvID. The bool is true when any
// lifecycle scan was truncated (oversized line).
func (r *GrokReader) appendGrokChildren(
	ctx context.Context,
	graph *collaboration.CollaborationGraph,
	rootSessionID, parentInvID, parentDir, parentSessionID string,
	rootLive bool,
	seen map[string]bool,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	children, truncated, err := r.discoverGrokChildren(ctx, parentDir, parentSessionID)
	if err != nil {
		return false, err
	}

	type nest struct {
		childSessionID string
		childInvID     string
		childDir       string
	}
	var nests []nest

	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return truncated, err
		}
		if child.subagentID == "" || seen[child.subagentID] {
			continue
		}
		// Conflicting parent IDs: only emit the edge when the recorded parent
		// matches this session (or parent is unrecorded). A child claimed by
		// another parent is left for that parent's discovery.
		if child.parentSessionID != "" && child.parentSessionID != parentSessionID {
			continue
		}
		seen[child.subagentID] = true

		inv, del := r.mapGrokChild(rootSessionID, parentInvID, parentSessionID, child, rootLive)
		graph.Invocations = append(graph.Invocations, inv)
		graph.Delegations = append(graph.Delegations, del)

		// Nesting uses the child session directory + subagents/, not
		// readable-backing (summary.json). Intermediate dirs without a full
		// Session still contribute descendants; BackingSessionRef stays gated.
		// Missing paths are "no descendants"; other FS errors mark incomplete
		// rather than claiming an exact zero-descendant branch.
		if child.childSessionID != "" {
			childDir, err := r.findSessionDir(child.childSessionID)
			if err != nil {
				truncated = true
				continue
			}
			if childDir == "" {
				continue
			}
			info, err := os.Stat(filepath.Join(childDir, "subagents"))
			if err != nil {
				if !os.IsNotExist(err) {
					truncated = true
				}
				continue
			}
			if info.IsDir() {
				nests = append(nests, nest{childSessionID: child.childSessionID, childInvID: inv.ID, childDir: childDir})
			}
		}
	}

	for _, n := range nests {
		if err := ctx.Err(); err != nil {
			return truncated, err
		}
		dir := n.childDir
		if dir == "" {
			var err error
			dir, err = r.findSessionDir(n.childSessionID)
			if err != nil {
				return true, nil // incomplete, not a hard failure of the root graph
			}
		}
		if dir == "" {
			continue
		}
		nestedTrunc, err := r.appendGrokChildren(ctx, graph, rootSessionID, n.childInvID, dir, n.childSessionID, rootLive, seen)
		if err != nil {
			return truncated || nestedTrunc, err
		}
		truncated = truncated || nestedTrunc
	}
	return truncated, nil
}

// grokChildRec holds merged sidecar + lifecycle facts for one native child.
type grokChildRec struct {
	subagentID      string
	parentSessionID string
	childSessionID  string
	subagentType    string
	description     string
	statusRaw       string
	startedAt       time.Time
	endedAt         time.Time
	hasStart        bool
	hasEnd          bool
	spawnEventID    string
	finishEventID   string
	spawnTS         time.Time
	finishTS        time.Time
	hasSpawn        bool
	hasFinish       bool
	fromMeta        bool
}

// discoverGrokChildren merges subagents/*/meta.json with parent updates.jsonl
// lifecycle events for the same subagent_id. Ordering is deterministic:
// earliest start time, then subagent_id. Malformed records are skipped.
// The bool is true when the parent lifecycle stream was truncated mid-scan.
func (r *GrokReader) discoverGrokChildren(ctx context.Context, parentDir, parentSessionID string) ([]*grokChildRec, bool, error) {
	byID := map[string]*grokChildRec{}

	subRoot := filepath.Join(parentDir, "subagents")
	if entries, err := os.ReadDir(subRoot); err == nil {
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, ent := range entries {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
			if !ent.IsDir() {
				continue
			}
			metaPath := filepath.Join(subRoot, ent.Name(), "meta.json")
			meta, ok := readSubagentMeta(metaPath)
			if !ok {
				continue
			}
			id := strings.TrimSpace(meta.SubagentID)
			if id == "" {
				id = strings.TrimSpace(meta.ChildSessionID)
			}
			if id == "" && validSessionID(ent.Name()) {
				id = ent.Name()
			}
			if id == "" {
				continue
			}
			rec := byID[id]
			if rec == nil {
				rec = &grokChildRec{subagentID: id}
				byID[id] = rec
			}
			rec.fromMeta = true
			if p := strings.TrimSpace(meta.ParentSessionID); p != "" && rec.parentSessionID == "" {
				rec.parentSessionID = p
			}
			if c := strings.TrimSpace(meta.ChildSessionID); c != "" {
				rec.childSessionID = c
			} else if rec.childSessionID == "" {
				rec.childSessionID = id
			}
			if t := strings.TrimSpace(meta.SubagentType); t != "" {
				rec.subagentType = t
			}
			if d := strings.TrimSpace(meta.Description); d != "" {
				// Never use prompt; description is the source summary field.
				rec.description = d
			}
			if s := strings.TrimSpace(meta.Status); s != "" && rec.statusRaw == "" {
				rec.statusRaw = s
			}
			if st := parseTS(meta.StartedAt); !st.IsZero() {
				rec.startedAt = st
				rec.hasStart = true
			}
			if et := parseTS(meta.CompletedAt); !et.IsZero() {
				rec.endedAt = et
				rec.hasEnd = true
			}
		}
	}

	stream, truncated, err := scanSubagentLifecycle(ctx, filepath.Join(parentDir, "updates.jsonl"))
	if err != nil {
		return nil, false, err
	}
	for _, ev := range stream {
		if err := ctx.Err(); err != nil {
			return nil, truncated, err
		}
		id := strings.TrimSpace(ev.subagentID)
		if id == "" {
			continue
		}
		rec := byID[id]
		if rec == nil {
			rec = &grokChildRec{subagentID: id}
			byID[id] = rec
		}
		if p := strings.TrimSpace(ev.parentSessionID); p != "" && rec.parentSessionID == "" {
			rec.parentSessionID = p
		}
		if c := strings.TrimSpace(ev.childSessionID); c != "" && rec.childSessionID == "" {
			rec.childSessionID = c
		}
		if t := strings.TrimSpace(ev.subagentType); t != "" && rec.subagentType == "" {
			rec.subagentType = t
		}
		if d := strings.TrimSpace(ev.description); d != "" && rec.description == "" {
			rec.description = d
		}
		switch ev.kind {
		case "spawned":
			rec.hasSpawn = true
			if ev.eventID != "" {
				rec.spawnEventID = ev.eventID
			}
			if !ev.ts.IsZero() {
				rec.spawnTS = ev.ts
				// Prefer structured meta start when present (stronger RFC3339).
				if !rec.hasStart {
					rec.startedAt = ev.ts
					rec.hasStart = true
				}
			}
		case "finished":
			rec.hasFinish = true
			if ev.eventID != "" {
				rec.finishEventID = ev.eventID
			}
			if s := strings.TrimSpace(ev.status); s != "" {
				// Finish status is the terminal lifecycle fact; overrides meta.
				rec.statusRaw = s
			}
			if !ev.ts.IsZero() {
				rec.finishTS = ev.ts
				if !rec.hasEnd {
					rec.endedAt = ev.ts
					rec.hasEnd = true
				}
			}
		}
	}

	for _, rec := range byID {
		if rec.parentSessionID == "" {
			rec.parentSessionID = parentSessionID
		}
		if rec.childSessionID == "" {
			rec.childSessionID = rec.subagentID
		}
	}

	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := byID[ids[i]], byID[ids[j]]
		if a.hasStart != b.hasStart {
			return a.hasStart
		}
		if a.hasStart && b.hasStart && !a.startedAt.Equal(b.startedAt) {
			return a.startedAt.Before(b.startedAt)
		}
		return ids[i] < ids[j]
	})
	out := make([]*grokChildRec, 0, len(ids))
	for _, id := range ids {
		out = append(out, byID[id])
	}
	return out, truncated, nil
}

// subagentMeta is the on-disk collaboration shape. prompt/output are never
// unmarshaled into TaskSummary; they are ignored by omitting them.
type subagentMeta struct {
	SubagentID             string `json:"subagent_id"`
	ParentSessionID        string `json:"parent_session_id"`
	ChildSessionID         string `json:"child_session_id"`
	SubagentType           string `json:"subagent_type"`
	Description            string `json:"description"`
	Status                 string `json:"status"`
	StartedAt              string `json:"started_at"`
	CompletedAt            string `json:"completed_at"`
	EffectiveContextSource string `json:"effective_context_source"`
	EffectiveModelID       string `json:"effective_model_id"`
}

func readSubagentMeta(path string) (subagentMeta, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return subagentMeta{}, false
	}
	var m subagentMeta
	if json.Unmarshal(data, &m) != nil {
		return subagentMeta{}, false
	}
	return m, true
}

type subagentLifecycleEvent struct {
	kind            string // spawned | finished
	subagentID      string
	parentSessionID string
	childSessionID  string
	subagentType    string
	description     string
	status          string
	eventID         string
	ts              time.Time
}

// scanSubagentLifecycle incrementally scans updates.jsonl for subagent
// lifecycle events only. Bounded scanner; skips oversized/malformed lines.
// Does not retain prompt/output bodies. The bool is true when an oversized
// line aborted the remainder of the scan (partial stream).
func scanSubagentLifecycle(ctx context.Context, path string) ([]subagentLifecycleEvent, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer f.Close()

	const maxLine = 10 * 1024 * 1024
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)

	var out []subagentLifecycleEvent
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		line := sc.Bytes()
		if len(line) == 0 || !bytes.Contains(line, []byte(`"subagent_`)) {
			continue
		}
		var wrap struct {
			Timestamp int64 `json:"timestamp"`
			Params    struct {
				Update json.RawMessage `json:"update"`
				Meta   *struct {
					EventID          string `json:"eventId"`
					AgentTimestampMs int64  `json:"agentTimestampMs"`
				} `json:"_meta"`
			} `json:"params"`
		}
		if json.Unmarshal(line, &wrap) != nil || len(wrap.Params.Update) == 0 {
			continue
		}
		var u struct {
			SessionUpdate   string `json:"sessionUpdate"`
			SubagentID      string `json:"subagent_id"`
			ParentSessionID string `json:"parent_session_id"`
			ChildSessionID  string `json:"child_session_id"`
			SubagentType    string `json:"subagent_type"`
			Description     string `json:"description"`
			Status          string `json:"status"`
		}
		if json.Unmarshal(wrap.Params.Update, &u) != nil {
			continue
		}
		var kind string
		switch u.SessionUpdate {
		case "subagent_spawned":
			kind = "spawned"
		case "subagent_finished":
			kind = "finished"
		default:
			continue
		}
		if strings.TrimSpace(u.SubagentID) == "" {
			continue
		}
		ev := subagentLifecycleEvent{
			kind:            kind,
			subagentID:      strings.TrimSpace(u.SubagentID),
			parentSessionID: strings.TrimSpace(u.ParentSessionID),
			childSessionID:  strings.TrimSpace(u.ChildSessionID),
			subagentType:    strings.TrimSpace(u.SubagentType),
			description:     strings.TrimSpace(u.Description),
			status:          strings.TrimSpace(u.Status),
		}
		if wrap.Params.Meta != nil {
			ev.eventID = strings.TrimSpace(wrap.Params.Meta.EventID)
			if wrap.Params.Meta.AgentTimestampMs > 0 {
				ev.ts = time.UnixMilli(wrap.Params.Meta.AgentTimestampMs).UTC()
			}
		}
		if ev.ts.IsZero() && wrap.Timestamp > 0 {
			// Grok records wall seconds on the outer envelope.
			ev.ts = time.Unix(wrap.Timestamp, 0).UTC()
		}
		out = append(out, ev)
	}
	if err := sc.Err(); err != nil {
		// Oversized line aborts the remainder of the file for this scan.
		// Return partial events and mark truncated so Completeness degrades.
		if err == bufio.ErrTooLong {
			return out, true, nil
		}
		return out, false, err
	}
	return out, false, nil
}

func (r *GrokReader) mapGrokChild(
	rootSessionID, parentInvID, parentSessionID string,
	child *grokChildRec,
	rootLive bool,
) (collaboration.AgentInvocation, collaboration.Delegation) {
	childInvID := collaboration.ChildInvocationID("grok", rootSessionID, child.subagentID)

	display := child.description
	if display == "" {
		display = child.subagentType
	}
	if display == "" {
		display = "grok child agent"
	}

	status := normalizeGrokSubagentStatus(child.statusRaw)
	if status == collaboration.StatusUnknown && !child.hasFinish {
		if child.hasSpawn || child.hasStart {
			if rootLive {
				status = collaboration.StatusRunning
			} else {
				status = collaboration.StatusOrphaned
			}
		}
	}

	hasStart, hasEnd := child.hasStart, child.hasEnd
	startedAt, endedAt := child.startedAt, child.endedAt
	// Conflict policy: never emit causally impossible timestamps.
	if hasStart && hasEnd && endedAt.Before(startedAt) {
		hasEnd = false
		endedAt = time.Time{}
	}

	inv := collaboration.AgentInvocation{
		ID:            childInvID,
		DisplayName:   display,
		AgentType:     "grok",
		RoleLabel:     child.subagentType,
		Status:        status,
		TimePrecision: grokTimePrecision(hasStart, hasEnd),
		SourceIdentity: collaboration.SourceIdentity{
			Kind:     collaboration.IdentitySubagentID,
			NativeID: child.subagentID,
		},
	}
	if child.childSessionID != "" && child.childSessionID != child.subagentID {
		inv.SourceIdentity.Attributes = map[string]string{
			"child_session_id": child.childSessionID,
		}
	}
	if hasStart {
		t := startedAt
		inv.StartedAt = &t
	}
	if hasEnd {
		t := endedAt
		inv.EndedAt = &t
	}

	// Backing only when a readable standalone Session exists.
	if child.childSessionID != "" && r.sessionHasReadableBacking(child.childSessionID) {
		inv.BackingSession = &collaboration.BackingSessionRef{
			AgentType: "grok",
			SessionID: child.childSessionID,
		}
		inv.ContentPrecision = collaboration.ExactFact()
	} else {
		inv.ContentPrecision = collaboration.FactEvidence{
			State:      collaboration.EvidenceMissing,
			ReasonCode: collaboration.ReasonSourceNotRecorded,
		}
	}

	anchorSession := parentSessionID
	if child.parentSessionID != "" {
		anchorSession = child.parentSessionID
	}

	del := collaboration.Delegation{
		ID:                 collaboration.DelegationIDFor(parentInvID, childInvID),
		ParentInvocationID: parentInvID,
		ChildInvocationID:  childInvID,
		ExecutionMode:      collaboration.ExecutionUnknown,
		Evidence: collaboration.DelegationEvidence{
			Timing: inv.TimePrecision,
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

	if child.hasSpawn {
		anchor := &collaboration.SourceAnchor{
			AgentType: "grok",
			SessionID: anchorSession,
			Precision: collaboration.ExactFact(),
		}
		if child.spawnEventID != "" {
			anchor.EventID = child.spawnEventID
		}
		if !child.spawnTS.IsZero() {
			ts := child.spawnTS
			// The harness records the child's started_at (ns clock) before it
			// emits the subagent_spawned update (ms clock), so the raw spawn
			// timestamp can post-date started_at by a few milliseconds for the
			// same launch fact. The contract forbids a trigger post-dating the
			// child's start, so fall back to the stronger meta timestamp.
			switch {
			case hasStart && ts.After(startedAt):
				ts = startedAt
			case !hasStart && hasEnd && ts.After(endedAt):
				// No start fact and the spawn update post-dates the recorded
				// end: withhold the contradictory timestamp, keep the anchor.
				ts = time.Time{}
			}
			if !ts.IsZero() {
				anchor.Timestamp = &ts
			}
		} else if hasStart {
			ts := startedAt
			anchor.Timestamp = &ts
		}
		del.Trigger = anchor
		del.Evidence.Trigger = collaboration.ExactFact()
	} else {
		del.Evidence.Trigger = collaboration.FactEvidence{
			State:      collaboration.EvidenceMissing,
			ReasonCode: collaboration.ReasonSourceNotRecorded,
		}
	}

	if child.hasFinish {
		anchor := &collaboration.SourceAnchor{
			AgentType: "grok",
			SessionID: anchorSession,
			Precision: collaboration.ExactFact(),
		}
		if child.finishEventID != "" {
			anchor.EventID = child.finishEventID
		}
		if !child.finishTS.IsZero() {
			ts := child.finishTS
			anchor.Timestamp = &ts
		} else if hasEnd {
			ts := endedAt
			anchor.Timestamp = &ts
		}
		del.Result = anchor
		del.Evidence.Result = collaboration.ExactFact()
	} else if child.hasSpawn || child.hasStart {
		del.Evidence.Result = collaboration.FactEvidence{
			State:      collaboration.EvidenceMissing,
			ReasonCode: collaboration.ReasonCompletionNotRecorded,
		}
	} else {
		del.Evidence.Result = collaboration.FactEvidence{
			State:      collaboration.EvidenceMissing,
			ReasonCode: collaboration.ReasonSourceNotRecorded,
		}
	}

	return inv, del
}

func (r *GrokReader) sessionHasReadableBacking(sessionID string) bool {
	if !validSessionID(sessionID) {
		return false
	}
	loc, err := r.findSession(sessionID)
	if err != nil {
		return false
	}
	_, err = os.Stat(loc.SummaryPath)
	return err == nil
}

// findSessionDir locates a session directory by UUID even when summary.json is
// absent (partial write). Used for nested subagent discovery only; list/get
// still require a summary for a discoverable Session.
// Returns ("", nil) when the id is not present; non-nil error for I/O failures
// other than not-exist (callers must not treat those as "no descendants").
func (r *GrokReader) findSessionDir(id string) (string, error) {
	if !validSessionID(id) {
		return "", nil
	}
	if loc, err := r.findSession(id); err == nil {
		return loc.Dir, nil
	}
	entries, err := os.ReadDir(r.sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var firstStatErr error
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		dir := filepath.Join(r.sessionsDir, ent.Name(), id)
		st, err := os.Stat(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if firstStatErr == nil {
				firstStatErr = err
			}
			continue
		}
		if st.IsDir() {
			return dir, nil
		}
	}
	if firstStatErr != nil {
		return "", firstStatErr
	}
	return "", nil
}

func grokTimePrecision(hasStart, hasEnd bool) collaboration.FactEvidence {
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

func normalizeGrokSubagentStatus(s string) collaboration.InvocationStatus {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "completed", "complete", "success", "succeeded":
		return collaboration.StatusCompleted
	case "failed", "error":
		return collaboration.StatusFailed
	case "cancelled", "canceled", "interrupted", "aborted":
		return collaboration.StatusCancelled
	case "running", "in_progress", "in-progress":
		return collaboration.StatusRunning
	case "pending", "queued":
		return collaboration.StatusPending
	case "waiting":
		return collaboration.StatusWaiting
	case "":
		return collaboration.StatusUnknown
	default:
		return collaboration.StatusUnknown
	}
}
