package db

import (
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestReplaceAndReverseLookupChangeRequestCreationEvidence(t *testing.T) {
	database := openTestDB(t)
	insertTestSession(t, database, "codex", "created-pr")
	now := time.Date(2026, 8, 11, 16, 17, 21, 0, time.UTC)
	evidence := model.ChangeRequestCreationEvidence{
		EvidenceID: "cr-create-one",
		Reference: model.ChangeRequestReference{
			Provider: model.ChangeProviderGitHub, DisplayOrigin: "https://github.com",
			TargetRepositorySlug: "acme/widgets", DisplayNumber: "42",
			NormalizedURL: "https://github.com/acme/widgets/pull/42",
		},
		CommandKind: "github_cli_pr_create", ToolName: "exec", EventID: "invoke",
		ToolCallID: "call-1", TurnIndex: 7, RecordedAt: now,
		SourceRevision: "sha256:one", Assessment: model.ExactGitEvidence(),
	}
	if err := database.ReplaceSessionChangeRequestCreationEvidence("codex", "created-pr", "sha256:one", []model.ChangeRequestCreationEvidence{evidence}); err != nil {
		t.Fatal(err)
	}
	matches, err := database.ChangeRequestCreationSessions(evidence.Reference.NormalizedURL, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].RootSessionID != "created-pr" ||
		matches[0].Evidence.EventID != "invoke" || !matches[0].Evidence.RecordedAt.Equal(now) {
		t.Fatalf("unexpected matches: %+v", matches)
	}
	if err := database.ReplaceSessionChangeRequestCreationEvidence("codex", "created-pr", "sha256:two", nil); err != nil {
		t.Fatal(err)
	}
	matches, err = database.ChangeRequestCreationSessions(evidence.Reference.NormalizedURL, 100)
	if err != nil || len(matches) != 0 {
		t.Fatalf("stale evidence retained: matches=%+v err=%v", matches, err)
	}
	current, err := database.HasSessionChangeRequestCreationIndex("codex", "created-pr")
	if err != nil || !current {
		t.Fatalf("empty replacement index current=%v err=%v", current, err)
	}
}
