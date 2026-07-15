package codex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadSessionMetaPrefersRolloutIDForSubagent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-date-child-id.jsonl")
	line := `{"timestamp":"2026-07-12T09:04:30Z","type":"session_meta","payload":{"session_id":"parent-id","id":"child-id","parent_thread_id":"parent-id","thread_source":"subagent","agent_path":"/root/audit","cwd":"/tmp/project"}}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	session, ok := readSessionMeta(path)
	if !ok {
		t.Fatal("expected session metadata")
	}
	if session.ResumeID != "child-id" {
		t.Fatalf("resume id = %q, want child-id", session.ResumeID)
	}
	if !session.IsSubagent || session.ParentSessionID != "parent-id" || session.AgentPath != "/root/audit" {
		t.Fatalf("subagent lineage = %+v", session)
	}
}

func TestReadSessionMetaDoesNotClassifyOrdinaryForkAsSubagent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-date-fork-id.jsonl")
	line := `{"timestamp":"2026-07-12T09:04:30Z","type":"session_meta","payload":{"session_id":"fork-id","id":"fork-id","forked_from_id":"parent-id","thread_source":"user","cwd":"/tmp/project"}}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	session, ok := readSessionMeta(path)
	if !ok {
		t.Fatal("expected session metadata")
	}
	if session.IsSubagent || session.ParentSessionID != "" {
		t.Fatalf("ordinary fork must remain a root session: %+v", session)
	}
}

func TestReadSessionMetaPrefersTurnContextModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-date-model-id.jsonl")
	lines := []byte(
		`{"timestamp":"2026-07-12T09:04:30Z","type":"session_meta","payload":{"id":"model-id","cwd":"/tmp/project","model_provider":"openai","base_instructions":{"text":"You are Codex, based on GPT-5."}}}` + "\n" +
			`{"timestamp":"2026-07-12T09:04:31Z","type":"turn_context","payload":{"model":"gpt-5.5"}}` + "\n",
	)
	if err := os.WriteFile(path, lines, 0o644); err != nil {
		t.Fatal(err)
	}

	session, ok := readSessionMeta(path)
	if !ok {
		t.Fatal("expected session metadata")
	}
	if session.ModelName != "gpt-5.5" {
		t.Fatalf("model name = %q, want gpt-5.5", session.ModelName)
	}

	_, modelName, provider := parseCodexEvents(path)
	if modelName != "gpt-5.5" || provider != "openai" {
		t.Fatalf("parseCodexEvents model/provider = %q/%q, want gpt-5.5/openai", modelName, provider)
	}
}
