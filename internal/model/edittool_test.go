package model

import (
	"testing"
)

// ── IsEditTool ────────────────────────────────────────────────────────────────

func TestIsEditTool(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		// Claude
		{"Edit", true},
		{"str_replace_editor", true},
		// OpenCode
		{"edit", true},
		// Codex / Copilot
		{"apply_patch", true},
		// unrelated
		{"Bash", false},
		{"Read", false},
		{"", false},
		{"Write", false},
	}
	for _, tc := range cases {
		if got := IsEditTool(tc.name); got != tc.want {
			t.Errorf("IsEditTool(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// ── ExtractEditCalls — normalised input ───────────────────────────────────────

func TestExtractEditCallsNormalized(t *testing.T) {
	evt := RenderEvent{
		Type:      "ToolInvocation",
		TurnIndex: 3,
		ToolName:  "Edit",
		ToolInput: map[string]any{
			"file_path":  "src/foo.go",
			"old_string": "old",
			"new_string": "new",
		},
	}
	calls := ExtractEditCalls(evt)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	c := calls[0]
	if c.TurnIndex != 3 {
		t.Errorf("TurnIndex: got %d, want 3", c.TurnIndex)
	}
	if c.FilePath != "src/foo.go" {
		t.Errorf("FilePath: %q", c.FilePath)
	}
	if c.OldString != "old" {
		t.Errorf("OldString: %q", c.OldString)
	}
	if c.NewString != "new" {
		t.Errorf("NewString: %q", c.NewString)
	}
}

func TestExtractEditCallsNonEditToolReturnsNil(t *testing.T) {
	evt := RenderEvent{
		Type:      "ToolInvocation",
		ToolName:  "Bash",
		ToolInput: map[string]any{"command": "ls"},
	}
	if calls := ExtractEditCalls(evt); calls != nil {
		t.Errorf("expected nil for non-edit tool, got %v", calls)
	}
}

func TestExtractEditCallsEmptyNormalized(t *testing.T) {
	// All three fields empty → nil (no real edit described)
	evt := RenderEvent{
		Type:      "ToolInvocation",
		ToolName:  "Edit",
		ToolInput: map[string]any{},
	}
	if calls := ExtractEditCalls(evt); calls != nil {
		t.Errorf("expected nil for empty input, got %v", calls)
	}
}

// ── parseApplyPatch — Update File ─────────────────────────────────────────────

func TestParseApplyPatchUpdate(t *testing.T) {
	patch := "*** Begin Patch\n" +
		"*** Update File: src/main.go\n" +
		"@@ -1,3 +1,3 @@\n" +
		"-old line\n" +
		"+new line\n" +
		" context\n" +
		"*** End Patch"
	evt := RenderEvent{
		ToolName:  "apply_patch",
		ToolInput: map[string]any{"args": patch},
	}
	calls := ExtractEditCalls(evt)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	c := calls[0]
	if c.FilePath != "src/main.go" {
		t.Errorf("FilePath: %q", c.FilePath)
	}
	if c.OldString != "old line\ncontext" {
		t.Errorf("OldString: %q", c.OldString)
	}
	if c.NewString != "new line\ncontext" {
		t.Errorf("NewString: %q", c.NewString)
	}
}

// ── parseApplyPatch — Add File ────────────────────────────────────────────────

func TestParseApplyPatchAddFile(t *testing.T) {
	// Add File sections have no @@ header; lines are + prefixed.
	patch := "*** Begin Patch\n" +
		"*** Add File: new/file.go\n" +
		"+package main\n" +
		"+\n" +
		"+func main() {}\n" +
		"*** End Patch"
	evt := RenderEvent{
		ToolName:  "apply_patch",
		ToolInput: map[string]any{"args": patch},
	}
	calls := ExtractEditCalls(evt)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call for Add File, got %d", len(calls))
	}
	c := calls[0]
	if c.FilePath != "new/file.go" {
		t.Errorf("FilePath: %q", c.FilePath)
	}
	if c.OldString != "" {
		t.Errorf("Add File should have empty OldString, got %q", c.OldString)
	}
	if c.NewString != "package main\n\nfunc main() {}" {
		t.Errorf("NewString: %q", c.NewString)
	}
}

// ── parseApplyPatch — Delete File ─────────────────────────────────────────────

func TestParseApplyPatchDeleteFile(t *testing.T) {
	// Delete File sections have no @@ header; removed lines are - prefixed.
	patch := "*** Begin Patch\n" +
		"*** Delete File: old/file.go\n" +
		"-line a\n" +
		"-line b\n" +
		"*** End Patch"
	evt := RenderEvent{
		ToolName:  "apply_patch",
		ToolInput: map[string]any{"args": patch},
	}
	calls := ExtractEditCalls(evt)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call for Delete File, got %d", len(calls))
	}
	c := calls[0]
	if c.FilePath != "old/file.go" {
		t.Errorf("FilePath: %q", c.FilePath)
	}
	if c.OldString != "line a\nline b" {
		t.Errorf("OldString: %q", c.OldString)
	}
	if c.NewString != "" {
		t.Errorf("Delete File should have empty NewString, got %q", c.NewString)
	}
}

// ── parseApplyPatch — multi-file ─────────────────────────────────────────────

func TestParseApplyPatchMultiFile(t *testing.T) {
	patch := "*** Begin Patch\n" +
		"*** Update File: a.go\n" +
		"@@ -1 +1 @@\n" +
		"-aold\n" +
		"+anew\n" +
		"*** Update File: b.go\n" +
		"@@ -1 +1 @@\n" +
		"-bold\n" +
		"+bnew\n" +
		"*** End Patch"
	evt := RenderEvent{
		ToolName:  "apply_patch",
		ToolInput: map[string]any{"args": patch},
	}
	calls := ExtractEditCalls(evt)
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].FilePath != "a.go" || calls[1].FilePath != "b.go" {
		t.Errorf("file paths: %q %q", calls[0].FilePath, calls[1].FilePath)
	}
}

// ── parseApplyPatch — input key variants ─────────────────────────────────────

func TestParseApplyPatchInputKeys(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: x.go\n@@ -1 +1 @@\n-o\n+n\n*** End Patch"
	for _, key := range []string{"args", "input", "patch"} {
		evt := RenderEvent{
			ToolName:  "apply_patch",
			ToolInput: map[string]any{key: patch},
		}
		calls := ExtractEditCalls(evt)
		if len(calls) != 1 {
			t.Errorf("key %q: expected 1 call, got %d", key, len(calls))
		}
	}
}

// ── parseApplyPatch — malformed / empty ──────────────────────────────────────

func TestParseApplyPatchEmptyPatch(t *testing.T) {
	evt := RenderEvent{
		ToolName:  "apply_patch",
		ToolInput: map[string]any{"args": ""},
	}
	if calls := ExtractEditCalls(evt); calls != nil {
		t.Errorf("empty patch: expected nil, got %v", calls)
	}
}

func TestParseApplyPatchNoBeginEnd(t *testing.T) {
	// Patch without Begin/End markers should produce no calls.
	evt := RenderEvent{
		ToolName:  "apply_patch",
		ToolInput: map[string]any{"args": "*** Update File: x.go\n@@ -1 +1 @@\n-o\n+n"},
	}
	if calls := ExtractEditCalls(evt); len(calls) != 0 {
		t.Errorf("patch without Begin/End: expected 0 calls, got %d", len(calls))
	}
}

func TestParseApplyPatchMissingInputKey(t *testing.T) {
	evt := RenderEvent{
		ToolName:  "apply_patch",
		ToolInput: map[string]any{"something_else": "data"},
	}
	if calls := ExtractEditCalls(evt); calls != nil {
		t.Errorf("missing input key: expected nil, got %v", calls)
	}
}
