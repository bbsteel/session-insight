package indexer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
	sessionreader "github.com/bbsteel/session-insight/internal/reader"
)

type gitEvidenceReader struct {
	session         model.Session
	envelope        *model.IndexSnapshotEnvelope
	recheckEnvelope *model.IndexSnapshotEnvelope
	envelopeCalls   int
}

func (reader *gitEvidenceReader) AgentType() string   { return reader.session.AgentType }
func (reader *gitEvidenceReader) DisplayName() string { return "Git test" }
func (reader *gitEvidenceReader) ListSessions() ([]model.Session, error) {
	return []model.Session{reader.session}, nil
}
func (reader *gitEvidenceReader) GetSession(string) (*model.SessionDetail, error) {
	return reader.envelope.Detail, nil
}
func (reader *gitEvidenceReader) GetRenderEvents(string) ([]model.RenderEvent, error) {
	return reader.envelope.RenderEvents, nil
}
func (reader *gitEvidenceReader) RenderANSI(string, int) (string, error) { return "", nil }
func (reader *gitEvidenceReader) ReadIndexSnapshotEnvelope(context.Context, model.Session) (*model.IndexSnapshotEnvelope, error) {
	reader.envelopeCalls++
	if reader.envelopeCalls > 1 && reader.recheckEnvelope != nil {
		return reader.recheckEnvelope, nil
	}
	return reader.envelope, nil
}

func TestIndexerCapturesOnlyProvenLiveBaselineThenExactFinal(t *testing.T) {
	repository := initializeGitEvidenceRepository(t)
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	createdAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	session := model.Session{
		ID: "git-capture", AgentType: "git-test", CWD: repository,
		CreatedAt: createdAt, UpdatedAt: createdAt.Add(time.Second),
	}
	head := gitEvidenceCommand(t, repository, "rev-parse", "HEAD")
	reader := &gitEvidenceReader{session: session}
	reader.envelope = gitEvidenceEnvelope(session, repository, head, "a", model.SessionLive)
	index := New(database, []sessionreader.BaseSessionReader{reader})
	if err := index.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	first, ok, err := database.SessionGitEvidenceEnvelope(session.AgentType, session.ID)
	if err != nil || !ok || len(first.Repositories) != 1 {
		t.Fatalf("baseline evidence ok=%v repositories=%d err=%v", ok, len(first.Repositories), err)
	}
	baselineEvidence := first.Repositories[0]
	if baselineEvidence.Baseline == nil || baselineEvidence.Final != nil || !baselineEvidence.Provisional ||
		baselineEvidence.Assessment.ReasonCode != model.ReasonFinalNotCaptured {
		t.Fatalf("baseline evidence = %+v", baselineEvidence)
	}

	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	session.UpdatedAt = session.UpdatedAt.Add(time.Second)
	reader.session = session
	reader.envelope = gitEvidenceEnvelope(session, repository, head, "b", model.SessionFinalized)
	reader.envelope.RenderEvents = []model.RenderEvent{
		{
			EventID: "edit-tracked", Type: "ToolInvocation", Timestamp: session.UpdatedAt.Add(-time.Second),
			TurnIndex: 1, ToolName: "apply_patch", ToolCallID: "edit-call",
			ToolInput: map[string]any{"input": "*** Begin Patch\n*** Update File: tracked.txt\n@@\n-before\n+after\n*** End Patch"},
		},
		{
			EventID: "edit-result", ParentEventID: "edit-tracked", Type: "ToolResult",
			Timestamp: session.UpdatedAt, TurnIndex: 1, ToolCallID: "edit-call",
		},
	}
	if err := index.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	final, ok, err := database.SessionGitEvidenceEnvelope(session.AgentType, session.ID)
	if err != nil || !ok || len(final.Repositories) != 1 {
		t.Fatalf("final evidence ok=%v repositories=%d err=%v", ok, len(final.Repositories), err)
	}
	derived := final.Repositories[0]
	if derived.Baseline == nil || derived.Final == nil || derived.Provisional ||
		derived.Authority != model.GitAuthorityLocalInterval || len(derived.Files) != 1 {
		t.Fatalf("derived evidence = %+v", derived)
	}
	if derived.Files[0].DisplayPath != "tracked.txt" || derived.Files[0].Status != model.GitFileModified ||
		derived.Files[0].PatchAssessment.State != model.GitEvidenceExact ||
		derived.Files[0].Additions == nil || *derived.Files[0].Additions != 1 ||
		derived.Files[0].Deletions == nil || *derived.Files[0].Deletions != 1 || len(derived.Files[0].Evidence) != 1 ||
		derived.Files[0].Evidence[0].EventID != "edit-tracked" || derived.Files[0].Evidence[0].PositionsRevision < 1 {
		t.Fatalf("derived file = %+v", derived.Files[0])
	}
	patch, err := database.SessionGitEvidencePatch(
		session.AgentType, session.ID, derived.RepositoryEntryKey, derived.Files[0].Key, 1<<20,
	)
	if err != nil || !strings.Contains(string(patch), "-before\n+after\n") {
		t.Fatalf("retained patch = %q err=%v", patch, err)
	}

	// A newer source revision without exact finalization must stop serving the
	// previous local final immediately, while retaining its real baseline.
	session.UpdatedAt = session.UpdatedAt.Add(time.Second)
	reader.session = session
	reader.envelope = gitEvidenceEnvelope(session, repository, head, "f", model.SessionFinalizationUnknown)
	if err := index.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	invalidated, ok, err := database.SessionGitEvidenceEnvelope(session.AgentType, session.ID)
	if err != nil || !ok || len(invalidated.Repositories) != 1 {
		t.Fatalf("invalidated evidence ok=%v repositories=%d err=%v", ok, len(invalidated.Repositories), err)
	}
	current := invalidated.Repositories[0]
	if current.Baseline == nil || current.Final != nil || len(current.Files) != 0 ||
		current.Authority != model.GitAuthorityNone || current.Assessment.ReasonCode != model.ReasonFinalNotCaptured {
		t.Fatalf("invalidated evidence = %+v", current)
	}
}

func TestIndexerDoesNotBackfillHistoricalFinalBaseline(t *testing.T) {
	repository := initializeGitEvidenceRepository(t)
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	createdAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	session := model.Session{
		ID: "git-history", AgentType: "git-test", CWD: repository,
		CreatedAt: createdAt, UpdatedAt: createdAt.Add(time.Minute),
	}
	originHead := gitEvidenceCommand(t, repository, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repository, "later.txt"), []byte("later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitEvidenceCommand(t, repository, "add", "--", "later.txt")
	gitEvidenceCommand(t, repository, "-c", "user.name=Session Insight", "-c", "user.email=session-insight@example.invalid", "commit", "-q", "-m", "later")
	currentHead := gitEvidenceCommand(t, repository, "rev-parse", "HEAD")
	if currentHead == originHead {
		t.Fatal("test repository HEAD did not advance")
	}
	reader := &gitEvidenceReader{session: session, envelope: gitEvidenceEnvelope(session, repository, originHead, "c", model.SessionFinalized)}
	index := New(database, []sessionreader.BaseSessionReader{reader})
	if err := index.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	evidence, ok, err := database.SessionGitEvidenceEnvelope(session.AgentType, session.ID)
	if err != nil || !ok || len(evidence.Repositories) != 1 {
		t.Fatalf("historical evidence ok=%v repositories=%d err=%v", ok, len(evidence.Repositories), err)
	}
	repositoryEvidence := evidence.Repositories[0]
	if repositoryEvidence.Baseline != nil || repositoryEvidence.Final != nil ||
		repositoryEvidence.Assessment.ReasonCode != model.ReasonBaselineNotCaptured ||
		repositoryEvidence.Authority != model.GitAuthorityNone {
		t.Fatalf("historical evidence = %+v", repositoryEvidence)
	}
	if repositoryEvidence.Repository.HeadSHA != originHead || repositoryEvidence.Repository.HeadSHA == currentHead {
		t.Fatalf("historical binding HEAD = %q, origin=%q current=%q", repositoryEvidence.Repository.HeadSHA, originHead, currentHead)
	}
}

func TestIndexerPreservesBinaryPatchUnavailabilityAcrossSnapshotRoundTrip(t *testing.T) {
	repository := initializeGitEvidenceRepository(t)
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	createdAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	session := model.Session{
		ID: "git-binary", AgentType: "git-test", CWD: repository,
		CreatedAt: createdAt, UpdatedAt: createdAt.Add(time.Second),
	}
	head := gitEvidenceCommand(t, repository, "rev-parse", "HEAD")
	reader := &gitEvidenceReader{session: session, envelope: gitEvidenceEnvelope(session, repository, head, "f", model.SessionLive)}
	index := New(database, []sessionreader.BaseSessionReader{reader})
	if err := index.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(repository, "binary.bin"), []byte{0}, 0o644); err != nil {
		t.Fatal(err)
	}
	session.UpdatedAt = session.UpdatedAt.Add(time.Second)
	reader.session = session
	reader.envelope = gitEvidenceEnvelope(session, repository, head, "1", model.SessionFinalized)
	if err := index.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	evidence, ok, err := database.SessionGitEvidenceEnvelope(session.AgentType, session.ID)
	if err != nil || !ok || len(evidence.Repositories) != 1 {
		t.Fatalf("binary evidence ok=%v repositories=%d err=%v", ok, len(evidence.Repositories), err)
	}
	files := evidence.Repositories[0].Files
	if len(files) != 1 || !files[0].Binary || files[0].PatchAssessment.ReasonCode != model.ReasonBinaryPatchUnavailable {
		t.Fatalf("binary derived file = %+v", files)
	}
}

func TestIndexerDiscardsCaptureWhenAuthoritativeSourceChanges(t *testing.T) {
	repository := initializeGitEvidenceRepository(t)
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	createdAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	session := model.Session{
		ID: "git-source-race", AgentType: "git-test", CWD: repository,
		CreatedAt: createdAt, UpdatedAt: createdAt.Add(time.Second),
	}
	head := gitEvidenceCommand(t, repository, "rev-parse", "HEAD")
	reader := &gitEvidenceReader{
		session:         session,
		envelope:        gitEvidenceEnvelope(session, repository, head, "d", model.SessionLive),
		recheckEnvelope: gitEvidenceEnvelope(session, repository, head, "e", model.SessionLive),
	}
	index := New(database, []sessionreader.BaseSessionReader{reader})
	if err := index.RunOnce(t.Context()); err == nil {
		t.Fatal("expected source revision race to fail the indexing attempt")
	}

	var snapshots int
	if err := database.Conn().QueryRow(`SELECT COUNT(*) FROM session_git_snapshots`).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if snapshots != 0 {
		t.Fatalf("published snapshots after source race = %d", snapshots)
	}
	if _, exists, err := database.GetWatermark(session.AgentType, session.ID); err != nil || exists {
		t.Fatalf("watermark exists=%v err=%v after source race", exists, err)
	}
}

func initializeGitEvidenceRepository(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	repository := t.TempDir()
	gitEvidenceCommand(t, repository, "init", "-q")
	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitEvidenceCommand(t, repository, "add", "--", "tracked.txt")
	gitEvidenceCommand(t, repository, "-c", "user.name=Session Insight", "-c", "user.email=session-insight@example.invalid", "commit", "-q", "-m", "initial")
	return repository
}

func gitEvidenceCommand(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git command failed: %v (%s)", err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output))
}

func gitEvidenceEnvelope(session model.Session, repository, head, digestSeed string, state model.SessionFinalizationState) *model.IndexSnapshotEnvelope {
	digest := strings.Repeat(digestSeed, 64)
	revision := "sha256:" + digest
	recordedAt := session.UpdatedAt
	exactString := func(value string) model.GitFact[string] {
		return model.GitFact[string]{
			Value: value, Assessment: model.ExactGitEvidence(), Source: model.GitSourceAgentRecorded,
			RecordedAt: &recordedAt, SourceRevision: revision,
		}
	}
	missing := model.NonExactGitEvidence(model.GitEvidenceMissing, model.ReasonAgentGitFactMissing)
	finalization := model.SessionFinalizationEvidence{State: state}
	switch state {
	case model.SessionLive:
		finalization.Assessment = model.ExactSessionEvidence()
		finalization.SignalRecordedAt = &recordedAt
		finalization.SignalAssessment = model.ExactSessionEvidence()
		finalization.SignalKind = model.SessionSignalLive
	case model.SessionFinalized:
		finalization.Assessment = model.ExactSessionEvidence()
		finalization.SignalRecordedAt = &recordedAt
		finalization.SignalAssessment = model.ExactSessionEvidence()
		finalization.SignalKind = model.SessionSignalFinalized
	default:
		finalization.Assessment = model.NonExactSessionEvidence(model.SessionEvidenceMissing, model.ReasonSessionStateNotRecorded)
		finalization.SignalKind = model.SessionSignalNone
		finalization.SignalAssessment = model.NonExactSessionEvidence(model.SessionEvidenceMissing, model.ReasonSessionStateNotRecorded)
	}
	return &model.IndexSnapshotEnvelope{
		Detail:       &model.SessionDetail{Session: session, Turns: []model.TurnVM{}},
		RenderEvents: []model.RenderEvent{}, SourceRevision: revision,
		SourceFingerprint: model.SourceFingerprint{Algorithm: model.SourceFingerprintSHA256, Digest: digest, SizeBytes: 1},
		OriginGit: &model.SessionGitOrigin{
			RepositoryURL: model.GitFact[string]{Assessment: missing, Source: model.GitSourceAgentRecorded, SourceRevision: revision},
			WorktreePath:  exactString(repository), Branch: exactString("main"), HeadSHA: exactString(head),
			DirtyState: model.GitFact[model.GitDirtyState]{
				Value: model.GitDirtyClean, Assessment: model.ExactGitEvidence(), Source: model.GitSourceAgentRecorded,
				RecordedAt: &recordedAt, SourceRevision: revision,
			},
		},
		Finalization: finalization,
	}
}
