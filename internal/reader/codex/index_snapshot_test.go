package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
)

func gitOriginFixtureReader() *CodexReader {
	return New(filepath.Join("testdata", "git-origin"))
}

func TestCodexAuthoritativeEnvelopeComplete(t *testing.T) {
	r := gitOriginFixtureReader()
	envelope := adaptertest.AssertIndexSnapshotEnvelope(t, r, adaptertest.IndexSnapshotEnvelopeExpect{
		SessionID: "complete",
	})
	origin := envelope.OriginGit
	assertExactStringGitFact(t, "repository_url", origin.RepositoryURL, "https://example.test/acme/widgets.git", envelope.SourceRevision)
	assertExactStringGitFact(t, "worktree_path", origin.WorktreePath, "/workspace/sanitized-project", envelope.SourceRevision)
	assertExactStringGitFact(t, "branch", origin.Branch, "feature/git-evidence", envelope.SourceRevision)
	assertExactStringGitFact(t, "head_sha", origin.HeadSHA, "0123456789abcdef0123456789abcdef01234567", envelope.SourceRevision)
	if origin.DirtyState.Value != model.GitDirtyUnknown ||
		origin.DirtyState.Assessment.State != model.GitEvidenceMissing ||
		origin.DirtyState.Assessment.ReasonCode != model.ReasonAgentGitFactMissing {
		t.Fatalf("dirty state was fabricated: %+v", origin.DirtyState)
	}
	finalization := envelope.Finalization
	if finalization.State != model.SessionFinalizationUnknown ||
		finalization.Assessment.Precision != model.SessionEvidenceMissing ||
		finalization.Assessment.ReasonCode != model.ReasonTurnMarkerNotSessionFinalization ||
		finalization.SignalKind != model.SessionSignalTurnComplete ||
		finalization.SignalAssessment.Precision != model.SessionEvidenceExact {
		t.Fatalf("task completion must remain a non-final session signal: %+v", finalization)
	}
	data, err := os.ReadFile(filepath.Join("testdata", "git-origin", "complete.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if envelope.SourceFingerprint.SizeBytes != int64(len(data)) {
		t.Fatalf("fingerprint size=%d want %d", envelope.SourceFingerprint.SizeBytes, len(data))
	}
	if envelope.Detail.CWD != "/workspace/sanitized-project" || len(envelope.Detail.Turns) != 1 || len(envelope.RenderEvents) == 0 {
		t.Fatalf("detail/render not built from fixture: detail=%+v events=%d", envelope.Detail.Session, len(envelope.RenderEvents))
	}
}

func TestCodexAuthoritativeEnvelopePartialInvalidAndPrivateValues(t *testing.T) {
	r := gitOriginFixtureReader()
	envelope := adaptertest.AssertIndexSnapshotEnvelope(t, r, adaptertest.IndexSnapshotEnvelopeExpect{
		SessionID: "partial-invalid",
		ForbiddenFragments: []string{
			"fixture-secret", "token@example.test", "relative/private-user",
		},
	})
	origin := envelope.OriginGit
	for name, fact := range map[string]model.GitFact[string]{
		"repository_url": origin.RepositoryURL,
		"worktree_path":  origin.WorktreePath,
		"head_sha":       origin.HeadSHA,
	} {
		if fact.Value != "" || fact.Assessment.State != model.GitEvidenceUnavailable || fact.Assessment.ReasonCode != model.ReasonAgentGitFactInvalid {
			t.Errorf("%s invalid fact=%+v", name, fact)
		}
	}
	assertExactStringGitFact(t, "branch", origin.Branch, "feature/partial", envelope.SourceRevision)
	if envelope.Detail.CWD != "" {
		t.Fatalf("invalid relative cwd escaped in detail: %q", envelope.Detail.CWD)
	}
	if envelope.Finalization.SignalAssessment.Precision != model.SessionEvidenceEstimated ||
		envelope.Finalization.SignalAssessment.ReasonCode != model.ReasonSessionSignalTimestampInvalid {
		t.Fatalf("invalid signal timestamp assessment=%+v", envelope.Finalization.SignalAssessment)
	}
}

func TestCodexAuthoritativeEnvelopeAbsentFacts(t *testing.T) {
	r := gitOriginFixtureReader()
	envelope := adaptertest.AssertIndexSnapshotEnvelope(t, r, adaptertest.IndexSnapshotEnvelopeExpect{SessionID: "absent"})
	for name, fact := range map[string]model.GitFact[string]{
		"repository_url": envelope.OriginGit.RepositoryURL,
		"branch":         envelope.OriginGit.Branch,
		"head_sha":       envelope.OriginGit.HeadSHA,
	} {
		if fact.Value != "" || fact.Assessment.State != model.GitEvidenceMissing || fact.Assessment.ReasonCode != model.ReasonAgentGitFactMissing {
			t.Errorf("%s missing fact=%+v", name, fact)
		}
	}
	assertExactStringGitFact(t, "worktree_path", envelope.OriginGit.WorktreePath, "/workspace/no-git-metadata", envelope.SourceRevision)
	if envelope.Finalization.SignalKind != model.SessionSignalNone ||
		envelope.Finalization.Assessment.ReasonCode != model.ReasonSessionStateNotRecorded {
		t.Fatalf("absent finalization=%+v", envelope.Finalization)
	}
}

func TestCodexAuthoritativeEnvelopeDegradesUntimedOriginFacts(t *testing.T) {
	for _, test := range []struct {
		name             string
		outerTimestamp   string
		payloadTimestamp string
	}{
		{name: "missing"},
		{name: "invalid", outerTimestamp: "not-a-timestamp", payloadTimestamp: "also-not-a-timestamp"},
	} {
		t.Run(test.name, func(t *testing.T) {
			sessionsDir := t.TempDir()
			path := filepath.Join(sessionsDir, "untimed.jsonl")
			metaPayload := map[string]any{
				"id":  "untimed",
				"cwd": "/workspace/untimed-project",
				"git": map[string]any{
					"repository_url": "https://example.test/acme/untimed.git",
					"branch":         "feature/untimed",
					"commit_hash":    "abcdef0123456789abcdef0123456789abcdef01",
				},
			}
			if test.payloadTimestamp != "" {
				metaPayload["timestamp"] = test.payloadTimestamp
			}
			metaLine := map[string]any{"type": "session_meta", "payload": metaPayload}
			if test.outerTimestamp != "" {
				metaLine["timestamp"] = test.outerTimestamp
			}
			lines := []map[string]any{
				metaLine,
				{
					"timestamp": "2026-08-11T08:01:00Z",
					"type":      "event_msg",
					"payload": map[string]any{
						"type":    "user_message",
						"message": "test an untimed origin",
					},
				},
			}
			var data []byte
			for _, line := range lines {
				encoded, err := json.Marshal(line)
				if err != nil {
					t.Fatal(err)
				}
				data = append(data, encoded...)
				data = append(data, '\n')
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}

			envelope, err := New(sessionsDir).ReadIndexSnapshotEnvelope(context.Background(), model.Session{ID: "untimed"})
			if err != nil {
				t.Fatal(err)
			}
			for name, check := range map[string]struct {
				fact model.GitFact[string]
				want string
			}{
				"repository_url": {envelope.OriginGit.RepositoryURL, "https://example.test/acme/untimed.git"},
				"worktree_path":  {envelope.OriginGit.WorktreePath, "/workspace/untimed-project"},
				"branch":         {envelope.OriginGit.Branch, "feature/untimed"},
				"head_sha":       {envelope.OriginGit.HeadSHA, "abcdef0123456789abcdef0123456789abcdef01"},
			} {
				if check.fact.Value != check.want || check.fact.Assessment.State != model.GitEvidenceEstimated ||
					check.fact.Assessment.ReasonCode != model.ReasonAgentGitFactTimestampUnavailable || check.fact.RecordedAt != nil {
					t.Errorf("%s untimed fact=%+v want value %q", name, check.fact, check.want)
				}
			}
			if validation := model.ValidateIndexSnapshotEnvelope(envelope); !validation.OK() {
				t.Fatalf("untimed envelope must remain valid: %+v", validation.Issues)
			}
			if envelope.Detail.Repository != "" || envelope.Detail.Branch != "" {
				t.Fatalf("legacy detail fields must remain empty: repository=%q branch=%q", envelope.Detail.Repository, envelope.Detail.Branch)
			}
		})
	}
}

func TestCodexOpenTurnDoesNotFabricateLiveState(t *testing.T) {
	envelope := adaptertest.AssertIndexSnapshotEnvelope(t, gitOriginFixtureReader(), adaptertest.IndexSnapshotEnvelopeExpect{SessionID: "open-turn"})
	if envelope.Finalization.State != model.SessionFinalizationUnknown ||
		envelope.Finalization.SignalKind != model.SessionSignalTurnOpen ||
		envelope.Finalization.Assessment.Precision != model.SessionEvidenceMissing ||
		envelope.Finalization.Assessment.ReasonCode != model.ReasonTurnMarkerNotSessionLiveness {
		t.Fatalf("open task must not be promoted to live: %+v", envelope.Finalization)
	}
}

func TestCodexAuthoritativeRevisionChangesWithParsedBytes(t *testing.T) {
	sessionsDir := t.TempDir()
	path := filepath.Join(sessionsDir, "revision.jsonl")
	original, err := os.ReadFile(filepath.Join("testdata", "git-origin", "complete.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(sessionsDir)
	first, err := r.ReadIndexSnapshotEnvelope(context.Background(), model.Session{ID: "revision"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(original, []byte("\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := r.ReadIndexSnapshotEnvelope(context.Background(), model.Session{ID: "revision"})
	if err != nil {
		t.Fatal(err)
	}
	if first.SourceRevision == second.SourceRevision {
		t.Fatalf("source revision did not change after byte mutation: %q", first.SourceRevision)
	}
	if first.Detail.Name != second.Detail.Name || len(first.RenderEvents) != len(second.RenderEvents) {
		t.Fatal("non-semantic byte mutation unexpectedly changed parsed detail/render data")
	}
}

func TestCodexAuthoritativeEnvelopeSatisfiesProtocol(t *testing.T) {
	var _ interface {
		ReadIndexSnapshotEnvelope(context.Context, model.Session) (*model.IndexSnapshotEnvelope, error)
	} = (*CodexReader)(nil)
	if Capabilities().AdapterRevision != 5 {
		t.Fatalf("adapter revision=%d want 5", Capabilities().AdapterRevision)
	}
}

func assertExactStringGitFact(t *testing.T, name string, fact model.GitFact[string], value, revision string) {
	t.Helper()
	if fact.Value != value || fact.Assessment.State != model.GitEvidenceExact || fact.Source != model.GitSourceAgentRecorded || fact.SourceRevision != revision {
		t.Fatalf("%s fact=%+v", name, fact)
	}
	if fact.RecordedAt == nil {
		t.Fatalf("%s exact fact lacks recorded_at", name)
	}
}

func TestCodexGitOriginFixturesStaySynthetic(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("testdata", "git-origin"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		data, err := os.ReadFile(filepath.Join("testdata", "git-origin", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"/home/", "/Users/", "github.com/", "gitlab.com/"} {
			if strings.Contains(string(data), forbidden) {
				t.Errorf("fixture %s contains non-synthetic fragment %q", entry.Name(), forbidden)
			}
		}
	}
}
