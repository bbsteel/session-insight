package claude

import (
	"os"
	"path/filepath"
	"testing"
)

// These tests use small synthetic JSONL fixtures written to a temp dir, so
// they run in CI / on any machine — unlike adapter_test.go and
// adapter_subagent_test.go, which read real local session files and skip
// themselves when those aren't present.

func writeTempJSONL(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestDropEmptyTrailingTurn(t *testing.T) {
	// A trailing empty user message (no text, no embedded bash commands, no
	// assistant response) — observed in real session data via the Phase 2
	// end-to-end render endpoint, which rendered it as a bare "> " line
	// with nothing else. The old EventVM-based parser filtered this via
	// filterEmptyTurns; the RenderEvent pipeline needs the same behavior.
	fixture := `{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"real question"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","model":"claude-x","content":[{"type":"text","text":"real answer"}]}}
{"type":"user","uuid":"u2","timestamp":"2026-01-01T00:00:02Z","message":{"role":"user","content":""}}
`
	path := writeTempJSONL(t, "empty_trailing_turn.jsonl", fixture)
	events, _, err := ParseClaudeRenderEvents(path, 0, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, e := range events {
		if e.TurnIndex == 1 {
			t.Errorf("expected the empty trailing turn (index 1) to be dropped, found event: %+v", e)
		}
	}

	var sawRealTurn bool
	for _, e := range events {
		if e.Type == "UserPrompt" && e.Text == "real question" {
			sawRealTurn = true
		}
	}
	if !sawRealTurn {
		t.Error("expected the real turn to survive filtering")
	}
}

func TestEmptyToolCallTurnIsNotDropped(t *testing.T) {
	// A turn whose user message has no visible text but does carry an
	// embedded bash command should NOT be dropped — it has real content,
	// just not in the UserPrompt.Text field.
	fixture := `{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"<bash-input>ls -la</bash-input>"}}
`
	path := writeTempJSONL(t, "bash_only_turn.jsonl", fixture)
	events, _, err := ParseClaudeRenderEvents(path, 0, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var sawToolInvocation bool
	for _, e := range events {
		if e.Type == "ToolInvocation" && e.ToolName == "Bash" {
			sawToolInvocation = true
		}
	}
	if !sawToolInvocation {
		t.Error("expected the embedded bash command's turn to survive filtering despite empty UserPrompt.Text")
	}
}

func TestEmptyFileReturnsNoEventsNoError(t *testing.T) {
	path := writeTempJSONL(t, "empty.jsonl", "")
	events, modelName, err := ParseClaudeRenderEvents(path, 0, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected no events, got %d", len(events))
	}
	if modelName != "" {
		t.Errorf("expected empty model name, got %q", modelName)
	}
}

func TestSingleThinkingBlockPreservesText(t *testing.T) {
	// One user turn, one assistant message with exactly one thinking block
	// and no further chunks. Before the fix, ThinkingStart never carried
	// Text, so a single-block thinking stream's content was silently
	// dropped entirely (the common case — Claude usually emits each
	// thinking burst as one block, not pre-split into multiple chunks).
	fixture := `{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"hello"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","model":"claude-x","content":[{"type":"thinking","thinking":"pondering the question"}]}}
`
	path := writeTempJSONL(t, "thinking.jsonl", fixture)
	events, _, err := ParseClaudeRenderEvents(path, 0, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var sawText bool
	for _, e := range events {
		if e.Type == "ThinkingStart" && e.Text == "pondering the question" {
			sawText = true
		}
	}
	if !sawText {
		t.Error("expected the single thinking block's text to be preserved on the ThinkingStart event, but it wasn't found anywhere in the emitted stream")
	}
}

func TestThinkingThenTextEmitsThinkingEndBeforeTextChunk(t *testing.T) {
	fixture := `{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"hello"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","model":"claude-x","content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"here is my answer"}]}}
`
	path := writeTempJSONL(t, "thinking_then_text.jsonl", fixture)
	events, _, err := ParseClaudeRenderEvents(path, 0, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	endIdx, textIdx := -1, -1
	for i, e := range events {
		if e.Type == "ThinkingEnd" {
			endIdx = i
		}
		if e.Type == "TextChunk" && textIdx == -1 {
			textIdx = i
		}
	}
	if endIdx == -1 || textIdx == -1 {
		t.Fatalf("expected both ThinkingEnd and TextChunk events, got endIdx=%d textIdx=%d", endIdx, textIdx)
	}
	if endIdx > textIdx {
		t.Errorf("expected ThinkingEnd (index %d) to come before TextChunk (index %d)", endIdx, textIdx)
	}
}

func TestToolUseWithoutIDStillLinksResult(t *testing.T) {
	// Real Claude tool_use blocks always carry an id, but the adapter must
	// not silently break the ToolInvocation<->ToolResult link if one ever
	// arrives without one (e.g. a future event shape, or a hand-edited
	// transcript).
	fixture := `{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"run a tool"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","model":"claude-x","content":[{"type":"tool_use","name":"Bash","id":"","input":{"command":"ls"}}]}}
{"type":"user","uuid":"u2","timestamp":"2026-01-01T00:00:02Z","toolUseResult":{"stdout":"file1\nfile2","is_error":false}}
`
	path := writeTempJSONL(t, "no_id_tool.jsonl", fixture)
	events, _, err := ParseClaudeRenderEvents(path, 0, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var invID string
	for _, e := range events {
		if e.Type == "ToolInvocation" && e.ToolName == "Bash" {
			invID = e.EventID
		}
	}
	if invID == "" {
		t.Fatal("expected a ToolInvocation event with a non-empty fallback EventID")
	}

	var linked bool
	for _, e := range events {
		if e.Type == "ToolResult" && e.ParentEventID == invID {
			linked = true
		}
	}
	if !linked {
		t.Errorf("expected ToolResult.ParentEventID to resolve to the fallback invocation EventID %q, but no ToolResult linked to it", invID)
	}
}

func TestCompactBoundaryEmitsAbstractCompactionRenderEvent(t *testing.T) {
	fixture := `{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"before compact"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","model":"claude-x","content":[{"type":"text","text":"ok"}]}}
{"type":"system","subtype":"compact_boundary","uuid":"c1","timestamp":"2026-01-01T00:00:02Z"}
{"type":"user","uuid":"u2","timestamp":"2026-01-01T00:00:03Z","message":{"role":"user","content":"after compact"}}
`
	path := writeTempJSONL(t, "compact_boundary.jsonl", fixture)
	events, _, err := ParseClaudeRenderEvents(path, 0, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var sawCompaction bool
	for _, e := range events {
		if e.Type == "CompactionBoundary" && e.TurnIndex == 0 {
			sawCompaction = true
		}
	}
	if !sawCompaction {
		t.Error("expected system compact_boundary to emit an abstract CompactionBoundary event on the preceding turn")
	}
}

func TestCompactBoundaryMarksTurnAnomaly(t *testing.T) {
	fixture := `{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"before compact"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","model":"claude-x","content":[{"type":"text","text":"ok"}]}}
{"type":"system","subtype":"compact_boundary","uuid":"c1","timestamp":"2026-01-01T00:00:02Z"}
{"type":"user","uuid":"u2","timestamp":"2026-01-01T00:00:03Z","message":{"role":"user","content":"after compact"}}
`
	path := writeTempJSONL(t, "compact_turn.jsonl", fixture)
	turns, _, err := parseClaudeEvents(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("expected two turns, got %d", len(turns))
	}
	if len(turns[0].Anomalies) != 1 || turns[0].Anomalies[0] != "compaction" {
		t.Fatalf("expected preceding turn to be marked with compaction, got %+v", turns[0].Anomalies)
	}
}

func TestFallbackEventIDsDoNotCollideAcrossFiles(t *testing.T) {
	// Two different files, each with a line missing "uuid", parsed
	// independently (mirroring how ParseClaudeRenderEventsWithSubagents
	// calls this function once per file). Their fallback-generated EventIDs
	// must not collide once merged into one stream.
	fixtureA := `{"type":"user","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"a"}}
`
	fixtureB := `{"type":"user","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"b"}}
`
	pathA := writeTempJSONL(t, "fileA.jsonl", fixtureA)
	pathB := writeTempJSONL(t, "fileB.jsonl", fixtureB)

	eventsA, _, err := ParseClaudeRenderEvents(pathA, 0, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	eventsB, _, err := ParseClaudeRenderEvents(pathB, 0, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	seen := make(map[string]bool)
	for _, e := range eventsA {
		seen[e.EventID] = true
	}
	for _, e := range eventsB {
		if seen[e.EventID] {
			t.Errorf("EventID %q collided between fileA and fileB parses", e.EventID)
		}
	}
}

func TestSubagentStitchingRejectsUnsafeAgentID(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.jsonl")
	subagentsDir := filepath.Join(dir, "main", "subagents")
	if err := os.MkdirAll(subagentsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// A path-traversal-shaped agent_id. If unguarded, filepath.Join could
	// be made to resolve outside subagentsDir.
	maliciousID := "../../../../etc/passwd"
	fixture := `{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"go"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","model":"claude-x","content":[{"type":"tool_use","name":"Agent","id":"call_1","input":{}}]}}
{"type":"user","uuid":"u2","timestamp":"2026-01-01T00:00:02Z","toolUseResult":{"agentId":"` + maliciousID + `"}}
`
	if err := os.WriteFile(mainPath, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	events, _, err := ParseClaudeRenderEventsWithSubagents(mainPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var sawRejection bool
	for _, e := range events {
		if e.Type == "AgentSpecific" && e.Subtype == "subagent_load_error" {
			if reason, _ := e.Payload["reason"].(string); reason == "unsafe agent_id" {
				sawRejection = true
			}
		}
		// Defensive: make sure no event's payload echoes back content that
		// would suggest a file outside subagentsDir was actually read.
		if e.Stdout != "" && e.Stdout != "file1\nfile2" {
			t.Errorf("unexpected stdout content present, possible path traversal read: %q", e.Stdout)
		}
	}
	if !sawRejection {
		t.Error("expected a subagent_load_error AgentSpecific event flagging the unsafe agent_id, found none")
	}
}

func TestSubagentParseFailureEmitsMarkerEvent(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.jsonl")
	subagentsDir := filepath.Join(dir, "main", "subagents")
	if err := os.MkdirAll(subagentsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	agentID := "abc123"
	fixture := `{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"go"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","model":"claude-x","content":[{"type":"tool_use","name":"Agent","id":"call_1","input":{}}]}}
{"type":"user","uuid":"u2","timestamp":"2026-01-01T00:00:02Z","toolUseResult":{"agentId":"` + agentID + `"}}
`
	if err := os.WriteFile(mainPath, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// The subagent file exists (so the "not found -> just skip" path isn't
	// taken) but contains content that os.Open will still succeed on; we
	// can't easily force a scanner.Err() without exceeding the buffer, so
	// instead verify the "file doesn't exist" path - already covered by
	// default behavior - and separately confirm a corrupt/oversized line
	// surfaces via the same marker mechanism by exceeding the scanner's
	// token buffer.
	subPath := filepath.Join(subagentsDir, "agent-"+agentID+".jsonl")
	oversized := make([]byte, 11*1024*1024) // exceeds the 10MB scanner.Buffer max
	for i := range oversized {
		oversized[i] = 'a'
	}
	content := `{"type":"user","uuid":"su1","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"` + string(oversized) + `"}}` + "\n"
	if err := os.WriteFile(subPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write subagent fixture: %v", err)
	}

	events, _, err := ParseClaudeRenderEventsWithSubagents(mainPath)
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}

	var sawError bool
	for _, e := range events {
		if e.Type == "AgentSpecific" && e.Subtype == "subagent_load_error" {
			if reason, _ := e.Payload["reason"].(string); reason != "" && reason != "unsafe agent_id" {
				sawError = true
			}
		}
	}
	if !sawError {
		t.Error("expected a subagent_load_error marker event when the subagent file fails to scan, found none")
	}
}
