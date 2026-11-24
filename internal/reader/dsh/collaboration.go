package dsh

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/model"
)

type collaborationCatalogEntry struct {
	ChildID string `json:"childId"`
	Label   string `json:"label"`
	Mode    string `json:"mode"`
}

type collaborationTranscript struct {
	session    model.Session
	complete   bool
	catalog    map[string]collaborationCatalogEntry
	settled    map[string]collaboration.SourceAnchor
	active     map[int]time.Time
	activity   []collaboration.ActivitySpan
	lastStatus collaboration.InvocationStatus
}

var missingCollaborationFact = collaboration.FactEvidence{
	State: collaboration.EvidenceMissing, ReasonCode: collaboration.ReasonSourceNotRecorded,
}

func (reader *Reader) readCollaborationTranscript(sessionID string) (collaborationTranscript, error) {
	transcript := collaborationTranscript{
		catalog:    make(map[string]collaborationCatalogEntry),
		settled:    make(map[string]collaboration.SourceAnchor),
		active:     make(map[int]time.Time),
		lastStatus: collaboration.StatusUnknown,
		complete:   true,
	}
	path, err := reader.sessionPath(sessionID)
	if err != nil {
		return transcript, err
	}
	detail, _, err := readSessionRecords(path, sessionID, nil, func(record nativeEvent) {
		transcript.observe(sessionID, record)
	})
	if err != nil {
		return transcript, err
	}
	transcript.session = detail.Session
	if detail.Provenance.State != model.RecordComplete {
		transcript.complete = false
	}
	return transcript, nil
}

func (transcript *collaborationTranscript) observe(sessionID string, record nativeEvent) {
	timestamp := time.UnixMilli(record.Time).UTC()
	switch record.Type {
	case "subagent/catalog":
		var entry collaborationCatalogEntry
		if json.Unmarshal(record.Data, &entry) != nil || entry.ChildID == "" || strings.TrimSpace(entry.ChildID) != entry.ChildID {
			transcript.complete = false
			return
		}
		transcript.catalog[entry.ChildID] = entry
	case "user/message":
		var notice struct {
			Source struct {
				Kind            string `json:"kind"`
				SenderSessionID string `json:"senderSessionId"`
			} `json:"source"`
		}
		if json.Unmarshal(record.Data, &notice) == nil && notice.Source.Kind == "subagent-settled" && notice.Source.SenderSessionID != "" {
			transcript.settled[notice.Source.SenderSessionID] = collaboration.SourceAnchor{
				AgentType: agentType, SessionID: sessionID, EventID: fmt.Sprintf("%s:%d", sessionID, record.Seq),
				Timestamp: &timestamp, Precision: collaboration.ExactFact(),
			}
		}
	case "turn/start", "turn/end":
		var turn struct {
			Turn   int `json:"turn"`
			Reason struct {
				Kind string `json:"kind"`
			} `json:"reason"`
		}
		if json.Unmarshal(record.Data, &turn) != nil || turn.Turn < 1 {
			transcript.complete = false
			return
		}
		if record.Type == "turn/start" {
			transcript.active[turn.Turn] = timestamp
			transcript.lastStatus = collaboration.StatusUnknown
			return
		}
		startedAt, found := transcript.active[turn.Turn]
		if !found || timestamp.Before(startedAt) {
			transcript.complete = false
			return
		}
		delete(transcript.active, turn.Turn)
		if timestamp.After(startedAt) {
			transcript.activity = append(transcript.activity, collaboration.ActivitySpan{StartedAt: startedAt, EndedAt: timestamp})
		}
		switch turn.Reason.Kind {
		case "completed":
			transcript.lastStatus = collaboration.StatusCompleted
		case "error":
			transcript.lastStatus = collaboration.StatusFailed
		case "cancelled", "aborted":
			transcript.lastStatus = collaboration.StatusCancelled
		default:
			transcript.lastStatus = collaboration.StatusUnknown
		}
	}
}

// collaborationChildren uses the reader's current discovery snapshot. Each
// independent transcript has its own selected generation and file revision.
func (reader *Reader) collaborationChildren() map[string][]metadataSnapshot {
	childrenByParent := make(map[string][]metadataSnapshot)
	reader.mutex.RLock()
	defer reader.mutex.RUnlock()
	for _, snapshot := range reader.metadata {
		parentID := snapshot.session.ParentSessionID
		if parentID != "" {
			childrenByParent[parentID] = append(childrenByParent[parentID], snapshot)
		}
	}
	return childrenByParent
}

func (reader *Reader) CollaborationSourceRevision(root model.Session) int64 {
	digest := fnv.New64a()
	fmt.Fprintln(digest, model.SessionRevision(root))
	childrenByParent := reader.collaborationChildren()
	seen := map[string]bool{root.ID: true}
	var writeDescendants func(string)
	writeDescendants = func(parentID string) {
		children := childrenByParent[parentID]
		sort.Slice(children, func(i, j int) bool { return children[i].session.ID < children[j].session.ID })
		for _, child := range children {
			childID := child.session.ID
			if seen[childID] {
				continue
			}
			seen[childID] = true
			fmt.Fprintf(digest, "%q %q %q %d %d %d\n", parentID, childID, child.path,
				child.size, child.modified.UnixNano(), model.SessionRevision(child.session))
			writeDescendants(childID)
		}
	}
	writeDescendants(root.ID)
	return int64(digest.Sum64() & (1<<63 - 1))
}

// ReadCollaboration builds one lane per native child session. A continuable
// child can run several turns, so each closed turn supplies its own activity
// span and leaves idle time between turns out of the parallel count.
func (reader *Reader) ReadCollaboration(ctx context.Context, root model.Session) (collaboration.CollaborationGraph, error) {
	var graph collaboration.CollaborationGraph
	if root.AgentType != agentType || root.IsSubagent || root.ParentSessionID != "" {
		return graph, fmt.Errorf("dsh collaboration requires a root session")
	}
	if err := ctx.Err(); err != nil {
		return graph, err
	}
	rootTranscript, err := reader.readCollaborationTranscript(root.ID)
	if err != nil {
		return graph, err
	}
	rootID := collaboration.RootInvocationID(agentType, root.ID)
	graph = collaboration.CollaborationGraph{
		RootAgentType: agentType, RootSessionID: root.ID, Revision: reader.CollaborationSourceRevision(root),
		Completeness: collaboration.ExactFact(),
		Invocations:  []collaboration.AgentInvocation{rootTranscript.invocation(rootID, rootTranscript.session.Name, collaboration.IdentityRootSession)},
	}

	childrenByParent := reader.collaborationChildren()

	seen := map[string]bool{root.ID: true}
	var addChildren func(collaborationTranscript, string) error
	addChildren = func(parent collaborationTranscript, parentID string) error {
		if !parent.complete {
			graph.Completeness = missingCollaborationFact
		}
		childIDs := make([]string, 0, len(parent.catalog)+len(childrenByParent[parent.session.ID]))
		for childID := range parent.catalog {
			childIDs = append(childIDs, childID)
		}
		for _, snapshot := range childrenByParent[parent.session.ID] {
			childID := snapshot.session.ID
			if _, recorded := parent.catalog[childID]; !recorded {
				childIDs = append(childIDs, childID)
			}
		}
		sort.Strings(childIDs)
		for _, childID := range childIDs {
			if err := ctx.Err(); err != nil {
				return err
			}
			if seen[childID] {
				graph.Completeness = missingCollaborationFact
				continue
			}
			child, readErr := reader.readCollaborationTranscript(childID)
			entry, catalogued := parent.catalog[childID]
			childInvocationID := collaboration.ChildInvocationID(agentType, root.ID, childID)
			invocation := collaboration.AgentInvocation{
				ID: childInvocationID, AgentType: agentType, DisplayName: entry.Label,
				Status: collaboration.StatusUnknown, TimePrecision: missingCollaborationFact,
				ContentPrecision: missingCollaborationFact,
				SourceIdentity:   collaboration.SourceIdentity{Kind: collaboration.IdentitySessionID, NativeID: childID},
			}
			hasBacking := readErr == nil && child.session.ParentSessionID == parent.session.ID
			if hasBacking {
				invocation = child.invocation(childInvocationID, entry.Label, collaboration.IdentitySessionID)
			} else {
				graph.Completeness = missingCollaborationFact
			}
			if invocation.DisplayName == "" {
				invocation.DisplayName = childID
			}
			delegation := collaboration.Delegation{
				ID:                 collaboration.DelegationIDFor(parentID, childInvocationID),
				ParentInvocationID: parentID, ChildInvocationID: childInvocationID,
				TaskSummary: entry.Label, ExecutionMode: collaboration.ExecutionUnknown,
				Evidence: collaboration.DelegationEvidence{
					Trigger: missingCollaborationFact, Timing: invocation.TimePrecision,
					Task: missingCollaborationFact, Result: missingCollaborationFact,
				},
			}
			if catalogued && entry.Label != "" {
				delegation.Evidence.Task = collaboration.ExactFact()
			}
			// Continuable children are explicitly background agents in dsh.
			if entry.Mode == "continuable" {
				delegation.ExecutionMode = collaboration.ExecutionBackground
			}
			if result, found := parent.settled[childID]; found {
				delegation.Result = &result
				delegation.Evidence.Result = collaboration.ExactFact()
			}
			seen[childID] = true
			graph.Invocations = append(graph.Invocations, invocation)
			graph.Delegations = append(graph.Delegations, delegation)
			if hasBacking {
				if err := addChildren(child, childInvocationID); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := addChildren(rootTranscript, rootID); err != nil {
		return collaboration.CollaborationGraph{}, err
	}
	return graph, nil
}

func (transcript collaborationTranscript) invocation(id, label, identityKind string) collaboration.AgentInvocation {
	if label == "" {
		label = transcript.session.Name
	}
	if label == "" {
		label = transcript.session.ID
	}
	invocation := collaboration.AgentInvocation{
		ID: id, DisplayName: label, AgentType: agentType, ModelName: transcript.session.ModelName,
		Status: collaboration.StatusUnknown, StartedAt: &transcript.session.CreatedAt,
		TimePrecision: missingCollaborationFact, ContentPrecision: collaboration.ExactFact(),
		BackingSession: &collaboration.BackingSessionRef{AgentType: agentType, SessionID: transcript.session.ID},
		SourceIdentity: collaboration.SourceIdentity{Kind: identityKind, NativeID: transcript.session.ID},
	}
	if !transcript.complete {
		invocation.ContentPrecision = collaboration.FactEvidence{
			State: collaboration.EvidenceEstimated, ReasonCode: collaboration.ReasonSourceNotRecorded,
		}
	}
	if identityKind != collaboration.IdentityRootSession {
		invocation.Status = transcript.lastStatus
		invocation.Activity = transcript.activity
		if len(transcript.active) == 0 && len(transcript.activity) > 0 {
			invocation.EndedAt = &transcript.activity[len(transcript.activity)-1].EndedAt
			invocation.TimePrecision = collaboration.ExactFact()
		}
	}
	return invocation
}
