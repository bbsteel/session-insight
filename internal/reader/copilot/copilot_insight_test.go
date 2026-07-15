package copilot

import (
	"os"
	"path/filepath"
	"testing"
)

// writeEvents writes JSONL lines to a temp events.jsonl and returns its path.
func writeEvents(t *testing.T, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "events.jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestInsightEvidenceAttribution exercises the temporal-nesting response
// attribution and overlap detection on a small synthetic session: subagent B
// runs a nested subagent C, so a response inside C's window must count for C
// (innermost), not B, and B/C must be flagged as overlapping.
func TestInsightEvidenceAttribution(t *testing.T) {
	lines := []string{
		`{"type":"user.message","timestamp":"2026-01-01T00:00:00Z","data":{"content":"go"}}`,
		`{"type":"tool.execution_start","timestamp":"2026-01-01T00:00:01Z","data":{"toolName":"task","toolCallId":"A","arguments":{"description":"Implement","name":"impl","model":"m1","mode":"sync","prompt":"PROMPT-A"}}}`,
		`{"type":"subagent.started","timestamp":"2026-01-01T00:01:00Z","data":{"toolCallId":"A","agentDisplayName":"Impl Agent"}}`,
		`{"type":"assistant.message","timestamp":"2026-01-01T00:01:01Z","data":{"outputTokens":10}}`,
		`{"type":"assistant.message","timestamp":"2026-01-01T00:01:02Z","data":{"outputTokens":20}}`,
		`{"type":"subagent.completed","timestamp":"2026-01-01T00:01:10Z","data":{"toolCallId":"A"}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-01-01T00:01:10Z","data":{"toolCallId":"A","toolTelemetry":{"metrics":{"numberOfToolCallsMadeByAgent":22}}}}`,
		`{"type":"tool.execution_start","timestamp":"2026-01-01T00:02:00Z","data":{"toolName":"task","toolCallId":"B","arguments":{"description":"Review","mode":"async","prompt":"PB"}}}`,
		`{"type":"subagent.started","timestamp":"2026-01-01T00:03:00Z","data":{"toolCallId":"B","agentDisplayName":"Review Agent"}}`,
		`{"type":"assistant.message","timestamp":"2026-01-01T00:03:01Z","data":{"outputTokens":5}}`,
		`{"type":"tool.execution_start","timestamp":"2026-01-01T00:03:01Z","data":{"toolName":"task","toolCallId":"C","arguments":{"description":"Nested","mode":"sync","prompt":"PC"}}}`,
		`{"type":"subagent.started","timestamp":"2026-01-01T00:03:02Z","data":{"toolCallId":"C","agentDisplayName":"Nested Agent"}}`,
		`{"type":"assistant.message","timestamp":"2026-01-01T00:03:03Z","data":{"outputTokens":7}}`,
		`{"type":"subagent.completed","timestamp":"2026-01-01T00:03:04Z","data":{"toolCallId":"C"}}`,
		`{"type":"subagent.completed","timestamp":"2026-01-01T00:03:10Z","data":{"toolCallId":"B"}}`,
		`{"type":"assistant.message","timestamp":"2026-01-01T00:05:00Z","data":{"outputTokens":100}}`,
	}
	p := writeEvents(t, lines)
	ev, err := parseInsightEvidence(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.Subagents) != 3 {
		t.Fatalf("want 3 subagents, got %d", len(ev.Subagents))
	}
	by := map[string]int{}
	tok := map[string]int64{}
	overlap := map[string]bool{}
	for _, s := range ev.Subagents {
		by[s.ToolCallID] = s.RequestCount
		tok[s.ToolCallID] = s.OutputTokens
		overlap[s.ToolCallID] = s.OverlapsOther
	}
	if by["A"] != 2 || tok["A"] != 30 {
		t.Errorf("A: want 2 reqs / 30 tokens, got %d / %d", by["A"], tok["A"])
	}
	// t=03:03 is inside both B and C; the innermost (C, later start) claims it.
	if by["B"] != 1 || by["C"] != 1 {
		t.Errorf("nested attribution wrong: B=%d C=%d (want 1/1)", by["B"], by["C"])
	}
	if !overlap["B"] || !overlap["C"] {
		t.Errorf("B and C windows overlap and must be flagged: B=%v C=%v", overlap["B"], overlap["C"])
	}
	if overlap["A"] {
		t.Error("A does not overlap and must not be flagged")
	}
	// Delegation facts preserved from task args.
	for _, s := range ev.Subagents {
		if s.ToolCallID == "A" {
			if s.Description != "Implement" || s.Model != "m1" || s.Mode != "sync" || s.PromptChars != 8 {
				t.Errorf("A delegation facts wrong: %+v", s)
			}
		}
	}
}

// TestInsightEvidenceGolden asserts the deterministic counts from the design's
// golden session. It runs only when the real Copilot session is present on this
// machine (it is not committed); CI without it simply skips.
func TestInsightEvidenceGolden(t *testing.T) {
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".copilot", "session-state", "27e7dd07-40e7-4728-9bc8-c70ee77f4d04", "events.jsonl")
	if _, err := os.Stat(p); err != nil {
		t.Skip("golden session not present on this machine")
	}
	ev, err := parseInsightEvidence(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.Subagents) != 27 {
		t.Errorf("want 27 subagents, got %d", len(ev.Subagents))
	}
	var reqs, sync, promptChars int
	var tokens int64
	for _, s := range ev.Subagents {
		reqs += s.RequestCount
		tokens += s.OutputTokens
		promptChars += s.PromptChars
		if s.Mode == "sync" {
			sync++
		}
	}
	if reqs != 277 {
		t.Errorf("want 277 attributed responses, got %d", reqs)
	}
	if tokens != 177449 {
		t.Errorf("want 177449 subagent output tokens, got %d", tokens)
	}
	if sync != 22 {
		t.Errorf("want 22 sync delegations, got %d", sync)
	}
	if promptChars != 68333 {
		t.Errorf("want 68333 delegation prompt chars, got %d", promptChars)
	}
}
