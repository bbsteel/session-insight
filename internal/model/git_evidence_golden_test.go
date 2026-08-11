package model

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

var updateGitEvidenceGolden = flag.Bool("update-git-evidence", false, "rewrite Git evidence golden fixtures")

const (
	testSHA1 = "1111111111111111111111111111111111111111"
	testSHA2 = "2222222222222222222222222222222222222222"
	testSHA3 = "3333333333333333333333333333333333333333"
)

func gitTestTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func gitTestTimePtr(value string) *time.Time {
	parsed := gitTestTime(value)
	return &parsed
}

func gitTestInt(value int) *int { return &value }

func exactStringFact(value, revision string) GitFact[string] {
	return GitFact[string]{
		Value: value, Assessment: ExactGitEvidence(), Source: GitSourceAgentRecorded,
		RecordedAt: gitTestTimePtr("2026-08-11T08:00:00Z"), SourceRevision: revision,
	}
}

func exactEvidenceLink() GitEvidenceLink {
	turn := 3
	return GitEvidenceLink{
		RootAgentType: "codex", RootSessionID: "session-root-1",
		SourceAgentType: "codex", SourceSessionID: "session-child-1",
		BackingAgentType: "codex", BackingSessionID: "session-child-1",
		InvocationID:   "codex:session-root-1:child:review",
		SourceRevision: "source-fingerprint-7", PositionsRevision: 42,
		EventID: "event-edit-1", ToolCallID: "call-edit-1", TurnIndex: &turn,
		RecordedAt: gitTestTimePtr("2026-08-11T08:01:30Z"),
		Assessment: ExactGitEvidence(),
	}
}

func exactRepository(entryKey string) GitRepositoryBinding {
	return GitRepositoryBinding{
		RepositoryEntryKey: entryKey,
		WorktreeRoot:       "/workspace/project",
		CommonRootID:       "repo-common-4f0e",
		WorktreeID:         "worktree-a822",
		Branch:             "feat/git-evidence",
		HeadSHA:            testSHA2,
		Assessment:         ExactGitEvidence(),
	}
}

func snapshotSummary(id string, kind GitSnapshotKind, sha, revision, start, end string) *GitSnapshotSummary {
	return &GitSnapshotSummary{
		SnapshotID: id, Kind: kind, HeadSHA: sha, ManifestDigest: "sha256:manifest-" + id,
		SourceRevision: revision, CaptureStartedAt: gitTestTime(start), CaptureEndedAt: gitTestTime(end),
		Assessment: ExactGitEvidence(),
	}
}

func localIntervalGolden() *SessionGitEvidenceEnvelope {
	entryKey := "repo-entry-local-1"
	return &SessionGitEvidenceEnvelope{
		RootAgentType: "codex",
		RootSessionID: "session-root-1",
		Repositories: []SessionGitEvidence{
			{
				RootAgentType: "codex", RootSessionID: "session-root-1", RepositoryEntryKey: entryKey,
				Revision: 8, Assessment: ExactGitEvidence(), Repository: exactRepository(entryKey),
				Origin: &SessionGitOrigin{
					RepositoryURL: exactStringFact("https://github.com/acme/widgets.git", "source-fingerprint-7"),
					WorktreePath:  exactStringFact("/workspace/project", "source-fingerprint-7"),
					Branch:        exactStringFact("feat/git-evidence", "source-fingerprint-7"),
					HeadSHA:       exactStringFact(testSHA1, "source-fingerprint-7"),
					DirtyState: GitFact[GitDirtyState]{
						Value: GitDirtyClean, Assessment: ExactGitEvidence(), Source: GitSourceAgentRecorded,
						RecordedAt: gitTestTimePtr("2026-08-11T08:00:00Z"), SourceRevision: "source-fingerprint-7",
					},
				},
				Baseline: snapshotSummary("snapshot-baseline-1", GitSnapshotBaseline, testSHA1, "source-fingerprint-7", "2026-08-11T08:00:00Z", "2026-08-11T08:00:01Z"),
				Final:    snapshotSummary("snapshot-final-1", GitSnapshotFinal, testSHA2, "source-fingerprint-9", "2026-08-11T08:10:00Z", "2026-08-11T08:10:02Z"),
				Files: []GitFileChange{
					{
						Ordinal: 0, Key: "worktree:sha256:file-1", Layer: GitFileLayerWorktree,
						DisplayPath: "internal/model/example.go", PathEncoding: GitPathUTF8,
						Status: GitFileModified, OldMode: "100644", NewMode: "100644",
						Additions: gitTestInt(4), Deletions: gitTestInt(1),
						StatusAssessment: ExactGitEvidence(), PatchAssessment: ExactGitEvidence(),
						Evidence: []GitEvidenceLink{exactEvidenceLink()},
					},
				},
				CandidateCommits: []GitCandidateCommit{},
				ChangeRequests:   []SessionChangeRequestLink{},
				Authority:        GitAuthorityLocalInterval,
				GeneratedAt:      gitTestTime("2026-08-11T08:10:03Z"),
			},
		},
	}
}

func githubIdentity() ChangeRequestIdentity {
	repo := &HostedRepositoryIdentity{HostID: "host-github-public", ImmutableID: "R_kgDOExample", Slug: "acme/widgets"}
	return ChangeRequestIdentity{
		Provider: ChangeProviderGitHub, HostID: repo.HostID, TargetRepository: repo, ProviderObjectID: "PR_kwDOExample42",
	}
}

func hostedExclusiveGolden() *SessionGitEvidenceEnvelope {
	entryKey := "repo-entry-hosted-1"
	contentKey := ContentVersionKey("github:PR_kwDOExample42:manifest-7")
	link := SessionChangeRequestLink{
		Ordinal: 0, LinkID: "cr-link-1",
		RootAgentType: "codex", RootSessionID: "session-root-1",
		SourceAgentType: "codex", SourceSessionID: "session-root-1", CollaborationRevision: 3,
		RepositoryEntryKey: entryKey, Change: githubIdentity(), ContentVersionKey: contentKey,
		Relationship: ChangeRelationshipExclusive, Method: ChangeLinkExplicit,
		Assessment: ExactGitEvidence(), ConfirmationSource: ChangeConfirmationUser,
		ConfirmationRevision: "confirmation-1", Evidence: []GitEvidenceLink{},
	}
	return &SessionGitEvidenceEnvelope{
		RootAgentType: "codex", RootSessionID: "session-root-1",
		Repositories: []SessionGitEvidence{
			{
				RootAgentType: "codex", RootSessionID: "session-root-1", RepositoryEntryKey: entryKey,
				Revision: 12, Assessment: ExactGitEvidence(), Repository: exactRepository(entryKey),
				Files: []GitFileChange{
					{
						Ordinal: 0, Key: "hosted:sha256:rename-1", Layer: GitFileLayerHosted,
						DisplayPath: "internal/new.go", OldDisplayPath: "internal/old.go", PathEncoding: GitPathUTF8,
						Status: GitFileRenamed, OldMode: "100644", NewMode: "100644",
						StatusAssessment: ExactGitEvidence(), PatchAssessment: ExactGitEvidence(),
						Evidence: []GitEvidenceLink{},
					},
				},
				CandidateCommits: []GitCandidateCommit{},
				ChangeRequests:   []SessionChangeRequestLink{link},
				Authority:        GitAuthorityHostedChange,
				AuthoritySelection: &ChangeRequestAuthoritySelection{
					LinkID: link.LinkID, ContentVersionKey: contentKey,
					RootAgentType: "codex", RootSessionID: "session-root-1", RepositoryEntryKey: entryKey,
					Coverage: ChangeCoverageCompleteDelivery,
				},
				GeneratedAt: gitTestTime("2026-08-11T09:00:00Z"),
			},
		},
	}
}

func hostedSnapshotGolden() *ChangeRequestSnapshot {
	return &ChangeRequestSnapshot{
		SnapshotID: "cr-snapshot-1", Identity: githubIdentity(),
		Content: ChangeRequestContentVersion{
			Key:        ContentVersionKey("github:PR_kwDOExample42:manifest-7"),
			BaseRefSHA: testSHA1, DiffBaseSHA: testSHA1, HeadSHA: testSHA2,
			FileManifestDigest: "sha256:manifest-7",
		},
		MetadataRevision: "metadata-etag-4", Kind: ChangeRequestPullRequest, DisplayNumber: "42",
		LifecycleState: ChangeLifecycleOpen, Title: "Add Git evidence", WebURL: "https://github.com/acme/widgets/pull/42",
		SourceRepository: &HostedRepositoryIdentity{HostID: "host-github-public", ImmutableID: "R_kgDOFork", Slug: "contributor/widgets"},
		SourceRef:        "feat/git-evidence", TargetRef: "main",
		Files: []GitFileChange{
			{
				Ordinal: 0, Key: "hosted:sha256:rename-1", Layer: GitFileLayerHosted,
				DisplayPath: "internal/new.go", OldDisplayPath: "internal/old.go", PathEncoding: GitPathUTF8,
				Status: GitFileRenamed, OldMode: "100644", NewMode: "100644",
				Additions: gitTestInt(8), Deletions: gitTestInt(2),
				StatusAssessment: ExactGitEvidence(), PatchAssessment: ExactGitEvidence(), Evidence: []GitEvidenceLink{},
			},
		},
		Commits: []GitCandidateCommit{
			{
				Ordinal: 0, SHA: testSHA3, Subject: "Add provider-neutral contracts", AuthorName: "Example Contributor",
				AuthoredAt: gitTestTimePtr("2026-08-11T08:30:00Z"), CommittedAt: gitTestTimePtr("2026-08-11T08:31:00Z"),
				Relation: GitCommitChangeMembership, Assessment: ExactGitEvidence(), Evidence: []GitEvidenceLink{},
			},
		},
		Completeness: ChangeRequestCompleteness{
			Metadata: ExactGitEvidence(), FileSet: ExactGitEvidence(), Patches: ExactGitEvidence(),
			Modes: ExactGitEvidence(), Commits: ExactGitEvidence(),
		},
		ETag: "etag-4", FetchedAt: gitTestTime("2026-08-11T08:32:00Z"),
	}
}

type gitGoldenCase struct {
	name     string
	build    func() any
	newValue func() any
	validate func(any) GitValidation
}

func gitGoldenCases() []gitGoldenCase {
	return []gitGoldenCase{
		{
			name: "local-interval", build: func() any { return localIntervalGolden() },
			newValue: func() any { return &SessionGitEvidenceEnvelope{} },
			validate: func(value any) GitValidation {
				return ValidateSessionGitEvidenceEnvelope(value.(*SessionGitEvidenceEnvelope))
			},
		},
		{
			name: "hosted-exclusive", build: func() any { return hostedExclusiveGolden() },
			newValue: func() any { return &SessionGitEvidenceEnvelope{} },
			validate: func(value any) GitValidation {
				return ValidateSessionGitEvidenceEnvelope(value.(*SessionGitEvidenceEnvelope))
			},
		},
		{
			name: "hosted-snapshot", build: func() any { return hostedSnapshotGolden() },
			newValue: func() any { return &ChangeRequestSnapshot{} },
			validate: func(value any) GitValidation { return ValidateChangeRequestSnapshot(value.(*ChangeRequestSnapshot)) },
		},
	}
}

func marshalGitGolden(value any) []byte {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		panic(err)
	}
	return buffer.Bytes()
}

func TestGitEvidenceGoldenSerialization(t *testing.T) {
	for _, tc := range gitGoldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			value := tc.build()
			if validation := tc.validate(value); !validation.OK() {
				t.Fatalf("builder violates contract: %+v", validation.Issues)
			}
			want := marshalGitGolden(value)
			path := filepath.Join("testdata", "git-evidence", tc.name+".json")
			if *updateGitEvidenceGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, want, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with -update-git-evidence to create): %v", err)
			}
			if !bytes.Equal(raw, want) {
				t.Fatalf("golden drift: %s", path)
			}
			round := tc.newValue()
			if err := json.Unmarshal(raw, round); err != nil {
				t.Fatalf("unmarshal golden: %v", err)
			}
			if !reflect.DeepEqual(round, value) {
				t.Fatalf("round-trip mismatch:\n got %#v\nwant %#v", round, value)
			}
			if validation := tc.validate(round); !validation.OK() {
				t.Fatalf("round-trip violates contract: %+v", validation.Issues)
			}
		})
	}
}

func TestGitEvidenceEmptyCollectionsSerializeAsArrays(t *testing.T) {
	envelope := &SessionGitEvidenceEnvelope{
		RootAgentType: "codex", RootSessionID: "empty-session", Repositories: []SessionGitEvidence{},
	}
	raw := marshalGitGolden(envelope)
	if !bytes.Contains(raw, []byte(`"repositories": []`)) {
		t.Fatalf("repositories must serialize as [], got:\n%s", raw)
	}
	assessment := ExactGitEvidence()
	raw = marshalGitGolden(assessment)
	if !bytes.Contains(raw, []byte(`"reasons": []`)) {
		t.Fatalf("reasons must serialize as [], got:\n%s", raw)
	}
}
