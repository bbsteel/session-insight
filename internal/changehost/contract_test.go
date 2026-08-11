package changehost

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

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
