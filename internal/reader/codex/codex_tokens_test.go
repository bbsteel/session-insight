package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

// Codex reports inclusive semantics (cached_input ⊂ input, reasoning ⊂
// output). The parser must convert to the canonical exclusive buckets.
func TestParseCodexEventsCanonicalTokenBuckets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	lines := []string{
		`{"timestamp":"2026-06-20T01:00:00.000Z","type":"event_msg","payload":{"type":"task_started"}}`,
		`{"timestamp":"2026-06-20T01:00:01.000Z","type":"event_msg","payload":{"type":"user_message","message":"hi"}}`,
		`{"timestamp":"2026-06-20T01:00:02.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":800,"output_tokens":100,"reasoning_output_tokens":30},"last_token_usage":{"input_tokens":1000,"cached_input_tokens":800,"output_tokens":100,"reasoning_output_tokens":30}}}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	turns, _ := parseCodexEvents(path)
	if len(turns) == 0 {
		t.Fatal("expected at least one turn")
	}
	u := turns[0].TokenUsage
	if u.PromptTokens != 200 {
		t.Errorf("prompt must be input−cached (200), got %d", u.PromptTokens)
	}
	if u.CacheReadTokens != 800 || u.CompletionTokens != 100 {
		t.Errorf("cache/output mismatch: %+v", u)
	}
	if u.ReasoningTokens != 30 {
		t.Errorf("reasoning annotation mismatch: %d", u.ReasoningTokens)
	}
	if u.Present.CacheWrite != model.PresenceNA {
		t.Errorf("cache_write must be n/a for codex, got %q", u.Present.CacheWrite)
	}
	if u.Present.Input != model.PresenceExact {
		t.Errorf("input presence must be exact, got %q", u.Present.Input)
	}
}
