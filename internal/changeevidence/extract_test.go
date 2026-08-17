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
		{EventID: "mention", Type: "TextChunk", Text: "https://github.com/acme/widgets/pull/99", Timestamp: now},
	}
	evidence := ExtractCreationEvidence(events, "sha256:source")
	if len(evidence) != 2 {
		t.Fatalf("evidence=%+v", evidence)
	}
	got := evidence[0]
	if got.Reference.NormalizedURL != "https://github.com/acme/widgets/pull/42" ||
		got.CommandKind != CommandGitHubCLI || got.Assessment.State != model.GitEvidenceExact ||
		got.SourceRevision != "sha256:source" || !got.RecordedAt.Equal(now) {
		t.Fatalf("unexpected created evidence: %+v", got)
	}
	if evidence[1].Reference.NormalizedURL != "https://github.com/acme/widgets/pull/99" ||
		evidence[1].CommandKind != CommandChangeRequestURL {
		t.Fatalf("unexpected mentioned evidence: %+v", evidence[1])
	}
}

func TestExtractCreationEvidenceRejectsFailureTruncationAndAmbiguousOutput(t *testing.T) {
	base := []model.RenderEvent{{EventID: "invoke", Type: "ToolInvocation", ToolInput: map[string]any{"command": "glab mr create"}}}
	cases := []struct {
		event    model.RenderEvent
		wantKind string
		wantURL  string
	}{
		{
			event:    model.RenderEvent{Type: "ToolResult", ParentEventID: "invoke", ExitCode: 1, Timestamp: time.Unix(1, 0).UTC(), Stdout: "https://gitlab.com/acme/widgets/-/merge_requests/7"},
			wantKind: CommandChangeRequestURL,
			wantURL:  "https://gitlab.com/acme/widgets/-/merge_requests/7",
		},
		{
			event: model.RenderEvent{Type: "ToolResult", ParentEventID: "invoke", Truncated: true, Timestamp: time.Unix(1, 0).UTC(), Stdout: "https://gitlab.com/acme/widgets/-/merge_requests/7"},
		},
		{
			event:    model.RenderEvent{Type: "ToolResult", ParentEventID: "invoke", Timestamp: time.Unix(1, 0).UTC(), Stdout: "https://gitlab.com/acme/widgets/-/merge_requests/7 https://gitlab.com/acme/widgets/-/merge_requests/8"},
			wantKind: CommandChangeRequestURL,
			wantURL:  "https://gitlab.com/acme/widgets/-/merge_requests/7",
		},
	}
	for _, test := range cases {
		got := ExtractCreationEvidence(append(base, test.event), "sha256:source")
		if test.wantKind == "" {
			if len(got) != 0 {
				t.Fatalf("unexpected evidence for %+v: %+v", test.event, got)
			}
			continue
		}
		if len(got) == 0 || got[0].CommandKind != test.wantKind || got[0].Reference.NormalizedURL != test.wantURL {
			t.Fatalf("evidence for %+v: %+v", test.event, got)
		}
		for _, item := range got {
			if item.CommandKind == CommandGitLabCLI {
				t.Fatalf("failed or ambiguous CLI output promoted to created: %+v", got)
			}
		}
	}
}

func TestExtractCreationEvidenceIndexesReviewURLsWithoutCLI(t *testing.T) {
	now := time.Date(2026, 8, 11, 16, 17, 21, 0, time.UTC)
	events := []model.RenderEvent{
		{EventID: "invoke", Type: "ToolInvocation", ToolInput: map[string]any{"command": "echo gh pr create"}},
		{Type: "ToolResult", ParentEventID: "invoke", Timestamp: now, Stdout: "https://github.com/acme/widgets/pull/42"},
		{EventID: "assistant", Type: "TextChunk", Timestamp: now, Text: "Opened https://gitee.com/acme/widgets/pulls/12 for review."},
	}
	got := ExtractCreationEvidence(events, "sha256:source")
	if len(got) != 2 {
		t.Fatalf("evidence=%+v", got)
	}
	if got[0].CommandKind != CommandChangeRequestURL ||
		got[0].Reference.NormalizedURL != "https://github.com/acme/widgets/pull/42" {
		t.Fatalf("unexpected GitHub mention: %+v", got[0])
	}
	if got[1].CommandKind != CommandChangeRequestURL ||
		got[1].Reference.Provider != model.ChangeProviderGeneric ||
		got[1].Reference.NormalizedURL != "https://gitee.com/acme/widgets/pulls/12" ||
		got[1].Reference.DisplayNumber != "12" {
		t.Fatalf("unexpected Gitee mention: %+v", got[1])
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

func TestExtractCreationEvidenceIgnoresNonReviewURLs(t *testing.T) {
	events := []model.RenderEvent{
		{EventID: "note", Type: "TextChunk", Timestamp: time.Unix(1, 0).UTC(),
			Text: "See https://gitee.com/acme/widgets and https://wiki.example/about"},
	}
	if got := ExtractCreationEvidence(events, "sha256:source"); len(got) != 0 {
		t.Fatalf("non-review URLs indexed: %+v", got)
	}
}
