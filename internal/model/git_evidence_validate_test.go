package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGitEvidenceEnumsAreClosedAndIndependent(t *testing.T) {
	for _, state := range []GitEvidenceState{GitEvidenceExact, GitEvidenceEstimated, GitEvidenceMissing, GitEvidenceUnavailable} {
		if !IsKnownGitEvidenceState(state) {
			t.Errorf("known evidence state %q rejected", state)
		}
	}
	if IsKnownGitEvidenceState("unsupported") {
		t.Fatal("GitEvidenceState must not inherit the Agent capability unsupported state")
	}
	if IsKnownGitEvidenceState("not_applicable") {
		t.Fatal("GitEvidenceState must remain independent from CapabilityState")
	}
	for _, provider := range []ChangeProviderKind{
		ChangeProviderGitHub, ChangeProviderGitLab, ChangeProviderGitea,
		ChangeProviderForgejo, ChangeProviderBitbucketCloud,
		ChangeProviderBitbucketDataCenter, ChangeProviderAzureDevOps,
		ChangeProviderGerrit, ChangeProviderGeneric,
	} {
		if !IsKnownChangeProviderKind(provider) {
			t.Errorf("known provider %q rejected", provider)
		}
	}
	if IsKnownChangeProviderKind("github_enterprise") {
		t.Fatal("unknown provider kind accepted")
	}
}

func TestValidateGitEvidenceAssessmentRequiresCompleteReasons(t *testing.T) {
	if validation := ValidateGitEvidenceAssessment(ExactGitEvidence()); !validation.OK() {
		t.Fatalf("exact assessment rejected: %+v", validation.Issues)
	}
	withoutArray := GitEvidenceAssessment{State: GitEvidenceEstimated, ReasonCode: ReasonCaptureRaced}
	if validation := ValidateGitEvidenceAssessment(withoutArray); !validation.Has(GitIssueInvalidAssessment) {
		t.Fatalf("nil reasons accepted: %+v", validation.Issues)
	}
	wrongPrimary := NonExactGitEvidence(GitEvidenceEstimated, ReasonCaptureRaced)
	wrongPrimary.Reasons[0] = ReasonSourceRevisionChanged
	if validation := ValidateGitEvidenceAssessment(wrongPrimary); !validation.Has(GitIssueInvalidAssessment) {
		t.Fatalf("reason ordering drift accepted: %+v", validation.Issues)
	}
	exactWithReason := NonExactGitEvidence(GitEvidenceExact, ReasonCaptureRaced)
	if validation := ValidateGitEvidenceAssessment(exactWithReason); !validation.Has(GitIssueInvalidAssessment) {
		t.Fatalf("exact assessment with degradation reason accepted: %+v", validation.Issues)
	}
}

func TestValidateChangeRequestReferenceRejectsSecretBearingURL(t *testing.T) {
	valid := ChangeRequestReference{
		Provider: ChangeProviderGitHub, DisplayOrigin: "https://github.com",
		TargetRepositorySlug: "acme/widgets", DisplayNumber: "42",
		NormalizedURL: "https://github.com/acme/widgets/pull/42",
	}
	if validation := ValidateChangeRequestReference(valid); !validation.OK() {
		t.Fatalf("valid reference rejected: %+v", validation.Issues)
	}
	valid.NormalizedURL += "?access_token=secret"
	if validation := ValidateChangeRequestReference(valid); !validation.Has(GitIssueInvalidURL) {
		t.Fatalf("secret-bearing URL accepted: %+v", validation.Issues)
	}
}

func TestValidateChangeRequestIdentitySeparatesGenericAndCanonical(t *testing.T) {
	generic := ChangeRequestIdentity{Provider: ChangeProviderGeneric, GenericOpaqueID: "generic-cr-1"}
	if validation := ValidateChangeRequestIdentity(generic); !validation.OK() {
		t.Fatalf("generic identity rejected: %+v", validation.Issues)
	}
	generic.ProviderObjectID = "42"
	if validation := ValidateChangeRequestIdentity(generic); !validation.Has(GitIssueInvalidIdentity) {
		t.Fatalf("generic identity fabricated provider fields: %+v", validation.Issues)
	}
	canonical := githubIdentity()
	canonical.TargetRepository.HostID = "different-host"
	if validation := ValidateChangeRequestIdentity(canonical); !validation.Has(GitIssueInvalidIdentity) {
		t.Fatalf("cross-host repository identity accepted: %+v", validation.Issues)
	}
}

func TestValidateSnapshotKeepsCompletenessDimensionsIndependent(t *testing.T) {
	snapshot := hostedSnapshotGolden()
	snapshot.Completeness.Patches = NonExactGitEvidence(GitEvidenceUnavailable, ReasonChangeRequestOverflow)
	snapshot.Files[0].PatchAssessment = NonExactGitEvidence(GitEvidenceUnavailable, ReasonChangeRequestOverflow)
	if validation := ValidateChangeRequestSnapshot(snapshot); !validation.OK() {
		t.Fatalf("dimensioned provider overflow rejected: %+v", validation.Issues)
	}
	if snapshot.Completeness.FileSet.State != GitEvidenceExact {
		t.Fatal("patch overflow must not degrade the exact file-set dimension")
	}
	snapshot.Files[0].Ordinal = 2
	if validation := ValidateChangeRequestSnapshot(snapshot); !validation.Has(GitIssueInvalidOrdinal) {
		t.Fatalf("unstable file ordinal accepted: %+v", validation.Issues)
	}
}

func TestValidateSnapshotRejectsUnfixedDerivedContentVersion(t *testing.T) {
	snapshot := hostedSnapshotGolden()
	snapshot.Content.NativeVersion = ""
	snapshot.Content.BaseRefSHA = ""
	if validation := ValidateChangeRequestSnapshot(snapshot); !validation.Has(GitIssueInvalidRevision) {
		t.Fatalf("derived content version without base SHA accepted: %+v", validation.Issues)
	}
}

func TestValidateSessionEvidenceRejectsAutomaticExclusiveLink(t *testing.T) {
	envelope := hostedExclusiveGolden()
	repository := &envelope.Repositories[0]
	repository.ChangeRequests[0].Method = ChangeLinkHeadSHA
	if validation := ValidateSessionGitEvidence(repository); !validation.Has(GitIssueInvalidLink) {
		t.Fatalf("automatic exclusive link accepted: %+v", validation.Issues)
	}
}

func TestBindRequestExposesOnlyOpaqueRepositoryEntryKey(t *testing.T) {
	request := ChangeRequestBindRequest{
		ChangeKey: "change-key-github-pr-42", RepositoryEntryKey: "repo-entry-hosted-1",
		ContentVersionKey: "github:PR_kwDOExample42:manifest-7",
		Relationship:      ChangeRelationshipExclusive,
		Confirmation: &ChangeRequestBindConfirmation{
			CompleteDelivery: true, ContentVersionKey: "github:PR_kwDOExample42:manifest-7",
		},
	}
	if validation := ValidateChangeRequestBindRequest(request); !validation.OK() {
		t.Fatalf("valid bind request rejected: %+v", validation.Issues)
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"root_agent_type", "root_session_id", "worktree", "common_root_id", "collaboration_revision", "provider_object_id", "target_repository"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("bind request exposes server-derived field %q: %s", forbidden, raw)
		}
	}
	request.Confirmation.CompleteDelivery = false
	if validation := ValidateChangeRequestBindRequest(request); !validation.Has(GitIssueInvalidLink) {
		t.Fatalf("unconfirmed exclusive request accepted: %+v", validation.Issues)
	}
}

func TestValidateSessionEvidenceRejectsCrossRepositoryAuthority(t *testing.T) {
	envelope := hostedExclusiveGolden()
	repository := &envelope.Repositories[0]
	repository.AuthoritySelection.RepositoryEntryKey = "repo-entry-other"
	if validation := ValidateSessionGitEvidence(repository); !validation.Has(GitIssueInvalidAuthority) {
		t.Fatalf("cross-repository authority accepted: %+v", validation.Issues)
	}
}

func TestValidateSessionEvidenceRejectsExactLocalRename(t *testing.T) {
	envelope := localIntervalGolden()
	repository := &envelope.Repositories[0]
	repository.Files[0].Status = GitFileRenamed
	repository.Files[0].OldDisplayPath = "internal/model/old.go"
	if validation := ValidateSessionGitEvidence(repository); !validation.Has(GitIssueInvalidFile) {
		t.Fatalf("exact local rename accepted: %+v", validation.Issues)
	}
}

func TestValidateSessionEvidenceRequiresDistinctPositionRevision(t *testing.T) {
	envelope := localIntervalGolden()
	repository := &envelope.Repositories[0]
	repository.Files[0].Evidence[0].PositionsRevision = 0
	if validation := ValidateSessionGitEvidence(repository); !validation.Has(GitIssueInvalidRevision) {
		t.Fatalf("anchor without positions revision accepted: %+v", validation.Issues)
	}
}

func TestValidateChangeRequestLinkRejectsCrossRootEvidenceAnchor(t *testing.T) {
	link := hostedExclusiveGolden().Repositories[0].ChangeRequests[0]
	anchor := exactEvidenceLink()
	anchor.RootSessionID = "different-root"
	link.Evidence = []GitEvidenceLink{anchor}
	if validation := ValidateSessionChangeRequestLink(
		link, link.RootAgentType, link.RootSessionID, link.RepositoryEntryKey,
	); !validation.Has(GitIssueInvalidLink) {
		t.Fatalf("cross-root Change Request anchor accepted: %+v", validation.Issues)
	}
}

func TestValidateSessionEvidenceAllowsStableChangeLinkOrdinalGap(t *testing.T) {
	envelope := hostedExclusiveGolden()
	repository := &envelope.Repositories[0]
	repository.ChangeRequests[0].Ordinal = 4
	if validation := ValidateSessionGitEvidence(repository); !validation.OK() {
		t.Fatalf("stable Change Request ordinal gap rejected: %+v", validation.Issues)
	}
}

func TestValidateEnvelopeRejectsNullAndCrossRootRepositories(t *testing.T) {
	nilRepositories := &SessionGitEvidenceEnvelope{
		RootAgentType: "codex", RootSessionID: "session-root-1", Revision: 1,
		Assessment:  NonExactGitEvidence(GitEvidenceUnavailable, ReasonBaselineNotCaptured),
		GeneratedAt: gitTestTime("2026-08-11T08:00:00Z"),
	}
	if validation := ValidateSessionGitEvidenceEnvelope(nilRepositories); !validation.Has(GitIssueInvalidIdentity) {
		t.Fatalf("nil repositories accepted: %+v", validation.Issues)
	}
	envelope := localIntervalGolden()
	envelope.Repositories[0].RootSessionID = "different-root"
	if validation := ValidateSessionGitEvidenceEnvelope(envelope); !validation.Has(GitIssueInvalidIdentity) {
		t.Fatalf("cross-root repository accepted: %+v", validation.Issues)
	}
}

func TestValidateEnvelopeRequiresHonestEmptyAndAggregateState(t *testing.T) {
	empty := &SessionGitEvidenceEnvelope{
		RootAgentType: "codex", RootSessionID: "session-root-1", Revision: 1,
		Assessment: ExactGitEvidence(), GeneratedAt: gitTestTime("2026-08-11T08:00:00Z"),
		Repositories: []SessionGitEvidence{},
	}
	if validation := ValidateSessionGitEvidenceEnvelope(empty); !validation.Has(GitIssueInvalidAssessment) {
		t.Fatalf("exact empty envelope accepted: %+v", validation.Issues)
	}
	empty.Assessment = NonExactGitEvidence(GitEvidenceUnavailable, ReasonNotAGitRepository)
	if validation := ValidateSessionGitEvidenceEnvelope(empty); !validation.OK() {
		t.Fatalf("honest unavailable empty envelope rejected: %+v", validation.Issues)
	}

	envelope := localIntervalGolden()
	envelope.Repositories[0].Provisional = true
	if validation := ValidateSessionGitEvidenceEnvelope(envelope); !validation.Has(GitIssueInvalidAssessment) {
		t.Fatalf("unpropagated provisional state accepted: %+v", validation.Issues)
	}
	envelope.Provisional = true
	envelope.Repositories[0].Stale = true
	if validation := ValidateSessionGitEvidenceEnvelope(envelope); !validation.Has(GitIssueInvalidAssessment) {
		t.Fatalf("unpropagated stale state accepted: %+v", validation.Issues)
	}
}

func TestValidateSessionEvidenceRejectsNullPersistedChildren(t *testing.T) {
	envelope := localIntervalGolden()
	repository := &envelope.Repositories[0]
	repository.CandidateCommits = nil
	repository.ChangeRequests = nil
	validation := ValidateSessionGitEvidence(repository)
	if !validation.Has(GitIssueInvalidFile) || !validation.Has(GitIssueInvalidLink) {
		t.Fatalf("null persisted child arrays accepted: %+v", validation.Issues)
	}
}
