package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/reader/capability"
)

// Candidate discovery consumes normalized RenderEvents and session state from
// already-onboarded adapters. It never OCRs screenshots and never guesses
// rare states from error text or durations: only structured facts produce
// exact candidates; softer signals are marked low_confidence and listed
// separately.
const (
	PrecisionExact         = "exact"
	PrecisionLowConfidence = "low_confidence"

	// longOutputBytes is the stable threshold for the long-output scene.
	longOutputBytes = 4000
)

// Candidate is one suggested session + position for a checklist item.
type Candidate struct {
	ItemID        string `json:"item_id"`
	SessionID     string `json:"session_id"`
	ResumeID      string `json:"resume_id,omitempty"`
	ResumeCommand string `json:"resume_command,omitempty"`
	TurnIndex     int    `json:"turn_index"`
	EventID       string `json:"event_id,omitempty"`
	Summary       string `json:"summary"`
	Precision     string `json:"precision"`
}

// CandidateReport is the per-Agent discovery result.
type CandidateReport struct {
	Agent           string       `json:"agent"`
	ScannedSessions int          `json:"scanned_sessions"`
	Candidates      []*Candidate `json:"candidates"` // at most one per item
	// Errors notes sessions that could not be read; discovery continues.
	Errors []string `json:"errors,omitempty"`
}

// ByItem indexes candidates by checklist item ID.
func (r *CandidateReport) ByItem() map[string]*Candidate {
	out := map[string]*Candidate{}
	for _, c := range r.Candidates {
		out[c.ItemID] = c
	}
	return out
}

// DiscoverCandidates scans the most recent sessions of one Agent and returns
// the best candidate per checklist item. Read-only: it never deletes, stops,
// resumes or creates sessions.
func DiscoverCandidates(source reader.BaseSessionReader, maxSessions int) *CandidateReport {
	report := &CandidateReport{Agent: source.AgentType()}
	if maxSessions <= 0 {
		maxSessions = 30
	}
	sessions, err := source.ListSessions()
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("list sessions: %v", err))
		return report
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt) })
	if len(sessions) > maxSessions {
		sessions = sessions[:maxSessions]
	}

	best := map[string]*Candidate{}
	consider := func(c *Candidate) {
		if c == nil {
			return
		}
		prev := best[c.ItemID]
		if prev == nil || (prev.Precision == PrecisionLowConfidence && c.Precision == PrecisionExact) {
			best[c.ItemID] = c
		}
	}

	static, _ := reader.AgentDefinition(source.AgentType())

	for _, session := range sessions {
		report.ScannedSessions++
		identity, resumeCmd := resumeCommand(static, session, source.AgentType())
		base := Candidate{
			SessionID:     session.ID,
			ResumeID:      identity,
			ResumeCommand: resumeCmd,
		}

		liveness := reader.ResolveLiveness(source, session)
		livePrecision := PrecisionLowConfidence
		if liveness.State == capability.CapabilityExact {
			livePrecision = PrecisionExact
		}

		// Session-level scenes.
		if identity != "" {
			consider(withItem(base, "01-session-overview", 0, "", "representative resumable session", PrecisionExact))
		}
		if liveness.IsLive {
			consider(withItem(base, "16-live-status", 0, "", "session is live now", livePrecision))
		} else if session.TurnCount > 0 || session.MessageCount > 0 {
			consider(withItem(base, "17-session-completed", 0, "", "session appears finished (verify in the native CLI)", PrecisionLowConfidence))
		}

		events, err := source.GetRenderEvents(session.ID)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("session %.12s: %v", session.ID, err))
			continue
		}
		for _, itemID := range eventMatchedItems {
			if existing := best[itemID]; existing != nil && existing.Precision == PrecisionExact {
				continue
			}
			ev, summary, ok := matchEvents(itemID, events)
			if !ok {
				continue
			}
			consider(withItem(base, itemID, ev.TurnIndex, ev.EventID, summary, PrecisionExact))
		}
		// Tool running needs a live session with an unpaired invocation.
		if liveness.IsLive {
			if ev, ok := firstUnpairedInvocation(events); ok {
				consider(withItem(base, "06-tool-running", ev.TurnIndex, ev.EventID, "tool "+ev.ToolName+" running now", livePrecision))
			}
		}
	}

	for _, item := range checklist {
		if c, ok := best[item.ID]; ok {
			report.Candidates = append(report.Candidates, c)
		}
	}
	return report
}

// eventMatchedItems lists checklist items whose candidates come from the
// normalized event stream (as opposed to session-level state).
var eventMatchedItems = []string{
	"02-user-prompt",
	"03-assistant-response",
	"04-thinking",
	"05-tool-invocation",
	"07-tool-success",
	"08-tool-failure",
	"09-tool-timeout",
	"10-tool-rejected",
	"11-file-change",
	"12-subagent",
	"13-context-boundary",
	"14-permission-request",
	"15-long-output",
	"18-session-interrupted",
	"20-tool-group",
	"21-nested-fold",
}

func withItem(base Candidate, itemID string, turnIndex int, eventID, summary, precision string) *Candidate {
	c := base
	c.ItemID = itemID
	c.TurnIndex = turnIndex
	c.EventID = eventID
	c.Summary = summary
	c.Precision = precision
	return &c
}

// resumeCommand builds the copyable native resume command for a session from
// the adapter's declared ResumeCommand, without launching anything.
func resumeCommand(static capability.AgentCapabilities, session model.Session, fallbackAgentType string) (identity, command string) {
	if static.ResumeCommand == nil {
		return "", ""
	}
	agentType := session.AgentType
	if agentType == "" {
		agentType = fallbackAgentType
	}
	detail := &model.SessionDetail{Session: session}
	detail.AgentType = agentType
	identity = reader.ResumeCLIIdentity(detail, agentType)
	if identity == "" {
		return "", ""
	}
	args, ok := static.ResumeCommand.BuildResumeArgs(identity, session.ModelName, false)
	if !ok {
		return identity, ""
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, static.ResumeCommand.Executable)
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	cmd := strings.Join(parts, " ")
	if session.CWD != "" {
		cmd = "cd " + shellQuote(session.CWD) + " && " + cmd
	}
	return identity, cmd
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	isUnsafe := func(r rune) bool {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return false
		}
		return !strings.ContainsRune("-._/:=@+,", r)
	}
	if strings.IndexFunc(s, isUnsafe) == -1 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// matchEvents finds the best in-session event for an item. The summary
// describes the scene (tool name, marker kind), never private content beyond
// a short snippet.
func matchEvents(itemID string, events []model.RenderEvent) (*model.RenderEvent, string, bool) {
	switch itemID {
	case "02-user-prompt":
		fallback := -1
		for i := range events {
			if events[i].Type != "UserPrompt" {
				continue
			}
			if strings.Contains(events[i].Text, "\n") {
				return &events[i], "multi-line user prompt", true
			}
			if fallback < 0 {
				fallback = i
			}
		}
		if fallback >= 0 {
			return &events[fallback], "user prompt", true
		}
	case "03-assistant-response":
		fallback := -1
		for i := range events {
			if events[i].Type != "TextChunk" && events[i].Type != "AssistantMessage" {
				continue
			}
			if strings.Contains(events[i].Text, "```") {
				return &events[i], "assistant reply with code block", true
			}
			if fallback < 0 {
				fallback = i
			}
		}
		if fallback >= 0 {
			return &events[fallback], "assistant reply", true
		}
	case "04-thinking":
		for i := range events {
			if events[i].Type == "ThinkingStart" || events[i].Type == "ThinkingChunk" {
				return &events[i], "thinking block", true
			}
		}
	case "05-tool-invocation":
		paired := pairedInvocationIDs(events)
		fallback := -1
		for i := range events {
			if events[i].Type != "ToolInvocation" {
				continue
			}
			if _, ok := paired[events[i].EventID]; ok {
				return &events[i], "tool " + events[i].ToolName + " with result", true
			}
			if fallback < 0 {
				fallback = i
			}
		}
		if fallback >= 0 {
			return &events[fallback], "tool " + events[fallback].ToolName, true
		}
	case "07-tool-success":
		for i := range events {
			r := &events[i]
			if r.Type == "ToolResult" && r.ExitCode == 0 && !r.TimedOut && !r.Rejected && r.ErrorKind == "" && r.Stderr == "" {
				return r, "successful tool result", true
			}
		}
	case "08-tool-failure":
		for i := range events {
			r := &events[i]
			if r.Type == "ToolResult" && (r.ExitCode != 0 || r.ErrorKind != "" || r.Stderr != "") && !r.TimedOut && !r.Rejected {
				return r, "failed tool result (exit/error/stderr)", true
			}
		}
	case "09-tool-timeout":
		for i := range events {
			if events[i].Type == "ToolResult" && events[i].TimedOut {
				return &events[i], "timed-out tool result", true
			}
		}
	case "10-tool-rejected":
		for i := range events {
			if events[i].Type == "ToolResult" && events[i].Rejected {
				return &events[i], "rejected tool result", true
			}
		}
	case "11-file-change":
		for i := range events {
			if events[i].Type == "ToolInvocation" && len(model.ExtractEditCalls(events[i])) > 0 {
				return &events[i], "file edit via " + events[i].ToolName, true
			}
		}
	case "12-subagent":
		for i := range events {
			e := &events[i]
			if e.InvocationID != "" || strings.Contains(strings.ToLower(e.Subtype), "subagent") {
				return e, "subagent activity", true
			}
		}
		for i := range events {
			if events[i].Depth > 0 {
				return &events[i], "nested (child-depth) activity", true
			}
		}
	case "13-context-boundary":
		for i := range events {
			e := &events[i]
			switch e.Type {
			case "CompactionBoundary":
				return e, "compaction boundary", true
			case "RollbackStart", "RollbackEnd":
				return e, "rollback boundary", true
			case "TurnBoundary":
				if rolledBack, _ := e.Metadata["rolled_back"].(bool); rolledBack {
					return e, "rolled-back turn", true
				}
			}
		}
	case "14-permission-request":
		for i := range events {
			if strings.Contains(strings.ToLower(events[i].Subtype), "permission") {
				return &events[i], "permission request (" + events[i].Subtype + ")", true
			}
		}
	case "15-long-output":
		for i := range events {
			r := &events[i]
			if r.Type == "ToolResult" && r.Truncated {
				return r, "explicitly truncated output", true
			}
		}
		for i := range events {
			r := &events[i]
			if r.Type == "ToolResult" && len(r.Stdout)+len(r.Stderr) > longOutputBytes {
				return r, fmt.Sprintf("output above %d bytes", longOutputBytes), true
			}
		}
	case "18-session-interrupted":
		for i := range events {
			if strings.EqualFold(events[i].Subtype, "interrupted") {
				return &events[i], "interrupted marker", true
			}
		}
	case "20-tool-group":
		if ev, n := firstToolGroup(events, 3); ev != nil {
			return ev, fmt.Sprintf("%d consecutive tools", n), true
		}
	case "21-nested-fold":
		hasChild := false
		for i := range events {
			if events[i].Depth > 0 || events[i].InvocationID != "" {
				hasChild = true
				break
			}
		}
		if hasChild {
			for i := range events {
				if events[i].Depth > 0 && events[i].Type == "ToolInvocation" {
					return &events[i], "tool nested inside a subagent block", true
				}
			}
		}
	}
	return nil, "", false
}

// pairedInvocationIDs returns the EventIDs of ToolInvocations that have a
// matching ToolResult (via ParentEventID or ToolCallID).
func pairedInvocationIDs(events []model.RenderEvent) map[string]struct{} {
	invocationCallIDs := map[string]string{} // ToolCallID -> EventID
	for i := range events {
		if events[i].Type == "ToolInvocation" {
			if events[i].ToolCallID != "" {
				invocationCallIDs[events[i].ToolCallID] = events[i].EventID
			}
		}
	}
	paired := map[string]struct{}{}
	for i := range events {
		r := &events[i]
		if r.Type != "ToolResult" {
			continue
		}
		if r.ParentEventID != "" {
			paired[r.ParentEventID] = struct{}{}
		}
		if id, ok := invocationCallIDs[r.ToolCallID]; r.ToolCallID != "" && ok {
			paired[id] = struct{}{}
		}
	}
	return paired
}

// firstUnpairedInvocation finds a ToolInvocation with no ToolResult yet.
func firstUnpairedInvocation(events []model.RenderEvent) (*model.RenderEvent, bool) {
	paired := pairedInvocationIDs(events)
	for i := range events {
		if events[i].Type != "ToolInvocation" {
			continue
		}
		if _, ok := paired[events[i].EventID]; !ok {
			return &events[i], true
		}
	}
	return nil, false
}

// firstToolGroup finds the first run of at least min consecutive tool
// invocations at the same depth (results between invocations are allowed).
func firstToolGroup(events []model.RenderEvent, min int) (*model.RenderEvent, int) {
	var runStart *model.RenderEvent
	runCount, runDepth := 0, -1
	flush := func() (*model.RenderEvent, int, bool) {
		if runCount >= min {
			return runStart, runCount, true
		}
		return nil, 0, false
	}
	for i := range events {
		e := &events[i]
		switch e.Type {
		case "ToolInvocation":
			if runCount > 0 && e.Depth != runDepth {
				if ev, n, ok := flush(); ok {
					return ev, n
				}
				runCount, runStart = 0, nil
			}
			if runCount == 0 {
				runStart = e
				runDepth = e.Depth
			}
			runCount++
		case "ToolResult", "ThinkingChunk":
			// neutral: does not break a run
		default:
			if ev, n, ok := flush(); ok {
				return ev, n
			}
			runCount, runStart, runDepth = 0, nil, -1
		}
	}
	if ev, n, ok := flush(); ok {
		return ev, n
	}
	return nil, 0
}
