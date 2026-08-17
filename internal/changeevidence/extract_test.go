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

func TestExtractCreationEvidenceAcceptsCreateInUnquotedCommandChain(t *testing.T) {
	now := time.Date(2026, 8, 15, 9, 7, 59, 0, time.UTC)
	command := "cd /workspace/sanitized-project && git push -u origin HEAD && gh pr create --title sanitized --body \"$(cat <<'EOF'\nWhat:\n- mention gh pr create in a quote\nEOF\n)\""
	events := []model.RenderEvent{
		{EventID: "invoke", Type: "ToolInvocation", ToolName: "Run",
			ToolInput: map[string]any{"command": command}},
		{EventID: "result", ParentEventID: "invoke", Type: "ToolResult", Timestamp: now,
			Stdout: "To https://github.com/acme/widgets.git\nhttps://github.com/acme/widgets/pull/42\n"},
	}
	got := ExtractCreationEvidence(events, "index:grok:s1:1")
	if len(got) != 1 || got[0].Reference.NormalizedURL != "https://github.com/acme/widgets/pull/42" ||
		got[0].CommandKind != CommandGitHubCLI {
		t.Fatalf("chained create not extracted: %+v", got)
	}
}

func TestExtractCreationEvidenceAcceptsQuotedHeredocBodyAfterPush(t *testing.T) {
	command := "cd /workspace/sanitized-project && git push -u origin HEAD && gh pr create --title \"Complete coverage\" --body \"$(cat <<'EOF'\n## Summary\n- mention gh pr create in a quote\nEOF\n)\""
	events := []model.RenderEvent{
		{EventID: "invoke", Type: "ToolInvocation", ToolInput: map[string]any{"command": command}},
		{EventID: "result", ParentEventID: "invoke", Type: "ToolResult",
			Timestamp: time.Date(2026, 8, 15, 9, 7, 59, 0, time.UTC),
			Stdout:    "https://github.com/bbsteel/session-insight/pull/139\n"},
	}
	got := ExtractCreationEvidence(events, "index:grok:s1:1")
	if len(got) != 1 || got[0].Reference.NormalizedURL != "https://github.com/bbsteel/session-insight/pull/139" {
		t.Fatalf("heredoc-bodied create not extracted: %+v", got)
	}
}

func TestExtractCreationEvidenceAcceptsCmdField(t *testing.T) {
	events := []model.RenderEvent{
		{EventID: "invoke", Type: "ToolInvocation", ToolInput: map[string]any{"cmd": "gh pr create --fill"}},
		{Type: "ToolResult", ParentEventID: "invoke", Timestamp: time.Now().UTC(),
			Stdout: "https://github.com/acme/widgets/pull/42"},
	}
	if got := ExtractCreationEvidence(events, "sha256:source"); len(got) != 1 {
		t.Fatalf("cmd field ignored: %+v", got)
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
