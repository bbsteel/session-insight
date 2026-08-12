package changehost

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func supportedCapabilities() ProviderCapabilities {
	operations := make(map[CapabilityID]CapabilityDeclaration, len(CapabilityIDs()))
	for _, id := range CapabilityIDs() {
		operations[id] = CapabilityDeclaration{State: CapabilitySupported}
	}
	return ProviderCapabilities{
		Operations: operations,
		HostModes:  []HostMode{HostModePublicSaaS},
		AuthenticationModes: []AuthenticationMode{
			AuthAnonymous, AuthTokenEnvironment, AuthOSKeyring, AuthProviderCLI,
		},
		Limits: ProviderLimits{MaximumFiles: 3000, MaximumCommits: 250},
	}
}

func githubHost() HostIdentity {
	return HostIdentity{
		Key: "host-github-public", Provider: model.ChangeProviderGitHub,
		DisplayOrigin:   "https://github.com",
		EndpointOrigins: []string{"https://github.com", "https://api.github.com"},
	}
}

func TestValidateCapabilitiesRequiresFrozenOperationSet(t *testing.T) {
	capabilities := supportedCapabilities()
	if errs := ValidateCapabilities(capabilities); len(errs) != 0 {
		t.Fatalf("valid declaration rejected: %v", errs)
	}
	delete(capabilities.Operations, CapabilitySnapshotPatches)
	capabilities.Operations["write_review"] = CapabilityDeclaration{State: CapabilitySupported}
	errs := ValidateCapabilities(capabilities)
	if !errs.Has(ValidationMissingCapability) || !errs.Has(ValidationUnknownCapability) {
		t.Fatalf("operation-set drift accepted: %v", errs)
	}
}

func TestValidateCapabilitiesRequiresUnsupportedReason(t *testing.T) {
	capabilities := supportedCapabilities()
	capabilities.Operations[CapabilityDiscoverCommit] = CapabilityDeclaration{State: CapabilityUnsupported}
	if errs := ValidateCapabilities(capabilities); !errs.Has(ValidationInvalidReason) {
		t.Fatalf("reason-less unsupported declaration accepted: %v", errs)
	}
	capabilities.Operations[CapabilityDiscoverCommit] = CapabilityDeclaration{
		State: CapabilityUnsupported, ReasonCode: CapabilityReasonEndpointUnsupported,
	}
	if errs := ValidateCapabilities(capabilities); len(errs) != 0 {
		t.Fatalf("declared unsupported reason rejected: %v", errs)
	}
}

func TestValidateHostIdentityFreezesExplicitOrigins(t *testing.T) {
	if errs := ValidateHostIdentity(githubHost()); len(errs) != 0 {
		t.Fatalf("valid GitHub host rejected: %v", errs)
	}
	host := githubHost()
	host.EndpointOrigins[1] = "https://api.github.com/v3?token=secret"
	if errs := ValidateHostIdentity(host); !errs.Has(ValidationInvalidOrigin) {
		t.Fatalf("path/query-bearing endpoint accepted: %v", errs)
	}
	host = githubHost()
	host.EndpointOrigins = []string{"https://api.github.com"}
	if errs := ValidateHostIdentity(host); !errs.Has(ValidationInvalidOrigin) {
		t.Fatalf("host without its display origin accepted: %v", errs)
	}
}

func TestHostListDTOIsCredentialSafeAndUsesArray(t *testing.T) {
	mode := AuthTokenEnvironment
	response := HostListResponse{Hosts: []HostStatus{
		{
			Host: githubHost(), ApprovalState: HostApproved,
			AuthenticationMode: &mode, AuthenticationConfigured: true,
			Capabilities: supportedCapabilities(), Assessment: model.ExactGitEvidence(),
		},
	}}
	if errs := ValidateHostListResponse(response); len(errs) != 0 {
		t.Fatalf("valid host list rejected: %v", errs)
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"token_reference", "authorization", "cookie", "secret"} {
		if strings.Contains(strings.ToLower(string(raw)), forbidden) {
			t.Fatalf("host status leaked credential-shaped field %q: %s", forbidden, raw)
		}
	}
	empty, err := json.Marshal(HostListResponse{Hosts: []HostStatus{}})
	if err != nil {
		t.Fatal(err)
	}
	if string(empty) != `{"hosts":[]}` {
		t.Fatalf("empty host list must be [], got %s", empty)
	}
	if errs := ValidateHostListResponse(HostListResponse{}); !errs.Has(ValidationMissingField) {
		t.Fatalf("nil host list accepted: %v", errs)
	}
}

func TestResultMetadataRetryAfterUsesExplicitSeconds(t *testing.T) {
	seconds := int64(15)
	metadata := ResultMetadata{Assessment: model.ExactGitEvidence(), RetryAfterSeconds: &seconds}
	if errs := ValidateResultMetadata(metadata); len(errs) != 0 {
		t.Fatalf("valid retry interval rejected: %v", errs)
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"retry_after_seconds":15`) || strings.Contains(string(raw), `"retry_after":`) {
		t.Fatalf("retry interval must use explicit seconds, got %s", raw)
	}
	seconds = -1
	if errs := ValidateResultMetadata(metadata); !errs.Has(ValidationInvalidLimit) {
		t.Fatalf("negative retry interval accepted: %v", errs)
	}
}

func TestValidateSnapshotResultRequiresBytesForEveryExactPatch(t *testing.T) {
	result := validSnapshotResult()
	if errs := ValidateSnapshotResult(result); len(errs) != 0 {
		t.Fatalf("valid snapshot result rejected: %v", errs)
	}
	result.Contents = []SnapshotContent{}
	if errs := ValidateSnapshotResult(result); !errs.Has(ValidationContractMismatch) {
		t.Fatalf("exact patch without capture bytes accepted: %v", errs)
	}
	result = validSnapshotResult()
	result.Snapshot.Files[0].PatchAssessment = model.NonExactGitEvidence(
		model.GitEvidenceUnavailable, model.ReasonChangeRequestPartial,
	)
	if errs := ValidateSnapshotResult(result); !errs.Has(ValidationContractMismatch) {
		t.Fatalf("non-exact patch retained authoritative bytes: %v", errs)
	}
	result = validSnapshotResult()
	result.Contents = nil
	if errs := ValidateSnapshotResult(result); !errs.Has(ValidationMissingField) {
		t.Fatalf("nil capture content array accepted: %v", errs)
	}
}

func validSnapshotResult() SnapshotResult {
	additions, deletions := 2, 1
	target := &model.HostedRepositoryIdentity{
		HostID: "host-github-public", ImmutableID: "repository-1", Slug: "acme/widgets",
	}
	return SnapshotResult{
		Snapshot: model.ChangeRequestSnapshot{
			SnapshotID: "snapshot-provider-pr-42",
			Identity: model.ChangeRequestIdentity{
				Provider: model.ChangeProviderGitHub, HostID: target.HostID,
				TargetRepository: target, ProviderObjectID: "provider-pr-42",
			},
			Content: model.ChangeRequestContentVersion{
				Key:        "github:provider-pr-42:content-1",
				BaseRefSHA: strings.Repeat("1", 40), DiffBaseSHA: strings.Repeat("1", 40),
				HeadSHA: strings.Repeat("2", 40), FileManifestDigest: "sha256:manifest-1",
			},
			MetadataRevision: "metadata-1", Kind: model.ChangeRequestPullRequest,
			DisplayNumber: "42", LifecycleState: model.ChangeLifecycleOpen,
			Title: "Add provider capture", WebURL: "https://github.com/acme/widgets/pull/42",
			SourceRepository: target, SourceRef: "feature", TargetRef: "main",
			Files: []model.GitFileChange{{
				Ordinal: 0, Key: "hosted:file-1", Layer: model.GitFileLayerHosted,
				DisplayPath: "internal/provider.go", PathEncoding: model.GitPathUTF8,
				Status: model.GitFileModified, OldMode: "100644", NewMode: "100644",
				Additions: &additions, Deletions: &deletions,
				StatusAssessment: model.ExactGitEvidence(), PatchAssessment: model.ExactGitEvidence(),
				Evidence: []model.GitEvidenceLink{},
			}},
			Commits: []model.GitCandidateCommit{},
			Completeness: model.ChangeRequestCompleteness{
				Metadata: model.ExactGitEvidence(), FileSet: model.ExactGitEvidence(),
				Patches: model.ExactGitEvidence(), Modes: model.ExactGitEvidence(),
				Commits: model.ExactGitEvidence(),
			},
			FetchedAt: time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC),
		},
		Contents: []SnapshotContent{{
			FileKey: "hosted:file-1", Purpose: SnapshotContentPatch,
			Content: []byte("@@ -1 +1 @@\n-old\n+new\n"),
		}},
		Metadata: ResultMetadata{Assessment: model.ExactGitEvidence(), PageCount: 1, ItemCount: 1},
	}
}

func TestProviderErrorIsTypedAndRedactsCause(t *testing.T) {
	cause := errors.New("https://token@example.com/private?access_token=secret")
	err := &Error{Code: ErrorAuthRequired, Operation: OperationGetSnapshot, Cause: cause}
	if !errors.Is(err, cause) {
		t.Fatal("typed provider error must retain trusted internal cause")
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "example.com") {
		t.Fatalf("safe error string leaked cause: %q", err.Error())
	}
	if err.EvidenceReason() != model.ReasonChangeHostAuthRequired {
		t.Fatalf("reason mapping = %q", err.EvidenceReason())
	}
	for _, code := range []ErrorCode{
		ErrorHostNotApproved, ErrorHostRevoked, ErrorAuthRequired, ErrorUnsupported,
		ErrorNotFound, ErrorPartial, ErrorOverflow, ErrorRateLimited,
		ErrorCaptureRaced, ErrorInvalidResponse, ErrorUnavailable,
	} {
		if !IsKnownErrorCode(code) {
			t.Errorf("known error code %q rejected", code)
		}
	}
	if IsKnownErrorCode("provider_exploded") {
		t.Fatal("unknown provider error code accepted")
	}
}

type contractProvider struct {
	kind model.ChangeProviderKind
	host HostIdentity
	caps ProviderCapabilities
}

func (p contractProvider) Kind() model.ChangeProviderKind { return p.kind }
func (p contractProvider) Host() HostIdentity             { return p.host }
func (p contractProvider) Capabilities() ProviderCapabilities {
	return p.caps
}
func (contractProvider) ParseReference(string) (model.ChangeRequestReference, bool) {
	return model.ChangeRequestReference{}, false
}
func (contractProvider) ParseRemote(string) (model.HostedRepositoryReference, bool) {
	return model.HostedRepositoryReference{}, false
}
func (contractProvider) ResolveRepository(context.Context, model.HostedRepositoryReference) (RepositoryResult, error) {
	panic("ValidateProvider must not resolve")
}
func (contractProvider) Resolve(context.Context, model.ChangeRequestReference) (ResolveResult, error) {
	panic("ValidateProvider must not resolve")
}
func (contractProvider) DiscoverForHead(context.Context, model.HostedRepositoryIdentity, model.HostedRepositoryIdentity, string, string) (DiscoveryResult, error) {
	panic("ValidateProvider must not discover")
}
func (contractProvider) DiscoverForCommit(context.Context, model.HostedRepositoryIdentity, string) (DiscoveryResult, error) {
	panic("ValidateProvider must not discover")
}
func (contractProvider) GetSnapshot(context.Context, model.ChangeRequestIdentity, model.ContentVersionKey) (SnapshotResult, error) {
	panic("ValidateProvider must not fetch")
}

func TestValidateProviderChecksOnlyStaticBoundContract(t *testing.T) {
	provider := contractProvider{kind: model.ChangeProviderGitHub, host: githubHost(), caps: supportedCapabilities()}
	if errs := ValidateProvider(provider); len(errs) != 0 {
		t.Fatalf("valid provider rejected: %v", errs)
	}
	provider.kind = model.ChangeProviderGitLab
	if errs := ValidateProvider(provider); !errs.Has(ValidationContractMismatch) {
		t.Fatalf("provider/host mismatch accepted: %v", errs)
	}
}
