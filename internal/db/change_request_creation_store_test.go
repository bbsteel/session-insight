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

func TestReplaceChangeRequestCreationEvidenceRejectsInvalidItems(t *testing.T) {
	database := openTestDB(t)
	insertTestSession(t, database, "codex", "invalid-pr")
	valid := validCreationEvidence("sha256:one")
	cases := []struct {
		name     string
		revision string
		item     model.ChangeRequestCreationEvidence
	}{
		{name: "empty evidence id", revision: valid.SourceRevision, item: withCreationEvidence(valid, func(item *model.ChangeRequestCreationEvidence) { item.EvidenceID = "" })},
		{name: "source revision mismatch", revision: "sha256:other", item: valid},
		{name: "zero timestamp", revision: valid.SourceRevision, item: withCreationEvidence(valid, func(item *model.ChangeRequestCreationEvidence) { item.RecordedAt = time.Time{} })},
		{name: "non-exact", revision: valid.SourceRevision, item: withCreationEvidence(valid, func(item *model.ChangeRequestCreationEvidence) {
			item.Assessment = model.NonExactGitEvidence(model.GitEvidenceEstimated, model.ReasonChangeLinkAmbiguous)
		})},
		{name: "invalid reference", revision: valid.SourceRevision, item: withCreationEvidence(valid, func(item *model.ChangeRequestCreationEvidence) {
			item.Reference.NormalizedURL = "https://user:secret@github.com/acme/widgets/pull/42"
			item.Reference.TargetRepositorySlug = ""
		})},
	}
	for _, test := range cases {
		if err := database.ReplaceSessionChangeRequestCreationEvidence("codex", "invalid-pr", test.revision, []model.ChangeRequestCreationEvidence{test.item}); err == nil {
			t.Fatalf("%s: accepted invalid evidence", test.name)
		}
	}
	matches, err := database.ChangeRequestCreationSessions(valid.Reference.NormalizedURL, 100)
	if err != nil || len(matches) != 0 {
		t.Fatalf("rejected items were stored: matches=%+v err=%v", matches, err)
	}
}

func TestChangeRequestCreationSessionsOrdersNewestFirstAndBoundsLimit(t *testing.T) {
	database := openTestDB(t)
	insertTestSession(t, database, "codex", "newer")
	insertTestSession(t, database, "claude", "older")
	url := "https://github.com/acme/widgets/pull/42"
	older := validCreationEvidence("sha256:older")
	older.EvidenceID = "cr-create-older"
	older.EventID = "older"
	older.RecordedAt = time.Date(2026, 8, 11, 16, 0, 0, 0, time.UTC)
	newer := validCreationEvidence("sha256:newer")
	newer.EvidenceID = "cr-create-newer"
	newer.EventID = "newer"
	newer.RecordedAt = time.Date(2026, 8, 11, 17, 0, 0, 0, time.UTC)
	if err := database.ReplaceSessionChangeRequestCreationEvidence("claude", "older", older.SourceRevision, []model.ChangeRequestCreationEvidence{older}); err != nil {
		t.Fatal(err)
	}
	if err := database.ReplaceSessionChangeRequestCreationEvidence("codex", "newer", newer.SourceRevision, []model.ChangeRequestCreationEvidence{newer}); err != nil {
		t.Fatal(err)
	}
	matches, err := database.ChangeRequestCreationSessions(url, 0)
	if err != nil || len(matches) != 2 || matches[0].RootSessionID != "newer" || matches[1].RootSessionID != "older" {
		t.Fatalf("default order = %+v err=%v", matches, err)
	}
	limited, err := database.ChangeRequestCreationSessions(url, 1)
	if err != nil || len(limited) != 1 || limited[0].RootSessionID != "newer" {
		t.Fatalf("limit 1 = %+v err=%v", limited, err)
	}
	capped, err := database.ChangeRequestCreationSessions(url, 501)
	if err != nil || len(capped) != 2 {
		t.Fatalf("oversize limit = %+v err=%v", capped, err)
	}
}

func TestDeleteSessionDataCascadesCreationEvidence(t *testing.T) {
	database := openTestDB(t)
	insertTestSession(t, database, "codex", "delete-pr")
	evidence := validCreationEvidence("sha256:one")
	if err := database.ReplaceSessionChangeRequestCreationEvidence("codex", "delete-pr", evidence.SourceRevision, []model.ChangeRequestCreationEvidence{evidence}); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteSessionData("codex", "delete-pr"); err != nil {
		t.Fatal(err)
	}
	matches, err := database.ChangeRequestCreationSessions(evidence.Reference.NormalizedURL, 100)
	if err != nil || len(matches) != 0 {
		t.Fatalf("creation evidence survived session delete: matches=%+v err=%v", matches, err)
	}
	current, err := database.HasSessionChangeRequestCreationIndex("codex", "delete-pr")
	if err != nil || current {
		t.Fatalf("creation index survived session delete: current=%v err=%v", current, err)
	}
}

func validCreationEvidence(sourceRevision string) model.ChangeRequestCreationEvidence {
	return model.ChangeRequestCreationEvidence{
		EvidenceID: "cr-create-" + sourceRevision,
		Reference: model.ChangeRequestReference{
			Provider: model.ChangeProviderGitHub, DisplayOrigin: "https://github.com",
			TargetRepositorySlug: "acme/widgets", DisplayNumber: "42",
			NormalizedURL: "https://github.com/acme/widgets/pull/42",
		},
		CommandKind: "github_cli_pr_create", ToolName: "exec", EventID: "invoke",
		ToolCallID: "call-1", TurnIndex: 7, RecordedAt: time.Date(2026, 8, 11, 16, 17, 21, 0, time.UTC),
		SourceRevision: sourceRevision, Assessment: model.ExactGitEvidence(),
	}
}

func withCreationEvidence(base model.ChangeRequestCreationEvidence, mutate func(*model.ChangeRequestCreationEvidence)) model.ChangeRequestCreationEvidence {
	mutate(&base)
	return base
}

func TestReplaceAndReverseLookupGenericChangeRequestURL(t *testing.T) {
	database := openTestDB(t)
	insertTestSession(t, database, "grok", "mentioned-pr")
	now := time.Date(2026, 8, 11, 16, 17, 21, 0, time.UTC)
	evidence := model.ChangeRequestCreationEvidence{
		EvidenceID: "cr-create-url",
		Reference: model.ChangeRequestReference{
			Provider: model.ChangeProviderGeneric, DisplayOrigin: "https://gitee.com",
			TargetRepositorySlug: "acme/widgets", DisplayNumber: "12",
			NormalizedURL: "https://gitee.com/acme/widgets/pulls/12",
		},
		CommandKind: "change_request_url", ToolName: "message", EventID: "assistant",
		TurnIndex: 2, RecordedAt: now,
		SourceRevision: "index:grok:mentioned-pr:1", Assessment: model.ExactGitEvidence(),
	}
	if err := database.ReplaceSessionChangeRequestCreationEvidence("grok", "mentioned-pr", evidence.SourceRevision, []model.ChangeRequestCreationEvidence{evidence}); err != nil {
		t.Fatal(err)
	}
	matches, err := database.ChangeRequestCreationSessions(evidence.Reference.NormalizedURL, 100)
	if err != nil || len(matches) != 1 || matches[0].RootSessionID != "mentioned-pr" ||
		matches[0].Evidence.CommandKind != "change_request_url" {
		t.Fatalf("unexpected generic URL matches: %+v err=%v", matches, err)
	}
}
