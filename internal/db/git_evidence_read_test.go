package db

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestSessionGitEvidenceEnvelopeReconstructsExplicitArraysAndLinks(t *testing.T) {
	database, _, link := setupChangeRequestLinkTest(t, "read-envelope", "read-entry")
	defer database.Close()
	stored, err := database.StoreSessionChangeRequestLink(link)
	if err != nil {
		t.Fatal(err)
	}
	evidence := testSessionGitEvidence("read-envelope", "read-entry", "read.go")
	evidence.Origin = testSessionGitOrigin("https://github.example/acme/widgets", "source-revision")
	evidence.ChangeRequests = []model.SessionChangeRequestLink{stored}
	if err := database.ReplaceSessionGitEvidence(evidence); err != nil {
		t.Fatal(err)
	}

	envelope, ok, err := database.SessionGitEvidenceEnvelope("codex", "read-envelope")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || len(envelope.Repositories) != 1 {
		t.Fatalf("unexpected evidence envelope: ok=%v envelope=%+v", ok, envelope)
	}
	if validation := model.ValidateSessionGitEvidenceEnvelope(&envelope); !validation.OK() {
		t.Fatalf("reconstructed envelope is invalid: %+v", validation.Issues)
	}
	repository := envelope.Repositories[0]
	if repository.RepositoryEntryKey != "read-entry" || len(repository.Files) != 1 || len(repository.ChangeRequests) != 1 {
		t.Fatalf("reconstructed repository evidence is incomplete: %+v", repository)
	}
	if repository.Origin == nil || repository.Origin.RepositoryURL.Value != "https://github.example/acme/widgets" {
		t.Fatalf("origin was not reconstructed: %+v", repository.Origin)
	}
	if repository.CandidateCommits == nil || repository.Files[0].Evidence == nil || repository.ChangeRequests[0].Evidence == nil {
		t.Fatal("read contract emitted null arrays")
	}

	empty, ok, err := database.SessionGitEvidenceEnvelope("codex", "missing-session")
	if err != nil || ok || empty.Repositories == nil {
		t.Fatalf("missing evidence result: ok=%v err=%v envelope=%+v", ok, err, empty)
	}
}

func TestChangeRequestReadReturnsFixedSnapshotAndBoundedPatch(t *testing.T) {
	database, changeKey, _ := setupChangeRequestLinkTest(t, "read-change", "read-change-entry")
	defer database.Close()

	record, err := database.ChangeRequest(changeKey, "")
	if err != nil {
		t.Fatal(err)
	}
	if record.Snapshot == nil || record.Snapshot.Content.Key != testChangeRequestSnapshotWrite().Snapshot.Content.Key || record.CacheState != "current" {
		t.Fatalf("unexpected Change Request record: %+v", record)
	}
	if validation := model.ValidateChangeRequestSnapshot(record.Snapshot); !validation.OK() {
		t.Fatalf("reconstructed Change Request snapshot is invalid: %+v", validation.Issues)
	}
	if record.Snapshot.Files == nil || record.Snapshot.Commits == nil || record.Aliases == nil {
		t.Fatal("Change Request record emitted null arrays")
	}
	patch, err := database.ChangeRequestPatch(changeKey, record.Snapshot.Content.Key, record.Snapshot.Files[0].Key, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if string(patch) != "@@ -1 +1 @@\n-old\n+new\n" {
		t.Fatalf("patch = %q", patch)
	}
	if _, err := database.ChangeRequestPatch(changeKey, record.Snapshot.Content.Key, record.Snapshot.Files[0].Key, 4); !errors.Is(err, ErrSourceContentReadLimit) {
		t.Fatalf("small read cap error = %v", err)
	}
	if _, err := database.ChangeRequestPatch(changeKey, record.Snapshot.Content.Key, "hosted:missing", 1<<20); !errors.Is(err, ErrSourceContentNotFound) {
		t.Fatalf("unknown file-key error = %v", err)
	}
}

func TestExclusiveLinkImmediatelySelectsHostedAuthority(t *testing.T) {
	database, _, link := setupChangeRequestLinkTest(t, "read-exclusive", "read-exclusive-entry")
	defer database.Close()
	stored := storeExclusiveChangeRequestLink(t, database, link)

	envelope, ok, err := database.SessionGitEvidenceEnvelope("codex", "read-exclusive")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || len(envelope.Repositories) != 1 {
		t.Fatalf("unexpected evidence envelope: ok=%v envelope=%+v", ok, envelope)
	}
	evidence := envelope.Repositories[0]
	if evidence.Authority != model.GitAuthorityHostedChange || evidence.AuthoritySelection == nil ||
		evidence.AuthoritySelection.LinkID != stored.LinkID || evidence.Assessment.State != model.GitEvidenceExact {
		t.Fatalf("exclusive link did not select hosted authority: %+v", evidence)
	}
}

func TestChangeRequestReadKeepsGenericIdentityOffline(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	reference := model.ChangeRequestReference{
		Provider: model.ChangeProviderGeneric, DisplayOrigin: "https://code.example",
		NormalizedURL: "https://code.example/team/repo/reviews/7",
	}
	digest := sha256.Sum256([]byte(reference.NormalizedURL))
	identity := model.ChangeRequestIdentity{
		Provider: model.ChangeProviderGeneric, GenericOpaqueID: "generic-" + hex.EncodeToString(digest[:]),
	}
	changeKey, err := database.StoreGenericChangeRequest(reference, identity)
	if err != nil {
		t.Fatal(err)
	}
	record, err := database.ChangeRequest(changeKey, "")
	if err != nil {
		t.Fatal(err)
	}
	if record.Snapshot != nil || record.CacheState != "offline" || record.Identity != identity ||
		!reflect.DeepEqual(record.Aliases, []string{reference.NormalizedURL}) {
		t.Fatalf("unexpected generic Change Request record: %+v", record)
	}
}
