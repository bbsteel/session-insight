package gitevidence

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func defaultCandidateDiscoverer(t *testing.T, maxCandidates int) *CandidateCommitDiscoverer {
	t.Helper()
	limits := DefaultCandidateCommitLimits()
	if maxCandidates > 0 {
		limits.MaxCandidates = maxCandidates
	}
	discoverer, err := NewCandidateCommitDiscoverer(testRunner(t, nil), limits)
	if err != nil {
		t.Fatal(err)
	}
	return discoverer
}

func commitFixtureFile(t *testing.T, repositoryPath, relativePath, content, subject string, when time.Time) string {
	t.Helper()
	path := filepath.Join(repositoryPath, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, repositoryPath, "add", "--", relativePath)
	gitFixtureCommand(t, repositoryPath, when,
		"-c", "user.name=Fixture Author", "-c", "user.email=fixture@example.invalid",
		"commit", "--no-gpg-sign", "-m", subject)
	return strings.TrimSpace(gitCommand(t, repositoryPath, "rev-parse", "HEAD"))
}

func gitFixtureCommand(t *testing.T, cwd string, when time.Time, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = cwd
	stamp := when.Format(time.RFC3339)
	command.Env = append(os.Environ(),
		"LC_ALL=C", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Fixture Author", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=Fixture Author", "GIT_COMMITTER_EMAIL=fixture@example.invalid",
		"GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %q: %v\n%s", args, err, output)
	}
	return string(output)
}

func candidateInput(repository *Repository, origin, baseline, final string, paths []CandidateCommitPath, start, end time.Time) CandidateCommitInput {
	return CandidateCommitInput{
		Binding: repository.Binding("server-entry-key"), OriginSHA: origin, BaselineSHA: baseline, FinalSHA: final,
		ChangedPaths: paths, WindowStart: start, WindowEnd: end,
	}
}

func TestCandidateCommitsRankFirstParentThenMergeAncestryOnDetachedHead(t *testing.T) {
	parent := t.TempDir()
	repositoryPath := createRepository(t, parent, "merge-history")
	base := strings.TrimSpace(gitCommand(t, repositoryPath, "rev-parse", "HEAD"))
	fixtureStart := time.Now().UTC().Truncate(time.Second).Add(time.Minute)
	mainOne := commitFixtureFile(t, repositoryPath, "main-one.txt", "main one\n", "main one", fixtureStart)
	gitCommand(t, repositoryPath, "checkout", "-b", "feature")
	side := commitFixtureFile(t, repositoryPath, "side.txt", "side\n", "side commit", fixtureStart.Add(time.Minute))
	gitCommand(t, repositoryPath, "checkout", "main")
	mainTwo := commitFixtureFile(t, repositoryPath, "main-two.txt", "main two\n", "main two", fixtureStart.Add(2*time.Minute))
	gitFixtureCommand(t, repositoryPath, fixtureStart.Add(3*time.Minute),
		"-c", "user.name=Fixture Author", "-c", "user.email=fixture@example.invalid",
		"merge", "--no-ff", "--no-gpg-sign", "feature", "-m", "merge feature")
	merge := strings.TrimSpace(gitCommand(t, repositoryPath, "rev-parse", "HEAD"))
	gitCommand(t, repositoryPath, "checkout", "--detach", merge)

	repository := resolvedRepository(t, defaultResolver(t), repositoryPath)
	if !repository.Detached {
		t.Fatal("fixture HEAD is not detached")
	}
	result, err := defaultCandidateDiscoverer(t, 0).Discover(context.Background(), repository,
		candidateInput(repository, "", base, merge, nil, fixtureStart.Add(-30*time.Second), fixtureStart.Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{merge, mainTwo, mainOne, side}
	if len(result.Commits) != len(want) {
		t.Fatalf("candidate count = %d, want %d: %+v", len(result.Commits), len(want), result.Commits)
	}
	for index, sha := range want {
		commit := result.Commits[index]
		if commit.Ordinal != index || commit.SHA != sha || commit.Relation != model.GitCommitDescendant || commit.Assessment.State != model.GitEvidenceExact {
			t.Fatalf("candidate %d = %+v, want exact descendant %s", index, commit, sha)
		}
		if commit.AuthoredAt == nil || commit.CommittedAt == nil || commit.AuthorName != "Fixture Author" || commit.Evidence == nil {
			t.Fatalf("candidate metadata %d = %+v", index, commit)
		}
	}
	if result.Assessment.State != model.GitEvidenceExact {
		t.Fatalf("result assessment = %+v", result.Assessment)
	}
	encoded, err := json.Marshal(result.Commits)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "fixture@example.invalid") || strings.Contains(string(encoded), "author_email") {
		t.Fatalf("candidate result leaked author email: %s", encoded)
	}
}

func TestCandidateCommitsSupportSHA256(t *testing.T) {
	parent := t.TempDir()
	repositoryPath := filepath.Join(parent, "sha256-candidates")
	command := exec.Command("git", "init", "-b", "main", "--object-format=sha256", repositoryPath)
	command.Dir = parent
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("Git SHA-256 repositories unsupported: %v (%s)", err, output)
	}
	fixtureStart := time.Now().UTC().Truncate(time.Second).Add(time.Minute)
	base := commitFixtureFile(t, repositoryPath, "base.txt", "base\n", "sha256 base", fixtureStart)
	final := commitFixtureFile(t, repositoryPath, "candidate.txt", "candidate\n", "sha256 candidate", fixtureStart.Add(time.Minute))
	repository := resolvedRepository(t, defaultResolver(t), repositoryPath)
	result, err := defaultCandidateDiscoverer(t, 0).Discover(context.Background(), repository,
		candidateInput(repository, base, "", final, nil, fixtureStart.Add(30*time.Second), fixtureStart.Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Commits) != 1 || result.Commits[0].SHA != final || len(final) != 64 || result.Commits[0].Assessment.State != model.GitEvidenceExact {
		t.Fatalf("SHA-256 candidates = %+v", result)
	}
}

func TestCandidateCommitsSupplementExactTopologyWithEstimatedOverlap(t *testing.T) {
	repositoryPath := createRepository(t, t.TempDir(), "supplemented-candidates")
	base := strings.TrimSpace(gitCommand(t, repositoryPath, "rev-parse", "HEAD"))
	fixtureStart := time.Now().UTC().Truncate(time.Second).Add(time.Minute)
	final := commitFixtureFile(t, repositoryPath, "final.txt", "final\n", "exact descendant", fixtureStart)
	repository := resolvedRepository(t, defaultResolver(t), repositoryPath)
	result, err := defaultCandidateDiscoverer(t, 0).Discover(context.Background(), repository,
		candidateInput(repository, "", base, final,
			[]CandidateCommitPath{{Key: "tracked-key", Path: "tracked.txt"}}, fixtureStart.Add(-time.Hour), fixtureStart.Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Commits) < 2 {
		t.Fatalf("supplemented candidates = %+v", result.Commits)
	}
	if result.Commits[0].SHA != final || result.Commits[0].Relation != model.GitCommitDescendant || result.Commits[0].Assessment.State != model.GitEvidenceExact {
		t.Fatalf("topology candidate = %+v", result.Commits[0])
	}
	if result.Commits[1].SHA != base || result.Commits[1].Relation != model.GitCommitPathOverlap || result.Commits[1].Assessment.State != model.GitEvidenceEstimated || result.Commits[1].Assessment.ReasonCode != model.ReasonAgentGitFactMissing {
		t.Fatalf("path supplement = %+v", result.Commits[1])
	}
	if result.Assessment.State != model.GitEvidenceExact {
		t.Fatalf("complete discovery assessment = %+v", result.Assessment)
	}
}

func TestCandidateCommitsIgnoreReplacementRefsAndRemainReadOnly(t *testing.T) {
	repositoryPath := createRepository(t, t.TempDir(), "replacement-ref")
	base := strings.TrimSpace(gitCommand(t, repositoryPath, "rev-parse", "HEAD"))
	fixtureStart := time.Now().UTC().Truncate(time.Second).Add(time.Minute)
	first := commitFixtureFile(t, repositoryPath, "first.txt", "first\n", "first candidate", fixtureStart)
	final := commitFixtureFile(t, repositoryPath, "final.txt", "final\n", "final candidate", fixtureStart.Add(time.Minute))
	gitCommand(t, repositoryPath, "replace", final, base)
	repository := resolvedRepository(t, defaultResolver(t), repositoryPath)
	before := repositoryControlDigest(t, filepath.Join(repositoryPath, ".git"))
	result, err := defaultCandidateDiscoverer(t, 0).Discover(context.Background(), repository,
		candidateInput(repository, "", base, final, nil, fixtureStart.Add(-30*time.Second), fixtureStart.Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	after := repositoryControlDigest(t, filepath.Join(repositoryPath, ".git"))
	if before != after {
		t.Fatalf("candidate discovery mutated repository: before %s after %s", before, after)
	}
	if len(result.Commits) != 2 || result.Commits[0].SHA != final || result.Commits[1].SHA != first {
		t.Fatalf("replacement ref changed topology: %+v", result.Commits)
	}
}

func TestCandidateCommitsDegradeRewrittenAndMissingOrigins(t *testing.T) {
	repositoryPath := createRepository(t, t.TempDir(), "rewritten")
	fixtureStart := time.Now().UTC().Truncate(time.Second).Add(time.Minute)
	gitCommand(t, repositoryPath, "checkout", "-b", "topic")
	oldOrigin := commitFixtureFile(t, repositoryPath, "target.txt", "topic\n", "topic before rebase", fixtureStart)
	gitCommand(t, repositoryPath, "checkout", "main")
	_ = commitFixtureFile(t, repositoryPath, "main.txt", "main\n", "new main base", fixtureStart.Add(time.Minute))
	gitCommand(t, repositoryPath, "checkout", "topic")
	gitFixtureCommand(t, repositoryPath, fixtureStart.Add(2*time.Minute), "rebase", "main")
	final := strings.TrimSpace(gitCommand(t, repositoryPath, "rev-parse", "HEAD"))
	if final == oldOrigin {
		t.Fatal("rebase fixture did not rewrite the topic commit")
	}
	repository := resolvedRepository(t, defaultResolver(t), repositoryPath)
	windowStart, windowEnd := fixtureStart.Add(-time.Hour), fixtureStart.Add(time.Hour)
	paths := []CandidateCommitPath{{Key: "target-key", Path: "target.txt"}}

	t.Run("rewritten", func(t *testing.T) {
		result, err := defaultCandidateDiscoverer(t, 0).Discover(context.Background(), repository,
			candidateInput(repository, "", oldOrigin, final, paths, windowStart, windowEnd))
		if err != nil {
			t.Fatal(err)
		}
		if result.Assessment.ReasonCode != model.ReasonHeadHistoryRewritten || len(result.Commits) == 0 {
			t.Fatalf("rewritten degradation = %+v", result)
		}
		candidate := result.Commits[0]
		if candidate.SHA != final || candidate.Relation != model.GitCommitPathOverlap || candidate.Assessment.State != model.GitEvidenceEstimated || candidate.Assessment.ReasonCode != model.ReasonHeadHistoryRewritten {
			t.Fatalf("rewritten candidate = %+v", candidate)
		}
	})

	t.Run("missing_origin", func(t *testing.T) {
		missing := strings.Repeat("f", len(final))
		result, err := defaultCandidateDiscoverer(t, 0).Discover(context.Background(), repository,
			candidateInput(repository, missing, "", final, paths, windowStart, windowEnd))
		if err != nil {
			t.Fatal(err)
		}
		if result.Assessment.ReasonCode != model.ReasonSnapshotObjectMissing || len(result.Commits) == 0 {
			t.Fatalf("missing-origin degradation = %+v", result)
		}
		if result.Commits[0].Relation != model.GitCommitPathOverlap || result.Commits[0].Assessment.ReasonCode != model.ReasonSnapshotObjectMissing {
			t.Fatalf("missing-origin candidate = %+v", result.Commits[0])
		}
	})

	t.Run("missing_final", func(t *testing.T) {
		missing := strings.Repeat("e", len(final))
		result, err := defaultCandidateDiscoverer(t, 0).Discover(context.Background(), repository,
			candidateInput(repository, oldOrigin, "", missing, paths, windowStart, windowEnd))
		if err != nil {
			t.Fatal(err)
		}
		if result.Assessment.State != model.GitEvidenceMissing || result.Assessment.ReasonCode != model.ReasonSnapshotObjectMissing || len(result.Commits) != 0 {
			t.Fatalf("missing-final degradation = %+v", result)
		}
	})
}

func TestCandidateCommitsRankPathOverlapBeforeTimeOnly(t *testing.T) {
	repositoryPath := createRepository(t, t.TempDir(), "fallback-ranking")
	fixtureStart := time.Now().UTC().Truncate(time.Second).Add(time.Minute)
	target := commitFixtureFile(t, repositoryPath, "target.txt", "target\n", "touch target", fixtureStart)
	other := commitFixtureFile(t, repositoryPath, "other.txt", "other\n", "touch other", fixtureStart.Add(time.Minute))
	repository := resolvedRepository(t, defaultResolver(t), repositoryPath)
	windowStart, windowEnd := fixtureStart.Add(-time.Hour), fixtureStart.Add(time.Hour)

	result, err := defaultCandidateDiscoverer(t, 0).Discover(context.Background(), repository,
		candidateInput(repository, "", "", other, []CandidateCommitPath{{Key: "target-key", Path: "target.txt"}}, windowStart, windowEnd))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Commits) < 2 || result.Commits[0].SHA != target || result.Commits[0].Relation != model.GitCommitPathOverlap {
		t.Fatalf("path-first candidates = %+v", result.Commits)
	}
	if result.Assessment.ReasonCode != model.ReasonBaselineNotCaptured {
		t.Fatalf("path-only assessment = %+v", result.Assessment)
	}
	foundOther := false
	for _, commit := range result.Commits[1:] {
		if commit.SHA == other && commit.Relation == model.GitCommitTimeWindow && commit.Assessment.State == model.GitEvidenceEstimated {
			foundOther = true
		}
	}
	if !foundOther {
		t.Fatalf("time-only candidate missing after path candidates: %+v", result.Commits)
	}

	timeOnly, err := defaultCandidateDiscoverer(t, 0).Discover(context.Background(), repository,
		candidateInput(repository, "", "", other, nil, windowStart, windowEnd))
	if err != nil {
		t.Fatal(err)
	}
	if len(timeOnly.Commits) == 0 {
		t.Fatal("bounded time-only discovery returned no candidates")
	}
	for _, commit := range timeOnly.Commits {
		if commit.Relation != model.GitCommitTimeWindow || commit.Assessment.State != model.GitEvidenceEstimated || commit.Assessment.ReasonCode != model.ReasonBaselineNotCaptured {
			t.Fatalf("time-only candidate = %+v", commit)
		}
	}
}

func TestCandidateCommitsEnforceHistoryCap(t *testing.T) {
	repositoryPath := createRepository(t, t.TempDir(), "bounded-history")
	base := strings.TrimSpace(gitCommand(t, repositoryPath, "rev-parse", "HEAD"))
	fixtureStart := time.Now().UTC().Truncate(time.Second).Add(time.Minute)
	final := base
	for index := 0; index < 8; index++ {
		final = commitFixtureFile(t, repositoryPath, "history.txt", strings.Repeat("x", index+1), "history commit "+strconv.Itoa(index), fixtureStart.Add(time.Duration(index)*time.Minute))
	}
	repository := resolvedRepository(t, defaultResolver(t), repositoryPath)
	result, err := defaultCandidateDiscoverer(t, 3).Discover(context.Background(), repository,
		candidateInput(repository, "", base, final, nil, fixtureStart.Add(-time.Hour), fixtureStart.Add(2*time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Commits) != 3 || result.Assessment.State != model.GitEvidenceEstimated || result.Assessment.ReasonCode != model.ReasonSnapshotLimitExceeded {
		t.Fatalf("bounded history result = %+v", result)
	}
	for _, commit := range result.Commits {
		if commit.Relation != model.GitCommitDescendant || commit.Assessment.State != model.GitEvidenceExact {
			t.Fatalf("bounded exact topology candidate = %+v", commit)
		}
	}
}

func TestCandidateCommitsTreatMaliciousPathAsLiteralData(t *testing.T) {
	repositoryPath := createRepository(t, t.TempDir(), "literal-path")
	fixtureStart := time.Now().UTC().Truncate(time.Second).Add(time.Minute)
	maliciousPath := "tracked;touch candidate-injected"
	final := commitFixtureFile(t, repositoryPath, maliciousPath, "literal\n", "literal path", fixtureStart)
	marker := filepath.Join(repositoryPath, "candidate-injected")
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fixture unexpectedly created marker: %v", err)
	}
	repository := resolvedRepository(t, defaultResolver(t), repositoryPath)
	result, err := defaultCandidateDiscoverer(t, 0).Discover(context.Background(), repository,
		candidateInput(repository, "", "", final,
			[]CandidateCommitPath{{Key: "literal-key", Path: maliciousPath}}, fixtureStart.Add(-time.Hour), fixtureStart.Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Commits) == 0 || result.Commits[0].SHA != final || result.Commits[0].Relation != model.GitCommitPathOverlap {
		t.Fatalf("literal path candidates = %+v", result.Commits)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("injection-looking path executed; marker stat = %v", err)
	}
}

func TestCandidateCommitInputMustStayWithinBindingAndBounds(t *testing.T) {
	repositoryPath := createRepository(t, t.TempDir(), "invalid-input")
	final := strings.TrimSpace(gitCommand(t, repositoryPath, "rev-parse", "HEAD"))
	repository := resolvedRepository(t, defaultResolver(t), repositoryPath)
	start := time.Now().UTC()
	valid := candidateInput(repository, "", "", final, nil, start, start.Add(time.Hour))

	for name, mutate := range map[string]func(*CandidateCommitInput){
		"binding": func(input *CandidateCommitInput) { input.Binding.WorktreeID = "other" },
		"window":  func(input *CandidateCommitInput) { input.WindowEnd = input.WindowStart.Add(32 * 24 * time.Hour) },
		"path": func(input *CandidateCommitInput) {
			input.ChangedPaths = []CandidateCommitPath{{Key: "escape", Path: "../outside"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := valid
			mutate(&input)
			result, err := defaultCandidateDiscoverer(t, 0).Discover(context.Background(), repository, input)
			if typed := typedError(t, err); typed.Code != ErrorInvalidInput {
				t.Fatalf("code = %q, want %q", typed.Code, ErrorInvalidInput)
			}
			if result.Assessment.State != model.GitEvidenceUnavailable {
				t.Fatalf("invalid input assessment = %+v", result.Assessment)
			}
		})
	}
	forged := &Repository{
		WorktreeRoot: repository.WorktreeRoot, CommonRootID: repository.CommonRootID,
		WorktreeID: repository.WorktreeID, HeadSHA: repository.HeadSHA, ObjectFormat: repository.ObjectFormat,
	}
	result, err := defaultCandidateDiscoverer(t, 0).Discover(context.Background(), forged, valid)
	if typed := typedError(t, err); typed.Code != ErrorInvalidInput {
		t.Fatalf("forged repository code = %q, want %q", typed.Code, ErrorInvalidInput)
	}
	if result.Assessment.State != model.GitEvidenceUnavailable {
		t.Fatalf("forged repository assessment = %+v", result.Assessment)
	}
}
