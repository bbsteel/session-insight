package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// fakeReader is a minimal BaseSessionReader over canned data.
type fakeReader struct {
	agent    string
	sessions []model.Session
	events   map[string][]model.RenderEvent
}

func (f *fakeReader) AgentType() string                      { return f.agent }
func (f *fakeReader) DisplayName() string                    { return f.agent }
func (f *fakeReader) ListSessions() ([]model.Session, error) { return f.sessions, nil }
func (f *fakeReader) GetSession(id string) (*model.SessionDetail, error) {
	for _, s := range f.sessions {
		if s.ID == id {
			return &model.SessionDetail{Session: s}, nil
		}
	}
	return nil, fmt.Errorf("session %q not found", id)
}
func (f *fakeReader) RenderANSI(id string, cols int) (string, error) { return "", nil }
func (f *fakeReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	return f.events[id], nil
}

func candidateFor(report *CandidateReport, itemID string) *Candidate {
	for _, c := range report.Candidates {
		if c.ItemID == itemID {
			return c
		}
	}
	return nil
}

func TestDiscoverCandidatesStructuredFacts(t *testing.T) {
	now := time.Now()
	session := model.Session{
		ID: "s1", AgentType: "claude", CWD: "/tmp/project", ResumeID: "resume-1",
		TurnCount: 3, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	events := []model.RenderEvent{
		{EventID: "u1", Type: "UserPrompt", Text: "line one\nline two", TurnIndex: 1},
		{EventID: "a1", Type: "TextChunk", Text: "here is code:\n```go\nx\n```", TurnIndex: 1},
		{EventID: "th1", Type: "ThinkingStart", TurnIndex: 1},
		{EventID: "ti1", Type: "ToolInvocation", ToolName: "Bash", ToolCallID: "c1", TurnIndex: 1},
		{EventID: "tr1", Type: "ToolResult", ToolCallID: "c1", ParentEventID: "ti1", ExitCode: 0, TurnIndex: 1},
		{EventID: "ti2", Type: "ToolInvocation", ToolName: "Bash", ToolCallID: "c2", TurnIndex: 1},
		{EventID: "tr2", Type: "ToolResult", ToolCallID: "c2", ParentEventID: "ti2", ExitCode: 1, Stderr: "boom", TurnIndex: 1},
		{EventID: "ti3", Type: "ToolInvocation", ToolName: "Bash", ToolCallID: "c3", TurnIndex: 1},
		{EventID: "tr3", Type: "ToolResult", ToolCallID: "c3", ParentEventID: "ti3", TimedOut: true, TurnIndex: 1},
		{EventID: "ti4", Type: "ToolInvocation", ToolName: "Bash", ToolCallID: "c4", TurnIndex: 1},
		{EventID: "tr4", Type: "ToolResult", ToolCallID: "c4", ParentEventID: "ti4", Rejected: true, TurnIndex: 1},
		{EventID: "ti5", Type: "ToolInvocation", ToolName: "Edit", ToolCallID: "c5",
			ToolInput: map[string]any{"file_path": "a.go", "old_string": "x", "new_string": "y"}, TurnIndex: 2},
		{EventID: "tr5", Type: "ToolResult", ToolCallID: "c5", ParentEventID: "ti5", Truncated: true, TurnIndex: 2},
		{EventID: "sub1", Type: "ToolInvocation", ToolName: "Task", Depth: 1, InvocationID: "inv-2", TurnIndex: 2},
		{EventID: "cb1", Type: "CompactionBoundary", TurnIndex: 2},
		{EventID: "int1", Type: "TurnBoundary", Subtype: "interrupted", TurnIndex: 3},
	}
	r := &fakeReader{agent: "claude", sessions: []model.Session{session}, events: map[string][]model.RenderEvent{"s1": events}}

	report := DiscoverCandidates(r, 10)
	wantExact := []string{
		"01-session-overview", "02-user-prompt", "03-assistant-response", "04-thinking",
		"05-tool-invocation", "07-tool-success", "08-tool-failure", "09-tool-timeout",
		"10-tool-rejected", "11-file-change", "12-subagent", "13-context-boundary",
		"15-long-output", "18-session-interrupted", "20-tool-group", "21-nested-fold",
	}
	for _, itemID := range wantExact {
		c := candidateFor(report, itemID)
		if c == nil {
			t.Errorf("missing candidate for %s", itemID)
			continue
		}
		if c.Precision != PrecisionExact {
			t.Errorf("%s: precision = %s, want exact", itemID, c.Precision)
		}
		if c.SessionID != "s1" {
			t.Errorf("%s: session = %s, want s1", itemID, c.SessionID)
		}
	}

	// Resume command comes from the adapter declaration, never invented.
	overview := candidateFor(report, "01-session-overview")
	if overview == nil || !strings.Contains(overview.ResumeCommand, "--resume resume-1") {
		t.Errorf("resume command = %+v, want native --resume form", overview)
	}
	if !strings.HasPrefix(overview.ResumeCommand, "cd /tmp/project && ") {
		t.Errorf("resume command should include the session cwd, got %q", overview.ResumeCommand)
	}

	// Items without structured evidence stay candidate-free (allowed gaps).
	for _, itemID := range []string{"00-version", "14-permission-request", "19-agent-specific"} {
		if c := candidateFor(report, itemID); c != nil {
			t.Errorf("%s must not have a candidate here, got %+v", itemID, c)
		}
	}
}

func TestDiscoverCandidatesNoGuessing(t *testing.T) {
	// A failure-looking result without structured timeout facts must not
	// produce a timeout candidate.
	now := time.Now()
	session := model.Session{ID: "s2", AgentType: "claude", TurnCount: 1, UpdatedAt: now.Add(-time.Hour)}
	events := []model.RenderEvent{
		{EventID: "ti1", Type: "ToolInvocation", ToolName: "Bash", ToolCallID: "c1", TurnIndex: 1},
		{EventID: "tr1", Type: "ToolResult", ToolCallID: "c1", ParentEventID: "ti1",
			ExitCode: 124, Stderr: "command timed out after 10s", TurnIndex: 1},
	}
	r := &fakeReader{agent: "claude", sessions: []model.Session{session}, events: map[string][]model.RenderEvent{"s2": events}}
	report := DiscoverCandidates(r, 10)
	if c := candidateFor(report, "09-tool-timeout"); c != nil {
		t.Fatalf("timeout must not be guessed from error text/exit code, got %+v", c)
	}
	if c := candidateFor(report, "08-tool-failure"); c == nil {
		t.Fatal("structured failure (exit code) should produce a failure candidate")
	}
	// A finished session offers a clearly-marked low-confidence completion suggestion.
	c := candidateFor(report, "17-session-completed")
	if c == nil || c.Precision != PrecisionLowConfidence {
		t.Fatalf("completion suggestion = %+v, want low_confidence", c)
	}
}

// TestStderrOnlyResult pins the design rule that structured stderr counts as
// a failure fact even with a zero exit code (reference input design §7:
// failure = ExitCode != 0, ErrorKind set, or structured stderr present).
func TestStderrOnlyResultIsFailureCandidate(t *testing.T) {
	now := time.Now()
	session := model.Session{ID: "s3", AgentType: "claude", TurnCount: 1, UpdatedAt: now.Add(-time.Hour)}
	events := []model.RenderEvent{
		{EventID: "ti1", Type: "ToolInvocation", ToolName: "Bash", ToolCallID: "c1", TurnIndex: 1},
		{EventID: "tr1", Type: "ToolResult", ToolCallID: "c1", ParentEventID: "ti1",
			ExitCode: 0, Stderr: "warning: deprecated flag", TurnIndex: 1},
	}
	r := &fakeReader{agent: "claude", sessions: []model.Session{session}, events: map[string][]model.RenderEvent{"s3": events}}
	report := DiscoverCandidates(r, 10)
	if c := candidateFor(report, "08-tool-failure"); c == nil {
		t.Fatal("structured stderr with exit 0 must still yield a failure candidate (design §7)")
	}
	if c := candidateFor(report, "07-tool-success"); c != nil {
		t.Fatalf("a result with stderr must not be offered as the success scene, got %+v", c)
	}
}

func TestFirstToolGroup(t *testing.T) {
	mk := func(id string, depth int) model.RenderEvent {
		return model.RenderEvent{EventID: id, Type: "ToolInvocation", ToolName: "Bash", Depth: depth}
	}
	res := func(id string) model.RenderEvent { return model.RenderEvent{EventID: id, Type: "ToolResult"} }
	events := []model.RenderEvent{
		mk("i1", 0), res("r1"), mk("i2", 0), res("r2"), mk("i3", 0),
	}
	ev, n := firstToolGroup(events, 3)
	if ev == nil || ev.EventID != "i1" || n != 3 {
		t.Fatalf("firstToolGroup = %v,%d; want i1,3", ev, n)
	}
	// A text chunk breaks the run.
	events = append(events, model.RenderEvent{EventID: "t", Type: "TextChunk"}, mk("i4", 0), mk("i5", 0))
	if ev, _ := firstToolGroup(events, 4); ev != nil {
		t.Fatalf("run broken by text must not count, got %v", ev.EventID)
	}
}
