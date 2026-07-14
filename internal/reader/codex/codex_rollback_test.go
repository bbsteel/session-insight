package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCodexRollbackFixture(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout-test.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func codexTurn(ts, id, prompt string) []string {
	return []string{
		`{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"task_started","turn_id":"` + id + `"}}`,
		`{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"user_message","message":"` + prompt + `"}}`,
		`{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"task_complete","turn_id":"` + id + `"}}`,
	}
}

func TestParseCodexRollbackKeepsHistoryOutsideActivePath(t *testing.T) {
	var lines []string
	lines = append(lines, codexTurn("2026-07-14T10:00:00Z", "t1", "one")...)
	lines = append(lines, codexTurn("2026-07-14T10:01:00Z", "t2", "two")...)
	lines = append(lines, codexTurn("2026-07-14T10:02:00Z", "t3", "three")...)
	lines = append(lines, codexTurn("2026-07-14T10:03:00Z", "t4", "four")...)
	lines = append(lines, `{"timestamp":"2026-07-14T10:04:00Z","type":"event_msg","payload":{"type":"thread_rolled_back","num_turns":3}}`)

	parsed, _ := parseCodexEvents(writeCodexRollbackFixture(t, lines...))
	if len(parsed.Active) != 1 || parsed.Active[0].UserMessage != "one" {
		t.Fatalf("active path = %+v, want only first turn", parsed.Active)
	}
	if parsed.Historical != 4 {
		t.Fatalf("historical = %d, want 4", parsed.Historical)
	}
	if len(parsed.RollbackGroups) != 1 {
		t.Fatalf("rollback groups = %d, want 1", len(parsed.RollbackGroups))
	}
	group := parsed.RollbackGroups[0]
	if group.AfterTurnIndex != 0 || len(group.Turns) != 3 {
		t.Fatalf("rollback group = %+v", group)
	}
	for i, turn := range group.Turns {
		if !turn.RolledBack || turn.OriginalTurnIndex != i+1 {
			t.Errorf("rolled-back turn %d metadata = %+v", i, turn)
		}
	}
}

func TestParseCodexRollbackAppliesBeforeEmptyTurnFiltering(t *testing.T) {
	lines := codexTurn("2026-07-14T10:00:00Z", "t1", "keep")
	lines = append(lines,
		`{"timestamp":"2026-07-14T10:01:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"empty"}}`,
		`{"timestamp":"2026-07-14T10:01:01Z","type":"event_msg","payload":{"type":"turn_aborted","turn_id":"empty","reason":"interrupted"}}`,
		`{"timestamp":"2026-07-14T10:01:02Z","type":"event_msg","payload":{"type":"thread_rolled_back","num_turns":1}}`,
	)

	parsed, _ := parseCodexEvents(writeCodexRollbackFixture(t, lines...))
	if len(parsed.Active) != 1 || parsed.Active[0].UserMessage != "keep" {
		t.Fatalf("empty rollback removed a real turn: %+v", parsed.Active)
	}
	if parsed.Historical != 1 || len(parsed.RollbackGroups) != 0 {
		t.Fatalf("empty rollback should not surface: historical=%d groups=%+v", parsed.Historical, parsed.RollbackGroups)
	}
}

func TestParseCodexRollbackThenContinueRenumbersActivePath(t *testing.T) {
	var lines []string
	lines = append(lines, codexTurn("2026-07-14T10:00:00Z", "t1", "one")...)
	lines = append(lines, codexTurn("2026-07-14T10:01:00Z", "old", "old branch")...)
	lines = append(lines, `{"timestamp":"2026-07-14T10:02:00Z","type":"event_msg","payload":{"type":"thread_rolled_back","num_turns":1}}`)
	lines = append(lines, codexTurn("2026-07-14T10:03:00Z", "new", "new branch")...)

	parsed, _ := parseCodexEvents(writeCodexRollbackFixture(t, lines...))
	if len(parsed.Active) != 2 {
		t.Fatalf("active turns = %d, want 2", len(parsed.Active))
	}
	if parsed.Active[1].TurnIndex != 1 || parsed.Active[1].UserMessage != "new branch" {
		t.Fatalf("new branch was not renumbered as active Turn 2: %+v", parsed.Active[1])
	}
	if got := parsed.RollbackGroups[0].Turns[0].UserMessage; got != "old branch" {
		t.Fatalf("rolled-back branch = %q", got)
	}
}
