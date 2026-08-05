package grok

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceInventoryRoles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("summary.json", `{}`)
	write("updates.jsonl", "{}\n")
	write("chat_history.jsonl", "{}\n")
	write("events.jsonl", "{}\n")
	write("rewind_points.jsonl", "{}\n")
	write("hunk_records.jsonl", "{}\n")
	write("system_prompt.txt", "you are")
	write("prompt_context.json", `{}`)
	write("signals.json", `{}`)
	write("resources_state.json", `{}`)
	write("announcement_state.json", `{}`)
	write("updates.jsonl.lock", "") // must be skipped
	sub := filepath.Join(dir, "subagents", "child1")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRel := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeRel(filepath.Join(sub, "meta.json"), `{}`)
	writeRel(filepath.Join(sub, "summary.json"), `{}`)

	sources := sourceInventory(dir, filepath.Join(dir, "summary.json"))
	byRole := map[string]int{}
	for _, s := range sources {
		byRole[s.Role]++
		if s.Path == dir {
			t.Fatalf("must not list session dir: %v", sources)
		}
		base := filepath.Base(s.Path)
		if strings.HasSuffix(base, ".lock") {
			t.Fatalf("must skip lock files: %s", s.Path)
		}
	}
	if byRole["primary_transcript"] != 1 {
		t.Fatalf("primary count=%d want 1 (%v)", byRole["primary_transcript"], byRole)
	}
	if byRole["updates"] != 1 {
		t.Fatalf("chat secondary should be updates role, got %v", byRole)
	}
	if byRole["events"] != 1 {
		t.Fatalf("events: %v", byRole)
	}
	// summary + system_prompt + prompt_context + signals + resources + announcement
	if byRole["metadata"] != 6 {
		t.Fatalf("metadata count=%d want 6 (%v)", byRole["metadata"], byRole)
	}
	if byRole["edit_cache"] != 1 || byRole["snapshot"] != 1 {
		t.Fatalf("hunk/rewind roles: %v", byRole)
	}
	if byRole["collaboration"] < 2 {
		t.Fatalf("want child meta+summary as collaboration, got %v", byRole)
	}
	if byRole["other"] != 0 {
		t.Fatalf("grok must not use other for known files: %v", byRole)
	}
}
