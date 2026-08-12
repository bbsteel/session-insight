package gitevidence

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func testMutationSource() MutationSource {
	return MutationSource{
		RootAgentType:  "codex",
		RootSessionID:  "root-session",
		SourceRevision: "sha256:" + strings.Repeat("a", 64),
		DefaultAttribution: MutationAttribution{
			SourceAgentType: "codex",
			SourceSessionID: "root-session",
		},
	}
}

func TestBuildFileMutationEvidenceApplyPatchOperationsAndStableIDs(t *testing.T) {
	timestamp := time.Date(2026, 8, 11, 8, 0, 0, 0, time.UTC)
	patch := "*** Begin Patch\n" +
		"*** Update File: src/main.go\n@@\n-old\n+new\n" +
		"*** Add File: src/new.go\n+new\n" +
		"*** Delete File: src/old.go\n-old\n" +
		"*** Update File: src/before.go\n*** Move to: src/after.go\n@@\n-old\n+new\n" +
		"*** End Patch"
	events := []model.RenderEvent{
		{
			EventID: "event-patch", Type: "ToolInvocation", Timestamp: timestamp,
			TurnIndex: 2, ToolName: "apply_patch", ToolCallID: "call-patch",
			ToolInput: map[string]any{"input": patch},
		},
		{
			EventID: "result-patch", ParentEventID: "event-patch", Type: "ToolResult",
			Timestamp: timestamp.Add(time.Second), TurnIndex: 2, ToolCallID: "call-patch",
		},
	}

	first, err := BuildFileMutationEvidence(events, testMutationSource())
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildFileMutationEvidence(events, testMutationSource())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("the same source revision and events must produce stable records")
	}
	if len(first.Issues) != 0 || len(first.Mutations) != 4 {
		t.Fatalf("build result=%+v", first)
	}
	want := []struct {
		operation    MutationOperation
		path         string
		previousPath string
	}{
		{MutationEdit, "src/main.go", ""},
		{MutationAdd, "src/new.go", ""},
		{MutationDelete, "src/old.go", ""},
		{MutationMove, "src/after.go", "src/before.go"},
	}
	for index, mutation := range first.Mutations {
		if mutation.ID == "" || mutation.EventID != "event-patch" || mutation.ResultEventID != "result-patch" ||
			mutation.ToolCallID != "call-patch" || mutation.SourceRevision != testMutationSource().SourceRevision ||
			mutation.Result != MutationSucceeded || mutation.RolledBack || mutation.RecordedAt == nil {
			t.Errorf("mutation[%d] identity/result=%+v", index, mutation)
		}
		if mutation.Operation != want[index].operation || mutation.Path != want[index].path || mutation.PreviousPath != want[index].previousPath {
			t.Errorf("mutation[%d]=%+v want %+v", index, mutation, want[index])
		}
	}
}

func TestBuildFileMutationEvidenceKeepsTerminalOutcomesWithoutParsingShell(t *testing.T) {
	events := []model.RenderEvent{
		{EventID: "edit-event", Type: "ToolInvocation", ToolName: "Edit", ToolCallID: "edit-call", ToolInput: map[string]any{"file_path": "src/edit.go"}},
		{Type: "ToolResult", ToolCallID: "edit-call", ExitCode: 1},
		{EventID: "write-event", Type: "ToolInvocation", ToolName: "Write", ToolCallID: "write-call", ToolInput: map[string]any{"file_path": "src/write.go"}},
		{Type: "ToolResult", ParentEventID: "write-event", ToolCallID: "write-call", Rejected: true},
		{EventID: "unknown-event", Type: "ToolInvocation", ToolName: "write_file", ToolCallID: "unknown-call", ToolInput: map[string]any{"file_path": "src/unknown.go"}},
		{EventID: "shell-event", Type: "ToolInvocation", ToolName: "exec", ToolCallID: "shell-call", ToolInput: map[string]any{"command": "apply_patch <<PATCH\n*** Add File: leaked.go\nPATCH"}},
		{EventID: "partial-patch", Type: "ToolInvocation", ToolName: "apply_patch", ToolInput: map[string]any{"input": "*** Begin Patch\n*** Add File: partial.go\n+partial"}},
	}

	result, err := BuildFileMutationEvidence(events, testMutationSource())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Mutations) != 3 {
		t.Fatalf("mutations=%+v", result.Mutations)
	}
	for index, want := range []MutationResultState{MutationFailed, MutationRejected, MutationUnknown} {
		if result.Mutations[index].Result != want {
			t.Errorf("mutation[%d].result=%q want %q", index, result.Mutations[index].Result, want)
		}
	}
	if result.Mutations[0].Operation != MutationEdit || result.Mutations[1].Operation != MutationWrite {
		t.Fatalf("normalized operations=%+v", result.Mutations)
	}
}

func TestBuildFileMutationEvidenceRejectsUnsafePathsWithoutLeakingThem(t *testing.T) {
	unsafe := []string{"../secret", "/etc/passwd", `C:\secret`, "bad\x00path", "bad\npath"}
	events := []model.RenderEvent{{
		EventID: "safe", Type: "ToolInvocation", ToolName: "Edit",
		ToolInput: map[string]any{"file_path": `src\nested/./safe.go`},
	}}
	for index, path := range unsafe {
		events = append(events, model.RenderEvent{
			EventID: "unsafe-" + string(rune('a'+index)), Type: "ToolInvocation",
			ToolName: "Edit", ToolInput: map[string]any{"file_path": path},
		})
	}

	result, err := BuildFileMutationEvidence(events, testMutationSource())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Mutations) != 1 || result.Mutations[0].Path != "src/nested/safe.go" {
		t.Fatalf("safe mutation normalization=%+v", result.Mutations)
	}
	if len(result.Issues) != len(unsafe) {
		t.Fatalf("issues=%+v", result.Issues)
	}
	for _, issue := range result.Issues {
		if issue.Code != MutationIssueInvalidPath {
			t.Errorf("issue=%+v", issue)
		}
	}
}

func TestBuildFileMutationEvidencePreservesRollbackAndSubagentAttribution(t *testing.T) {
	source := testMutationSource()
	source.InvocationAttribution = map[string]MutationAttribution{
		"child-invocation": {
			SourceAgentType: "claude", SourceSessionID: "child-source",
			BackingAgentType: "claude", BackingSessionID: "child-backing",
		},
	}
	events := []model.RenderEvent{
		{Type: "RollbackStart"},
		{
			EventID: "child-edit", Type: "ToolInvocation", TurnIndex: -2, Depth: 1,
			ToolName: "Edit", ToolCallID: "child-call", InvocationID: "child-invocation",
			ToolInput: map[string]any{"file_path": "src/child.go"},
		},
		{Type: "ToolResult", ParentEventID: "child-edit", ToolCallID: "child-call", InvocationID: "child-invocation", TurnIndex: -2},
		{Type: "RollbackEnd"},
	}
	result, err := BuildFileMutationEvidence(events, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Mutations) != 1 {
		t.Fatalf("mutations=%+v", result.Mutations)
	}
	mutation := result.Mutations[0]
	if !mutation.RolledBack || mutation.Result != MutationSucceeded || mutation.Depth != 1 || mutation.InvocationID != "child-invocation" ||
		mutation.SourceAgentType != "claude" || mutation.SourceSessionID != "child-source" ||
		mutation.BackingAgentType != "claude" || mutation.BackingSessionID != "child-backing" ||
		mutation.RootAgentType != "codex" || mutation.RootSessionID != "root-session" {
		t.Fatalf("rollback/subagent attribution=%+v", mutation)
	}
}

func TestBuildFileMutationEvidenceRejectsInvalidSource(t *testing.T) {
	source := testMutationSource()
	source.SourceRevision = ""
	_, err := BuildFileMutationEvidence(nil, source)
	if !errors.Is(err, ErrInvalidMutationSource) {
		t.Fatalf("error=%v", err)
	}

	source = testMutationSource()
	source.DefaultAttribution.BackingAgentType = "codex"
	_, err = BuildFileMutationEvidence(nil, source)
	if !errors.Is(err, ErrInvalidMutationSource) {
		t.Fatalf("partial backing error=%v", err)
	}
}

func TestSanitizeRepositoryRelativePath(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want string
		ok   bool
	}{
		{raw: "src/./nested//file.go", want: "src/nested/file.go", ok: true},
		{raw: `src\windows.go`, want: "src/windows.go", ok: true},
		{raw: "-leading-dash", want: "-leading-dash", ok: true},
		{raw: "../outside", ok: false},
		{raw: "/absolute", ok: false},
		{raw: `D:\absolute`, ok: false},
		{raw: `\\server\share`, ok: false},
		{raw: "bad\x00path", ok: false},
		{raw: "bad\tpath", ok: false},
	} {
		got, err := SanitizeRepositoryRelativePath(test.raw)
		if test.ok && (err != nil || got != test.want) {
			t.Errorf("SanitizeRepositoryRelativePath(%q)=(%q,%v) want %q", test.raw, got, err, test.want)
		}
		if !test.ok && !errors.Is(err, ErrInvalidMutationPath) {
			t.Errorf("SanitizeRepositoryRelativePath(%q) error=%v", test.raw, err)
		}
	}
}
