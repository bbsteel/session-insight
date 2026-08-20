package main

import (
	"regexp"
	"testing"
)

func TestChecklistShape(t *testing.T) {
	if len(checklist) != 22 {
		t.Fatalf("checklist must hold the 22 fixed items, got %d", len(checklist))
	}
	idPattern := regexp.MustCompile(`^\d{2}-[a-z-]+$`)
	seen := map[string]bool{}
	for i, item := range checklist {
		if !idPattern.MatchString(item.ID) {
			t.Errorf("item %d: id %q does not match the NN-name convention", i, item.ID)
		}
		if seen[item.ID] {
			t.Errorf("duplicate item id %q", item.ID)
		}
		seen[item.ID] = true
		if item.Title == "" || item.Goal == "" {
			t.Errorf("item %s: title and goal are required", item.ID)
		}
		if len(item.Slots) == 0 || item.Slots[0].LogicalName != item.ID {
			t.Errorf("item %s: first slot must be the default state named after the item", item.ID)
		}
		for _, slot := range item.Slots {
			if !knownLogicalNames[slot.LogicalName] {
				t.Errorf("slot %s missing from known logical names", slot.LogicalName)
			}
			if logicalNameItemID[slot.LogicalName] != item.ID {
				t.Errorf("slot %s maps back to %q, want %q", slot.LogicalName, logicalNameItemID[slot.LogicalName], item.ID)
			}
		}
	}
}

// TestFoldPairs locks the fold-pair naming table from the design: the tool,
// never the human, owns the -toggled suffixes.
func TestFoldPairs(t *testing.T) {
	want := map[string][]string{
		"04-thinking":         {"04-thinking-toggled"},
		"05-tool-invocation":  {"05-tool-invocation-toggled"},
		"11-file-change":      {"11-file-change-toggled"},
		"12-subagent":         {"12-subagent-toggled"},
		"13-context-boundary": {"13-context-boundary-toggled"},
		"15-long-output":      {"15-long-output-toggled"},
		"20-tool-group":       {"20-tool-group-toggled"},
		"21-nested-fold":      {"21-nested-fold-inner-toggled", "21-nested-fold-outer-toggled"},
	}
	got := map[string][]string{}
	for _, item := range checklist {
		for _, slot := range item.Slots[1:] {
			got[item.ID] = append(got[item.ID], slot.LogicalName)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("foldable item count = %d, want %d (%v)", len(got), len(want), got)
	}
	for id, wantSlots := range want {
		gotSlots := got[id]
		if len(gotSlots) != len(wantSlots) {
			t.Errorf("%s: fold slots %v, want %v", id, gotSlots, wantSlots)
			continue
		}
		for i := range wantSlots {
			if gotSlots[i] != wantSlots[i] {
				t.Errorf("%s: fold slot %d = %q, want %q", id, i, gotSlots[i], wantSlots[i])
			}
		}
	}
}
