package changeevidence

import (
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestExtractCreationEvidenceRequiresSuccessfulPairedCommand(t *testing.T) {
	now := time.Date(2026, 8, 11, 16, 17, 21, 0, time.UTC)
	events := []model.RenderEvent{
		{EventID: "invoke", Type: "ToolInvocation", ToolName: "exec", ToolCallID: "call-1", TurnIndex: 4,
			ToolInput: map[string]any{"command": "gh pr create --base main"}},
		{EventID: "result", ParentEventID: "invoke", Type: "ToolResult", ToolCallID: "call-1", TurnIndex: 4,
			Timestamp: now, Stdout: "https://github.com/acme/widgets/pull/42\n", ExitCode: 0},
		{EventID: "mention", Type: "TextChunk", Text: "https://github.com/acme/widgets/pull/99"},
	}
	evidence := ExtractCreationEvidence(events, "sha256:source")
	if len(evidence) != 1 {
		t.Fatalf("evidence=%+v", evidence)
	}
	got := evidence[0]
	if got.Reference.NormalizedURL != "https://github.com/acme/widgets/pull/42" ||
		got.CommandKind != CommandGitHubCLI || got.Assessment.State != model.GitEvidenceExact ||
		got.SourceRevision != "sha256:source" || !got.RecordedAt.Equal(now) {
		t.Fatalf("unexpected evidence: %+v", got)
	}
}

func TestExtractCreationEvidenceRejectsFailureTruncationAndAmbiguousOutput(t *testing.T) {
	base := []model.RenderEvent{{EventID: "invoke", Type: "ToolInvocation", ToolInput: map[string]any{"command": "glab mr create"}}}
	cases := []model.RenderEvent{
		{Type: "ToolResult", ParentEventID: "invoke", ExitCode: 1, Stdout: "https://gitlab.com/acme/widgets/-/merge_requests/7"},
		{Type: "ToolResult", ParentEventID: "invoke", Truncated: true, Stdout: "https://gitlab.com/acme/widgets/-/merge_requests/7"},
		{Type: "ToolResult", ParentEventID: "invoke", Stdout: "https://gitlab.com/acme/widgets/-/merge_requests/7 https://gitlab.com/acme/widgets/-/merge_requests/8"},
	}
	for _, event := range cases {
		if got := ExtractCreationEvidence(append(base, event), "sha256:source"); len(got) != 0 {
			t.Fatalf("unexpected evidence for %+v: %+v", event, got)
		}
	}
}

func TestExtractCreationEvidenceDoesNotPromoteCommandMention(t *testing.T) {
	events := []model.RenderEvent{
		{EventID: "invoke", Type: "ToolInvocation", ToolInput: map[string]any{"command": "echo gh pr create"}},
		{Type: "ToolResult", ParentEventID: "invoke", Stdout: "https://github.com/acme/widgets/pull/42"},
	}
	if got := ExtractCreationEvidence(events, "sha256:source"); len(got) != 0 {
		t.Fatalf("unexpected evidence: %+v", got)
	}
}

func TestExtractCreationEvidenceSupportsGitLabMergeRequests(t *testing.T) {
	events := []model.RenderEvent{
		{EventID: "invoke", Type: "ToolInvocation", ToolName: "exec", ToolInput: map[string]any{"command": "glab mr create --fill"}},
		{Type: "ToolResult", ParentEventID: "invoke", Timestamp: time.Now().UTC(), Stdout: "https://gitlab.com/acme/widgets/-/merge_requests/7\n"},
	}
	got := ExtractCreationEvidence(events, "sha256:source")
	if len(got) != 1 || got[0].Reference.Provider != model.ChangeProviderGitLab ||
		got[0].CommandKind != CommandGitLabCLI || got[0].Reference.DisplayNumber != "7" {
		t.Fatalf("unexpected GitLab evidence: %+v", got)
	}
}
