package db

import (
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/model"
)

func TestStoreSessionChangeRequestLinkDerivesOrdinalAndSupportsOfflineReverse(t *testing.T) {
	database, changeKey, link := setupChangeRequestLinkTest(t, "link-root", "link-entry")
	defer database.Close()

	anchorTime := time.Date(2026, 8, 11, 6, 7, 8, 0, time.UTC)
	turn := 3
	link.Evidence = []model.GitEvidenceLink{{
		RootAgentType: "codex", RootSessionID: "link-root",
		SourceAgentType: "codex", SourceSessionID: "link-root",
		SourceRevision: "sha256:source-1", PositionsRevision: 1,
		TurnIndex: &turn, RecordedAt: &anchorTime, Assessment: model.ExactGitEvidence(),
	}}
	stored, err := database.StoreSessionChangeRequestLink(link)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Ordinal != 0 {
		t.Fatalf("first link ordinal = %d", stored.Ordinal)
	}
	if err := database.ReplaceSessionGitEvidence(testSessionGitEvidence("link-root", "link-entry-two", "link-two.go")); err != nil {
		t.Fatal(err)
	}
	secondRepository := link
	secondRepository.LinkID = "link-link-root-two"
	secondRepository.RepositoryEntryKey = "link-entry-two"
	secondRepository.Evidence = []model.GitEvidenceLink{}
	secondStored, err := database.StoreSessionChangeRequestLink(secondRepository)
	if err != nil {
		t.Fatal(err)
	}
	if secondStored.Ordinal != 0 {
		t.Fatalf("first link for second repository ordinal = %d", secondStored.Ordinal)
	}
	matches, err := database.ChangeRequestSessions(changeKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 || matches[0].RootSessionID != "link-root" || matches[0].Match != "linked" {
		t.Fatalf("reverse matches = %+v", matches)
	}
	assertTableCount(t, database, "session_change_request_evidence_links", 1)

	stored.Evidence = []model.GitEvidenceLink{}
	updated, err := database.StoreSessionChangeRequestLink(stored)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Ordinal != 0 {
		t.Fatalf("updated link ordinal = %d", updated.Ordinal)
	}
	assertTableCount(t, database, "session_change_request_evidence_links", 0)
}

func TestStoreSessionChangeRequestLinkCannotMoveAcrossRepositoryBindings(t *testing.T) {
	database, changeKey, link := setupChangeRequestLinkTest(t, "immutable-link-root", "immutable-entry-one")
	defer database.Close()
	stored, err := database.StoreSessionChangeRequestLink(link)
	if err != nil {
		t.Fatal(err)
	}

	if err := database.ReplaceSessionGitEvidence(testSessionGitEvidence(
		"immutable-link-root", "immutable-entry-two", "other.go",
	)); err != nil {
		t.Fatal(err)
	}
	moved := stored
	moved.RepositoryEntryKey = "immutable-entry-two"
	if _, err := database.StoreSessionChangeRequestLink(moved); err == nil {
		t.Fatal("existing Change Request link moved to another repository binding")
	}

	matches, err := database.ChangeRequestSessions(changeKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].RepositoryEntryKey != "immutable-entry-one" {
		t.Fatalf("stored link changed after rejected move: %+v", matches)
	}
}

func TestStoreSessionChangeRequestExclusiveRequiresCompleteFixedUserConfirmation(t *testing.T) {
	database, _, link := setupChangeRequestLinkTest(t, "exclusive-root", "exclusive-entry")
	defer database.Close()
	link.Relationship = model.ChangeRelationshipExclusive
	link.Method = model.ChangeLinkExplicit
	link.ConfirmationSource = model.ChangeConfirmationUser
	confirmation, err := CanonicalChangeRequestConfirmationRevision(link)
	if err != nil {
		t.Fatal(err)
	}
	link.ConfirmationRevision = confirmation
	if _, err := database.StoreSessionChangeRequestLink(link); err != nil {
		t.Fatal(err)
	}

	invalid := link
	invalid.LinkID = "exclusive-link-invalid-confirmation"
	invalid.ConfirmationRevision = "client-supplied"
	if _, err := database.StoreSessionChangeRequestLink(invalid); err == nil {
		t.Fatal("exclusive link accepted an unbound confirmation revision")
	}
	stale := link
	stale.LinkID = "exclusive-link-stale-collaboration"
	stale.CollaborationRevision++
	confirmation, err = CanonicalChangeRequestConfirmationRevision(stale)
	if err != nil {
		t.Fatal(err)
	}
	stale.ConfirmationRevision = confirmation
	if _, err := database.StoreSessionChangeRequestLink(stale); err == nil {
		t.Fatal("exclusive link accepted a stale collaboration revision")
	}
}

func TestNewChangeRequestContentVersionDemotesHostedAuthorityPendingReconfirmation(t *testing.T) {
	database, _, link := setupChangeRequestLinkTest(t, "reconfirm-root", "reconfirm-entry")
	defer database.Close()
	link.Relationship = model.ChangeRelationshipExclusive
	link.Method = model.ChangeLinkExplicit
	link.ConfirmationSource = model.ChangeConfirmationUser
	confirmation, err := CanonicalChangeRequestConfirmationRevision(link)
	if err != nil {
		t.Fatal(err)
	}
	link.ConfirmationRevision = confirmation
	stored, err := database.StoreSessionChangeRequestLink(link)
	if err != nil {
		t.Fatal(err)
	}

	evidence := testSessionGitEvidence("reconfirm-root", "reconfirm-entry", "delivery.go")
	evidence.Revision = 2
	evidence.Assessment = model.ExactGitEvidence()
	evidence.ChangeRequests = []model.SessionChangeRequestLink{stored}
	evidence.Authority = model.GitAuthorityHostedChange
	evidence.AuthoritySelection = &model.ChangeRequestAuthoritySelection{
		LinkID: stored.LinkID, ContentVersionKey: stored.ContentVersionKey,
		RootAgentType: "codex", RootSessionID: "reconfirm-root",
		RepositoryEntryKey: "reconfirm-entry", Coverage: model.ChangeCoverageCompleteDelivery,
	}
	if err := database.ReplaceSessionGitEvidence(evidence); err != nil {
		t.Fatal(err)
	}

	newer := testChangeRequestSnapshotWrite()
	newer.Snapshot.SnapshotID = "snapshot-pr-42-reconfirmation"
	newer.Snapshot.Content.Key = "github:provider-pr-42:content-reconfirmation"
	newer.Snapshot.Content.HeadSHA = testChangeCommit
	newer.Snapshot.FetchedAt = newer.Snapshot.FetchedAt.Add(time.Minute)
	newer.SyncStartedAt = newer.SyncStartedAt.Add(time.Minute)
	if _, err := database.StoreChangeRequestSnapshot(newer); err != nil {
		t.Fatal(err)
	}

	var linkState, linkReason, authority string
	var stale int
	var revision int64
	if err := database.Conn().QueryRow(`
		SELECT state, reason_code FROM session_change_requests WHERE link_id = ?`, stored.LinkID,
	).Scan(&linkState, &linkReason); err != nil {
		t.Fatal(err)
	}
	if err := database.Conn().QueryRow(`
		SELECT revision, authority, stale FROM session_git_evidence WHERE evidence_id = ?`,
		CanonicalSessionRepositoryBindingID("codex", "reconfirm-root", "reconfirm-entry"),
	).Scan(&revision, &authority, &stale); err != nil {
		t.Fatal(err)
	}
	if linkState != string(model.GitEvidenceEstimated) || linkReason != string(model.ReasonChangeRequestPendingReconfirmation) {
		t.Fatalf("link state=%q reason=%q", linkState, linkReason)
	}
	if revision != 3 || authority != string(model.GitAuthorityNone) || stale != 1 {
		t.Fatalf("evidence revision=%d authority=%q stale=%d", revision, authority, stale)
	}
}

func TestNewChangeRequestVersionDoesNotDemoteCommitGraphAuthority(t *testing.T) {
	database, _, link := setupChangeRequestLinkTest(t, "local-authority-root", "local-authority-entry")
	defer database.Close()
	stored := storeExclusiveChangeRequestLink(t, database, link)
	evidence := testSessionGitEvidence("local-authority-root", "local-authority-entry", "delivery.go")
	evidence.Revision = 2
	evidence.Assessment = model.ExactGitEvidence()
	evidence.ChangeRequests = []model.SessionChangeRequestLink{stored}
	evidence.CandidateCommits = []model.GitCandidateCommit{{
		Ordinal: 0, SHA: testChangeCommit, Subject: "Candidate delivery",
		Relation:   model.GitCommitChangeMembership,
		Assessment: model.ExactGitEvidence(), Evidence: []model.GitEvidenceLink{},
	}}
	evidence.Authority = model.GitAuthorityCommitGraph
	if err := database.ReplaceSessionGitEvidence(evidence); err != nil {
		t.Fatal(err)
	}

	newer := testChangeRequestSnapshotWrite()
	newer.Snapshot.SnapshotID = "snapshot-pr-42-local-authority"
	newer.Snapshot.Content.Key = "github:provider-pr-42:content-local-authority"
	newer.Snapshot.Content.HeadSHA = testChangeCommit
	newer.Snapshot.FetchedAt = newer.Snapshot.FetchedAt.Add(time.Minute)
	newer.SyncStartedAt = newer.SyncStartedAt.Add(time.Minute)
	if _, err := database.StoreChangeRequestSnapshot(newer); err != nil {
		t.Fatal(err)
	}
	var authority string
	var stale int
	if err := database.Conn().QueryRow(`
		SELECT authority, stale FROM session_git_evidence WHERE evidence_id = ?`,
		CanonicalSessionRepositoryBindingID("codex", "local-authority-root", "local-authority-entry"),
	).Scan(&authority, &stale); err != nil {
		t.Fatal(err)
	}
	if authority != string(model.GitAuthorityCommitGraph) || stale != 0 {
		t.Fatalf("local authority=%q stale=%d", authority, stale)
	}
}

func TestDeleteSelectedChangeRequestLinkDemotesHostedAuthority(t *testing.T) {
	database, _, link := setupChangeRequestLinkTest(t, "delete-selected-root", "delete-selected-entry")
	defer database.Close()
	stored := storeExclusiveChangeRequestLink(t, database, link)
	storeHostedChangeRequestAuthority(t, database, stored)
	deleted, err := database.DeleteSessionChangeRequestLink("codex", "delete-selected-root", stored.LinkID)
	if err != nil || !deleted {
		t.Fatalf("deleted=%v err=%v", deleted, err)
	}
	assertHostedAuthorityDemoted(t, database, "delete-selected-root", "delete-selected-entry")
}

func TestDeletedHostedPatchRequiresRecoveryAndReconfirmation(t *testing.T) {
	database, _, link := setupChangeRequestLinkTest(t, "purged-content-root", "purged-content-entry")
	defer database.Close()
	stored := storeExclusiveChangeRequestLink(t, database, link)
	storeHostedChangeRequestAuthority(t, database, stored)
	owner := SourceContentOwner{
		ChangeSnapshotID: "snapshot-pr-42", PathKey: "hosted:file-1", Purpose: "patch",
	}
	if err := database.DeleteSourceContentRef(owner); err != nil {
		t.Fatal(err)
	}
	assertHostedAuthorityDemoted(t, database, "purged-content-root", "purged-content-entry")
	var cacheState, cacheHeadState, cacheHeadReason string
	if err := database.Conn().QueryRow(`
		SELECT cache_state FROM change_request_snapshots WHERE snapshot_id='snapshot-pr-42'`,
	).Scan(&cacheState); err != nil {
		t.Fatal(err)
	}
	if cacheState != "content_deleted" {
		t.Fatalf("cache state after patch deletion = %q", cacheState)
	}
	if err := database.Conn().QueryRow(`
		SELECT state, reason_code FROM change_request_cache_heads WHERE snapshot_id='snapshot-pr-42'`,
	).Scan(&cacheHeadState, &cacheHeadReason); err != nil {
		t.Fatal(err)
	}
	if cacheHeadState != "stale" || cacheHeadReason != string(model.ReasonChangeRequestPartial) {
		t.Fatalf("cache head after patch deletion state=%q reason=%q", cacheHeadState, cacheHeadReason)
	}
	if _, err := database.StoreSessionChangeRequestLink(stored); err == nil {
		t.Fatal("exclusive link was reconfirmed without retained patch content")
	}

	restored := testChangeRequestSnapshotWrite()
	restored.Snapshot.FetchedAt = restored.Snapshot.FetchedAt.Add(time.Minute)
	restored.SyncStartedAt = restored.SyncStartedAt.Add(time.Minute)
	if _, err := database.StoreChangeRequestSnapshot(restored); err != nil {
		t.Fatal(err)
	}
	if err := database.Conn().QueryRow(`
		SELECT state, reason_code FROM change_request_cache_heads WHERE snapshot_id='snapshot-pr-42'`,
	).Scan(&cacheHeadState, &cacheHeadReason); err != nil {
		t.Fatal(err)
	}
	if cacheHeadState != "current" || cacheHeadReason != "" {
		t.Fatalf("restored cache head state=%q reason=%q", cacheHeadState, cacheHeadReason)
	}
	stored.Assessment = model.ExactGitEvidence()
	if _, err := database.StoreSessionChangeRequestLink(stored); err != nil {
		t.Fatalf("reconfirm restored fixed content: %v", err)
	}
	assertTableCount(t, database, "source_content_blob_refs", 1)
}

func TestCollaborationRevisionChangeInvalidatesHostedConfirmation(t *testing.T) {
	database, _, link := setupChangeRequestLinkTest(t, "revision-change-root", "revision-change-entry")
	defer database.Close()
	stored := storeExclusiveChangeRequestLink(t, database, link)
	storeHostedChangeRequestAuthority(t, database, stored)
	if err := database.ReplaceCollaborationGraph(testRootCollaborationGraph("revision-change-root", 7)); err != nil {
		t.Fatal(err)
	}
	var authority string
	if err := database.Conn().QueryRow(`
		SELECT authority FROM session_git_evidence WHERE evidence_id = ?`,
		CanonicalSessionRepositoryBindingID("codex", "revision-change-root", "revision-change-entry"),
	).Scan(&authority); err != nil {
		t.Fatal(err)
	}
	if authority != string(model.GitAuthorityHostedChange) {
		t.Fatalf("same collaboration revision changed authority to %q", authority)
	}
	if err := database.ReplaceCollaborationGraph(testRootCollaborationGraph("revision-change-root", 8)); err != nil {
		t.Fatal(err)
	}
	assertHostedAuthorityDemoted(t, database, "revision-change-root", "revision-change-entry")
	var state, reason string
	if err := database.Conn().QueryRow(`
		SELECT state, reason_code FROM session_change_requests WHERE link_id = ?`, stored.LinkID,
	).Scan(&state, &reason); err != nil {
		t.Fatal(err)
	}
	if state != string(model.GitEvidenceEstimated) || reason != string(model.ReasonChangeRequestPendingReconfirmation) {
		t.Fatalf("confirmation state=%q reason=%q", state, reason)
	}
}

func TestChangeRequestCandidateSessionsUsesIndexedSHAsAndExcludesLinkedRoot(t *testing.T) {
	database, changeKey, link := setupChangeRequestLinkTest(t, "candidate-linked", "candidate-linked-entry")
	defer database.Close()
	if _, err := database.StoreSessionChangeRequestLink(link); err != nil {
		t.Fatal(err)
	}

	insertTestSession(t, database, "codex", "candidate-unbound")
	if _, err := database.Conn().Exec(`
		INSERT INTO collaboration_roots(
			root_agent_type, root_session_id, revision, completeness_state
		) VALUES ('codex','candidate-unbound',7,'exact')`); err != nil {
		t.Fatal(err)
	}
	evidence := testSessionGitEvidence("candidate-unbound", "candidate-unbound-entry", "candidate.go")
	evidence.Repository.HeadSHA = testChangeHeadSHA
	if err := database.ReplaceSessionGitEvidence(evidence); err != nil {
		t.Fatal(err)
	}
	if err := database.StoreSessionHostedRepositoryBinding(
		"codex", "candidate-unbound", "candidate-unbound-entry",
		*testChangeRequestSnapshotWrite().Snapshot.SourceRepository,
	); err != nil {
		t.Fatal(err)
	}

	insertTestSession(t, database, "codex", "candidate-other-repository")
	other := testSessionGitEvidence("candidate-other-repository", "candidate-other-entry", "other.go")
	other.Repository.HeadSHA = testChangeHeadSHA
	if err := database.ReplaceSessionGitEvidence(other); err != nil {
		t.Fatal(err)
	}
	if err := database.StoreSessionHostedRepositoryBinding(
		"codex", "candidate-other-repository", "candidate-other-entry",
		*testChangeRequestSnapshotWrite().Snapshot.Identity.TargetRepository,
	); err != nil {
		t.Fatal(err)
	}

	candidates, err := database.ChangeRequestCandidateSessions(changeKey, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].RootSessionID != "candidate-unbound" || candidates[0].Match != "head_sha" {
		t.Fatalf("candidate sessions = %+v", candidates)
	}
}

func TestStoreSessionHostedRepositoryBindingCreatesApprovedRepositoryIdentity(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertTestSession(t, database, "codex", "remote-only-session")
	if err := database.ReplaceSessionGitEvidence(testSessionGitEvidence(
		"remote-only-session", "remote-only-entry", "remote.go",
	)); err != nil {
		t.Fatal(err)
	}
	insertChangeHost(t, database, "host-github-public", "github", "github.example")
	repository := model.HostedRepositoryIdentity{
		HostID: "host-github-public", ImmutableID: "repository-before-pr", Slug: "acme/widgets",
	}
	if err := database.StoreSessionHostedRepositoryBinding(
		"codex", "remote-only-session", "remote-only-entry", repository,
	); err != nil {
		t.Fatal(err)
	}
	assertTableCount(t, database, "hosted_repositories", 1)
	assertTableCount(t, database, "session_hosted_repository_bindings", 1)

	if _, err := database.Conn().Exec(`
		UPDATE change_hosts SET lifecycle = 'revoked', revoked_at = '2026-08-11T12:00:00Z'
		WHERE host_id = ?`, repository.HostID,
	); err != nil {
		t.Fatal(err)
	}
	other := repository
	other.ImmutableID = "repository-after-revoke"
	if err := database.StoreSessionHostedRepositoryBinding(
		"codex", "remote-only-session", "remote-only-entry", other,
	); err == nil {
		t.Fatal("revoked host accepted a new Session repository mapping")
	}
}

func TestDeleteSessionChangeRequestLinkKeepsCachedSnapshot(t *testing.T) {
	database, changeKey, link := setupChangeRequestLinkTest(t, "delete-link-root", "delete-link-entry")
	defer database.Close()
	stored, err := database.StoreSessionChangeRequestLink(link)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := database.DeleteSessionChangeRequestLink("codex", "delete-link-root", stored.LinkID)
	if err != nil || !deleted {
		t.Fatalf("deleted=%v err=%v", deleted, err)
	}
	matches, err := database.ChangeRequestSessions(changeKey)
	if err != nil || len(matches) != 0 {
		t.Fatalf("matches=%+v err=%v", matches, err)
	}
	assertTableCount(t, database, "change_request_snapshots", 1)
	assertTableCount(t, database, "change_request_aliases", 5)
}

func setupChangeRequestLinkTest(t *testing.T, sessionID, entryKey string) (*DB, string, model.SessionChangeRequestLink) {
	t.Helper()
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	insertTestSession(t, database, "codex", sessionID)
	if _, err := database.Conn().Exec(`
		INSERT INTO collaboration_roots(
			root_agent_type, root_session_id, revision, completeness_state
		) VALUES ('codex',?,7,'exact')`, sessionID); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.ReplaceSessionGitEvidence(testSessionGitEvidence(sessionID, entryKey, "link.go")); err != nil {
		database.Close()
		t.Fatal(err)
	}
	insertChangeHost(t, database, "host-github-public", "github", "github.example")
	write := testChangeRequestSnapshotWrite()
	changeKey, err := database.StoreChangeRequestSnapshot(write)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	link := model.SessionChangeRequestLink{
		LinkID:        "link-" + sessionID,
		RootAgentType: "codex", RootSessionID: sessionID,
		SourceAgentType: "codex", SourceSessionID: sessionID,
		CollaborationRevision: 7, RepositoryEntryKey: entryKey,
		Change: write.Snapshot.Identity, ContentVersionKey: write.Snapshot.Content.Key,
		Relationship: model.ChangeRelationshipContributing,
		Method:       model.ChangeLinkExplicit, Assessment: model.ExactGitEvidence(),
		ConfirmationSource: model.ChangeConfirmationNone,
		Evidence:           []model.GitEvidenceLink{},
	}
	return database, changeKey, link
}

func storeExclusiveChangeRequestLink(t *testing.T, database *DB, link model.SessionChangeRequestLink) model.SessionChangeRequestLink {
	t.Helper()
	link.Relationship = model.ChangeRelationshipExclusive
	link.Method = model.ChangeLinkExplicit
	link.ConfirmationSource = model.ChangeConfirmationUser
	confirmation, err := CanonicalChangeRequestConfirmationRevision(link)
	if err != nil {
		t.Fatal(err)
	}
	link.ConfirmationRevision = confirmation
	stored, err := database.StoreSessionChangeRequestLink(link)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func storeHostedChangeRequestAuthority(t *testing.T, database *DB, link model.SessionChangeRequestLink) {
	t.Helper()
	evidence := testSessionGitEvidence(link.RootSessionID, link.RepositoryEntryKey, "delivery.go")
	evidence.Revision = 2
	evidence.Assessment = model.ExactGitEvidence()
	evidence.ChangeRequests = []model.SessionChangeRequestLink{link}
	evidence.Authority = model.GitAuthorityHostedChange
	evidence.AuthoritySelection = &model.ChangeRequestAuthoritySelection{
		LinkID: link.LinkID, ContentVersionKey: link.ContentVersionKey,
		RootAgentType: link.RootAgentType, RootSessionID: link.RootSessionID,
		RepositoryEntryKey: link.RepositoryEntryKey, Coverage: model.ChangeCoverageCompleteDelivery,
	}
	if err := database.ReplaceSessionGitEvidence(evidence); err != nil {
		t.Fatal(err)
	}
}

func assertHostedAuthorityDemoted(t *testing.T, database *DB, sessionID, entryKey string) {
	t.Helper()
	var authority string
	var stale int
	if err := database.Conn().QueryRow(`
		SELECT authority, stale FROM session_git_evidence WHERE evidence_id = ?`,
		CanonicalSessionRepositoryBindingID("codex", sessionID, entryKey),
	).Scan(&authority, &stale); err != nil {
		t.Fatal(err)
	}
	if authority != string(model.GitAuthorityNone) || stale != 1 {
		t.Fatalf("authority=%q stale=%d", authority, stale)
	}
}

func testRootCollaborationGraph(sessionID string, revision int64) collaboration.CollaborationGraph {
	return collaboration.CollaborationGraph{
		RootAgentType: "codex", RootSessionID: sessionID, Revision: revision,
		Completeness: collaboration.ExactFact(),
		Invocations: []collaboration.AgentInvocation{{
			ID: collaboration.RootInvocationID("codex", sessionID), DisplayName: "Codex",
			AgentType: "codex", Status: collaboration.StatusCompleted,
			TimePrecision: collaboration.ExactFact(), ContentPrecision: collaboration.ExactFact(),
			SourceIdentity: collaboration.SourceIdentity{Kind: collaboration.IdentityRootSession, NativeID: sessionID},
		}},
	}
}
