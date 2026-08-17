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

func TestExtractCreationEvidenceRejectsUnsuccessfulAndAmbiguousResults(t *testing.T) {
	base := []model.RenderEvent{{EventID: "invoke", Type: "ToolInvocation", ToolInput: map[string]any{"command": "glab mr create"}}}
	url := "https://gitlab.com/acme/widgets/-/merge_requests/7"
	cases := []struct {
		event    model.RenderEvent
		wantKind string
		wantURL  string
	}{
		{
			event:    model.RenderEvent{Type: "ToolResult", ParentEventID: "invoke", ExitCode: 1, Timestamp: time.Unix(1, 0).UTC(), Stdout: url},
			wantKind: CommandChangeRequestURL, wantURL: url,
		},
		{event: model.RenderEvent{Type: "ToolResult", ParentEventID: "invoke", Truncated: true, Timestamp: time.Unix(1, 0).UTC(), Stdout: url}},
		{event: model.RenderEvent{Type: "ToolResult", ParentEventID: "invoke", TimedOut: true, Timestamp: time.Unix(1, 0).UTC(), Stdout: url}},
		{event: model.RenderEvent{Type: "ToolResult", ParentEventID: "invoke", Rejected: true, Timestamp: time.Unix(1, 0).UTC(), Stdout: url}},
		{
			event:    model.RenderEvent{Type: "ToolResult", ParentEventID: "invoke", ErrorKind: "tool_error", Timestamp: time.Unix(1, 0).UTC(), Stdout: url},
			wantKind: CommandChangeRequestURL, wantURL: url,
		},
		{
			event:    model.RenderEvent{Type: "ToolResult", Timestamp: time.Unix(1, 0).UTC(), Stdout: url},
			wantKind: CommandChangeRequestURL, wantURL: url,
		},
		{
			event:    model.RenderEvent{Type: "ToolResult", ParentEventID: "missing", Timestamp: time.Unix(1, 0).UTC(), Stdout: url},
			wantKind: CommandChangeRequestURL, wantURL: url,
		},
		{
			event:    model.RenderEvent{Type: "ToolResult", ParentEventID: "invoke", Timestamp: time.Unix(1, 0).UTC(), Stdout: url + " https://gitlab.com/acme/widgets/-/merge_requests/8"},
			wantKind: CommandChangeRequestURL, wantURL: url,
		},
		{
			event:    model.RenderEvent{Type: "ToolResult", ParentEventID: "invoke", Timestamp: time.Unix(1, 0).UTC(), Stdout: "https://github.com/acme/widgets/pull/42"},
			wantKind: CommandChangeRequestURL, wantURL: "https://github.com/acme/widgets/pull/42",
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

func TestExtractCreationEvidenceDeduplicatesRepeatedNormalizedURL(t *testing.T) {
	now := time.Date(2026, 8, 11, 16, 17, 21, 0, time.UTC)
	events := []model.RenderEvent{
		{EventID: "invoke", Type: "ToolInvocation", ToolInput: map[string]any{"command": "gh pr create --fill"}},
		{EventID: "result-a", ParentEventID: "invoke", Type: "ToolResult", Timestamp: now,
			Stdout: "https://github.com/acme/widgets/pull/42/\nhttps://github.com/acme/widgets/pull/42"},
		{EventID: "result-b", ParentEventID: "invoke", Type: "ToolResult", Timestamp: now,
			Stdout: "https://github.com/acme/widgets/pull/42"},
	}
	got := ExtractCreationEvidence(events, "sha256:source")
	if len(got) != 1 || got[0].Reference.NormalizedURL != "https://github.com/acme/widgets/pull/42" ||
		got[0].EventID != "invoke" || got[0].EvidenceID == "" {
		t.Fatalf("unexpected deduped evidence: %+v", got)
	}
	again := ExtractCreationEvidence(events, "sha256:source")
	if again[0].EvidenceID != got[0].EvidenceID {
		t.Fatalf("evidence id is not deterministic: %q vs %q", got[0].EvidenceID, again[0].EvidenceID)
	}
}

func TestExtractCreationEvidenceDoesNotPromoteEscapedQuotedCreate(t *testing.T) {
	events := []model.RenderEvent{
		{EventID: "invoke", Type: "ToolInvocation", ToolInput: map[string]any{
			"command": `echo "https://github.com/acme/widgets/pull/42 \" && gh pr create --fill"`,
		}},
		{Type: "ToolResult", ParentEventID: "invoke", Timestamp: time.Now().UTC(),
			Stdout: "https://github.com/acme/widgets/pull/42 \" && gh pr create --fill\n"},
	}
	got := ExtractCreationEvidence(events, "sha256:source")
	if len(got) != 1 || got[0].CommandKind != CommandChangeRequestURL ||
		got[0].Reference.NormalizedURL != "https://github.com/acme/widgets/pull/42" {
		t.Fatalf("escaped quoted create should stay a URL mention: %+v", got)
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

func TestExtractCreationEvidenceIgnoresNonReviewURLs(t *testing.T) {
	events := []model.RenderEvent{
		{EventID: "note", Type: "TextChunk", Timestamp: time.Unix(1, 0).UTC(),
			Text: "See https://gitee.com/acme/widgets and https://wiki.example/about"},
	}
	if got := ExtractCreationEvidence(events, "sha256:source"); len(got) != 0 {
		t.Fatalf("non-review URLs indexed: %+v", got)
	}
}
