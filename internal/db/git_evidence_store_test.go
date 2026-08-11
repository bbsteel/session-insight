package db

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestReplaceSessionGitEvidenceAtomicallyReplacesChildren(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertTestSession(t, database, "codex", "evidence-session")

	first := testSessionGitEvidence("evidence-session", "entry-1", "first.go")
	if err := database.ReplaceSessionGitEvidence(first); err != nil {
		t.Fatal(err)
	}
	second := testSessionGitEvidence("evidence-session", "entry-1", "second.go")
	second.Revision = 2
	if err := database.ReplaceSessionGitEvidence(second); err != nil {
		t.Fatal(err)
	}

	var revision, files, links int
	if err := database.Conn().QueryRow(`SELECT revision FROM session_git_evidence WHERE evidence_id='entry-1'`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if err := database.Conn().QueryRow(`SELECT COUNT(*) FROM session_git_files WHERE evidence_id='entry-1'`).Scan(&files); err != nil {
		t.Fatal(err)
	}
	if err := database.Conn().QueryRow(`SELECT COUNT(*) FROM session_git_evidence_links WHERE evidence_id='entry-1'`).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if revision != 2 || files != 1 || links != 1 {
		t.Fatalf("revision=%d files=%d links=%d", revision, files, links)
	}
	var path string
	if err := database.Conn().QueryRow(`SELECT display_path FROM session_git_files WHERE evidence_id='entry-1'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if path != "second.go" {
		t.Fatalf("stored path = %q", path)
	}
}

func TestReplaceSessionGitEvidenceRollbackPreservesPreviousResult(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertTestSession(t, database, "codex", "rollback-session")
	first := testSessionGitEvidence("rollback-session", "entry-rollback", "first.go")
	if err := database.ReplaceSessionGitEvidence(first); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn().Exec(`
		CREATE TRIGGER reject_git_file BEFORE INSERT ON session_git_files
		WHEN NEW.display_path = 'fail.go' BEGIN
			SELECT RAISE(ABORT, 'injected evidence failure');
		END`); err != nil {
		t.Fatal(err)
	}

	failed := testSessionGitEvidence("rollback-session", "entry-rollback", "fail.go")
	failed.Revision = 2
	if err := database.ReplaceSessionGitEvidence(failed); err == nil {
		t.Fatal("expected replacement failure")
	}
	var revision int
	var path string
	if err := database.Conn().QueryRow(`
		SELECT e.revision, f.display_path
		FROM session_git_evidence e
		JOIN session_git_files f ON f.evidence_id=e.evidence_id
		WHERE e.evidence_id='entry-rollback'`,
	).Scan(&revision, &path); err != nil {
		t.Fatal(err)
	}
	if revision != 1 || path != "first.go" {
		t.Fatalf("rollback retained revision=%d path=%q", revision, path)
	}
}

func TestReplaceSessionGitEvidenceKeepsOriginsForEveryRepositoryEntry(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertTestSession(t, database, "codex", "multi-repository")

	for i, item := range []struct {
		entry, path, remote, revision string
	}{
		{"entry-one", "one.go", "https://example.com/acme/one.git", "source-one"},
		{"entry-two", "two.go", "https://example.com/acme/two.git", "source-two"},
	} {
		evidence := testSessionGitEvidence("multi-repository", item.entry, item.path)
		evidence.Origin = testSessionGitOrigin(item.remote, item.revision)
		evidence.Revision = int64(i + 1)
		if err := database.ReplaceSessionGitEvidence(evidence); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := database.Conn().Query(`
		SELECT binding_id, json_extract(origin_json, '$.repository_url.value')
		FROM session_git_origins ORDER BY binding_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := []struct{ binding, remote string }{
		{"entry-one", "https://example.com/acme/one.git"},
		{"entry-two", "https://example.com/acme/two.git"},
	}
	for i, expected := range want {
		if !rows.Next() {
			t.Fatalf("origin row %d is missing", i)
		}
		var binding, remote string
		if err := rows.Scan(&binding, &remote); err != nil {
			t.Fatal(err)
		}
		if binding != expected.binding || remote != expected.remote {
			t.Fatalf("origin row %d = (%q,%q), want (%q,%q)", i, binding, remote, expected.binding, expected.remote)
		}
	}
	if rows.Next() {
		t.Fatal("unexpected extra origin row")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	withoutOrigin := testSessionGitEvidence("multi-repository", "entry-one", "one.go")
	withoutOrigin.Revision = 3
	if err := database.ReplaceSessionGitEvidence(withoutOrigin); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := database.Conn().QueryRow(`SELECT COUNT(*) FROM session_git_origins`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("origin rows after one entry lost its origin = %d, want 1", remaining)
	}
	var stale int
	if err := database.Conn().QueryRow(`
		SELECT COUNT(*) FROM session_git_origins WHERE binding_id = 'entry-one'`,
	).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Fatal("replacement without origin retained the previous origin row")
	}
}

func TestSourceContentDedupeSharedReferenceAndFinalGC(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertTestSession(t, database, "codex", "blob-session")
	for _, entry := range []string{"blob-entry-1", "blob-entry-2"} {
		if err := database.ReplaceSessionGitEvidence(testSessionGitEvidence("blob-session", entry, entry+".go")); err != nil {
			t.Fatal(err)
		}
	}
	owner1 := SourceContentOwner{EvidenceID: "blob-entry-1", PathKey: "path-1", Purpose: "patch"}
	owner2 := SourceContentOwner{EvidenceID: "blob-entry-2", PathKey: "path-2", Purpose: "patch"}
	sha1, err := database.PutSourceContent(owner1, []byte("shared content"), DefaultSourceContentQuota)
	if err != nil {
		t.Fatal(err)
	}
	sha2, err := database.PutSourceContent(owner2, []byte("shared content"), DefaultSourceContentQuota)
	if err != nil {
		t.Fatal(err)
	}
	if sha1 != sha2 {
		t.Fatalf("dedupe hashes differ: %s %s", sha1, sha2)
	}
	assertTableCount(t, database, "source_content_blobs", 1)
	assertTableCount(t, database, "source_content_blob_refs", 2)
	if err := database.DeleteSourceContentRef(owner1); err != nil {
		t.Fatal(err)
	}
	assertTableCount(t, database, "source_content_blobs", 1)
	if err := database.DeleteSourceContentRef(owner2); err != nil {
		t.Fatal(err)
	}
	assertTableCount(t, database, "source_content_blobs", 0)
}

func TestSourceContentQuotaRollsBackReferenceAndBlob(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertTestSession(t, database, "codex", "quota-session")
	if err := database.ReplaceSessionGitEvidence(testSessionGitEvidence("quota-session", "quota-entry", "quota.go")); err != nil {
		t.Fatal(err)
	}
	quota := SourceContentQuota{MaxFileBytes: 10, MaxSessionBytes: 5, MaxChangeRequestBytes: 10, MaxGlobalBytes: 20}
	if _, err := database.PutSourceContent(
		SourceContentOwner{EvidenceID: "quota-entry", PathKey: "one", Purpose: "patch"},
		[]byte("abc"), quota,
	); err != nil {
		t.Fatal(err)
	}
	_, err = database.PutSourceContent(
		SourceContentOwner{EvidenceID: "quota-entry", PathKey: "two", Purpose: "patch"},
		[]byte("def"), quota,
	)
	if !errors.Is(err, ErrSourceContentQuotaExceeded) {
		t.Fatalf("quota error = %v", err)
	}
	assertTableCount(t, database, "source_content_blobs", 1)
	assertTableCount(t, database, "source_content_blob_refs", 1)
}

func TestDeleteSessionDataCascadesGitContentAndTerminalBinding(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertTestSession(t, database, "codex", "delete-session")
	if _, err := database.Conn().Exec(`
		INSERT INTO terminal_bindings(
			agent_type,session_id,terminal_id,terminal_name,confidence,focusable,state,launched_at,last_verified_at
		) VALUES ('codex','delete-session','term','Terminal',1,1,'ready','2026-08-11T00:00:00Z','2026-08-11T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := database.ReplaceSessionGitEvidence(testSessionGitEvidence("delete-session", "delete-entry", "delete.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := database.PutSourceContent(
		SourceContentOwner{EvidenceID: "delete-entry", PathKey: "delete", Purpose: "patch"},
		[]byte("private source"), DefaultSourceContentQuota,
	); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteSessionData("codex", "delete-session"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{
		"sessions", "terminal_bindings", "session_git_bindings", "session_git_evidence",
		"session_git_files", "session_git_evidence_links", "source_content_blob_refs", "source_content_blobs",
	} {
		assertTableCount(t, database, table, 0)
	}
}

func TestDeleteSessionDataRetainsBlobSharedByAnotherSession(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, item := range []struct{ session, entry string }{
		{"delete-shared-1", "delete-shared-entry-1"},
		{"delete-shared-2", "delete-shared-entry-2"},
	} {
		insertTestSession(t, database, "codex", item.session)
		if err := database.ReplaceSessionGitEvidence(testSessionGitEvidence(item.session, item.entry, item.session+".go")); err != nil {
			t.Fatal(err)
		}
		if _, err := database.PutSourceContent(
			SourceContentOwner{EvidenceID: item.entry, PathKey: item.session, Purpose: "patch"},
			[]byte("shared private source"), DefaultSourceContentQuota,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.DeleteSessionData("codex", "delete-shared-1"); err != nil {
		t.Fatal(err)
	}
	assertTableCount(t, database, "source_content_blobs", 1)
	assertTableCount(t, database, "source_content_blob_refs", 1)
	var remainingOwner string
	if err := database.Conn().QueryRow(`SELECT evidence_id FROM source_content_blob_refs`).Scan(&remainingOwner); err != nil {
		t.Fatal(err)
	}
	if remainingOwner != "delete-shared-entry-2" {
		t.Fatalf("remaining blob owner = %q", remainingOwner)
	}
}

func testSessionGitEvidence(sessionID, entryKey, path string) model.SessionGitEvidence {
	turn := 2
	return model.SessionGitEvidence{
		RootAgentType: "codex", RootSessionID: sessionID, RepositoryEntryKey: entryKey,
		Revision:   1,
		Assessment: model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonBaselineNotCaptured),
		Repository: model.GitRepositoryBinding{
			RepositoryEntryKey: entryKey, WorktreeRoot: "/workspace/project",
			CommonRootID: "common-root", WorktreeID: "worktree-id",
			Assessment: model.ExactGitEvidence(),
		},
		Files: []model.GitFileChange{{
			Ordinal: 0, Key: "worktree:" + path, Layer: model.GitFileLayerWorktree,
			DisplayPath: path, PathEncoding: model.GitPathUTF8, Status: model.GitFileModified,
			StatusAssessment: model.ExactGitEvidence(), PatchAssessment: model.ExactGitEvidence(),
			Evidence: []model.GitEvidenceLink{{
				RootAgentType: "codex", RootSessionID: sessionID,
				SourceAgentType: "codex", SourceSessionID: sessionID,
				SourceRevision: "source-revision", PositionsRevision: 1,
				ToolCallID: "tool-call", TurnIndex: &turn, Assessment: model.ExactGitEvidence(),
			}},
		}},
		CandidateCommits: []model.GitCandidateCommit{},
		ChangeRequests:   []model.SessionChangeRequestLink{},
		Authority:        model.GitAuthorityNone,
		GeneratedAt:      time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC),
	}
}

func testSessionGitOrigin(remote, revision string) *model.SessionGitOrigin {
	exactString := func(value string) model.GitFact[string] {
		return model.GitFact[string]{
			Value: value, Assessment: model.ExactGitEvidence(),
			Source: model.GitSourceAgentRecorded, SourceRevision: revision,
		}
	}
	return &model.SessionGitOrigin{
		RepositoryURL: exactString(remote),
		WorktreePath:  exactString("/workspace/project"),
		Branch:        exactString("feat/example"),
		HeadSHA:       exactString(strings.Repeat("a", 40)),
		DirtyState: model.GitFact[model.GitDirtyState]{
			Value: model.GitDirtyUnknown, Assessment: model.ExactGitEvidence(),
			Source: model.GitSourceAgentRecorded, SourceRevision: revision,
		},
	}
}

func insertTestSession(t *testing.T, database *DB, agentType, sessionID string) {
	t.Helper()
	if _, err := database.Conn().Exec(`
		INSERT INTO sessions(agent_type,id,created_at,updated_at)
		VALUES (?,?,'2026-08-11T00:00:00Z','2026-08-11T00:00:00Z')`,
		agentType, sessionID,
	); err != nil {
		t.Fatal(err)
	}
}

func assertTableCount(t *testing.T, database *DB, table string, want int) {
	t.Helper()
	if strings.ContainsAny(table, " ;") {
		t.Fatalf("unsafe table name %q", table)
	}
	var got int
	if err := database.Conn().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}
