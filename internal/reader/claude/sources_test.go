package claude

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestSourceInventoryIncludesSubagentsAndTodos(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, "projects", "proj")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionID := "sess-abc"
	jsonl := filepath.Join(projects, sessionID+".jsonl")
	if err := os.WriteFile(jsonl, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(projects, sessionID, "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	subJSONL := filepath.Join(subDir, "agent-1.jsonl")
	subMeta := filepath.Join(subDir, "agent-1.meta.json")
	if err := os.WriteFile(subJSONL, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(subMeta, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	todoDir := filepath.Join(root, "todos")
	if err := os.MkdirAll(todoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	todoPath := filepath.Join(todoDir, sessionID+"-agent-"+sessionID+".json")
	if err := os.WriteFile(todoPath, []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Negative fixtures: must not appear as tool_results.
	for _, noise := range []string{
		sessionID + "-notes.txt",
		sessionID + "-agent-extra.txt",
		"other-" + sessionID + "-agent-x.json",
	} {
		if err := os.WriteFile(filepath.Join(todoDir, noise), []byte(`[]`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	sources := sourceInventory(jsonl, root)
	byRole := map[string][]string{}
	for _, s := range sources {
		byRole[s.Role] = append(byRole[s.Role], s.Path)
	}
	if len(byRole["primary_transcript"]) != 1 || byRole["primary_transcript"][0] != jsonl {
		t.Fatalf("primary=%v", byRole["primary_transcript"])
	}
	if len(byRole["collaboration"]) != 1 || byRole["collaboration"][0] != subJSONL {
		t.Fatalf("collaboration=%v", byRole["collaboration"])
	}
	if len(byRole["metadata"]) != 1 || byRole["metadata"][0] != subMeta {
		t.Fatalf("metadata=%v", byRole["metadata"])
	}
	if len(byRole["tool_results"]) != 1 || byRole["tool_results"][0] != todoPath {
		t.Fatalf("tool_results=%v", byRole["tool_results"])
	}
	if len(byRole["other"]) > 0 {
		t.Fatalf("unexpected other: %v", byRole["other"])
	}
}

func TestSourceInventoryMissingPrimary(t *testing.T) {
	root := t.TempDir()
	jsonl := filepath.Join(root, "projects", "p", "gone.jsonl")
	sources := sourceInventory(jsonl, root)
	if len(sources) != 1 {
		t.Fatalf("sources=%+v", sources)
	}
	if sources[0].Role != model.SourceRolePrimaryTranscript || sources[0].Path != filepath.Clean(jsonl) {
		t.Fatalf("source=%+v", sources[0])
	}
	if sources[0].State != model.SourceMissing {
		t.Fatalf("state=%s want missing", sources[0].State)
	}
}
