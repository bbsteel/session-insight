package changehost

import (
	"errors"
	"net/http"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestGenericParserKeepsExactPathIdentityOffline(t *testing.T) {
	parser := GenericParser{}
	ref, ok := parser.ParseReference("https://CODE.EXAMPLE:443/team/repo/reviews/7")
	if !ok {
		t.Fatal("safe generic reference rejected")
	}
	if ref.NormalizedURL != "https://code.example/team/repo/reviews/7" || ref.DisplayOrigin != "https://code.example" {
		t.Fatalf("unexpected normalization: %+v", ref)
	}
	identity, err := GenericIdentity(ref)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := parser.ParseReference(ref.NormalizedURL + "/")
	otherIdentity, err := GenericIdentity(other)
	if err != nil {
		t.Fatal(err)
	}
	if identity.GenericOpaqueID == otherIdentity.GenericOpaqueID {
		t.Fatal("distinct exact paths collapsed to one generic identity")
	}
	if validation := model.ValidateChangeRequestIdentity(identity); !validation.OK() {
		t.Fatalf("generic identity violates model contract: %+v", validation.Issues)
	}
}

func TestGenericParserRejectsAmbiguousOrSecretBearingReferences(t *testing.T) {
	parser := GenericParser{}
	for _, raw := range []string{
		"http://code.example/team/repo/reviews/7",
		"https://user:secret@code.example/team/repo/reviews/7",
		"https://code.example/team/repo/reviews/7?token=secret",
		"https://code.example/team/repo/reviews/7#fragment",
		"https://code.example/",
		"https://code.example:65536/team/repo/reviews/7",
		"https://code.example/team/../admin/reviews/7",
		" https://code.example/team/repo/reviews/7",
	} {
		if _, ok := parser.ParseReference(raw); ok {
			t.Errorf("unsafe generic reference accepted: %q", raw)
		}
	}
	root := model.ChangeRequestReference{
		Provider: model.ChangeProviderGeneric, DisplayOrigin: "https://code.example",
		NormalizedURL: "https://code.example/",
	}
	if _, err := GenericIdentity(root); !errors.Is(err, ErrUnsafeGenericReference) {
		t.Fatalf("root-only generic identity accepted: %v", err)
	}
}

type testParser struct {
	kind   model.ChangeProviderKind
	ref    model.ChangeRequestReference
	remote model.HostedRepositoryReference
}

func (p testParser) Kind() model.ChangeProviderKind { return p.kind }
func (p testParser) ParseReference(string) (model.ChangeRequestReference, bool) {
	return p.ref, true
}
func (p testParser) ParseRemote(string) (model.HostedRepositoryReference, bool) {
	return p.remote, true
}

func TestRegistryUsesProviderParserBeforeGenericFallback(t *testing.T) {
	registry := NewRegistry()
	if err := registry.RegisterParser(GenericParser{}); err != nil {
		t.Fatal(err)
	}
	github := testParser{kind: model.ChangeProviderGitHub, ref: model.ChangeRequestReference{
		Provider: model.ChangeProviderGitHub, DisplayOrigin: "https://github.com",
		TargetRepositorySlug: "acme/widgets", DisplayNumber: "42",
		NormalizedURL: "https://github.com/acme/widgets/pull/42",
	}}
	if err := registry.RegisterParser(github); err != nil {
		t.Fatal(err)
	}
	ref, ok := registry.ParseReference("https://github.com/acme/widgets/pull/42")
	if !ok || ref.Provider != model.ChangeProviderGitHub {
		t.Fatalf("provider parser did not win: %+v, %v", ref, ok)
	}
	if err := registry.RegisterParser(github); !errors.Is(err, ErrDuplicateProvider) {
		t.Fatalf("duplicate parser accepted: %v", err)
	}
}

func TestRegistryFailsClosedOnAmbiguousProviderReference(t *testing.T) {
	registry := NewRegistry()
	github := testParser{kind: model.ChangeProviderGitHub, ref: model.ChangeRequestReference{
		Provider: model.ChangeProviderGitHub, DisplayOrigin: "https://code.example",
		TargetRepositorySlug: "acme/widgets", DisplayNumber: "42",
		NormalizedURL: "https://code.example/acme/widgets/pull/42",
	}}
	gitlab := testParser{kind: model.ChangeProviderGitLab, ref: model.ChangeRequestReference{
		Provider: model.ChangeProviderGitLab, DisplayOrigin: "https://code.example",
		TargetRepositorySlug: "acme/widgets", DisplayNumber: "42",
		NormalizedURL: "https://code.example/acme/widgets/merge_requests/42",
	}}
	if err := registry.RegisterParser(github); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterParser(gitlab); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ResolveReference("ambiguous"); !errors.Is(err, ErrAmbiguousReference) {
		t.Fatalf("ambiguous automatic parsers selected a winner: %v", err)
	}
}

func TestRegistryRejectsUnsanitizedRemote(t *testing.T) {
	registry := NewRegistry()
	parser := testParser{kind: model.ChangeProviderGitHub, remote: model.HostedRepositoryReference{
		Provider: model.ChangeProviderGitHub, DisplayOrigin: "https://github.com", Slug: "acme/widgets",
		SanitizedRemote: "https://user:secret@github.com/acme/widgets.git?token=secret",
	}}
	if err := registry.RegisterParser(parser); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.ParseRemote("https://user:secret@github.com/acme/widgets.git?token=secret"); ok {
		t.Fatal("unsanitized provider remote accepted")
	}
}

func TestRegistryBindsFactoryToRequestedKindAndApprovedHost(t *testing.T) {
	approved := approvedGitHubHost(t)
	client, err := newHTTPClient(approved, HTTPClientConfig{}, nil, roundTripFunc(func(*http.Request) (*http.Response, error) {
		panic("provider validation must not perform network I/O")
	}))
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	if err := registry.RegisterFactory(model.ChangeProviderGitHub, func(HostIdentity, *HTTPClient) (Provider, error) {
		gitlabHost := HostIdentity{
			Key: "host-gitlab-public", Provider: model.ChangeProviderGitLab,
			DisplayOrigin: "https://gitlab.com", EndpointOrigins: []string{"https://gitlab.com"},
		}
		return contractProvider{kind: model.ChangeProviderGitLab, host: gitlabHost, caps: supportedCapabilities()}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.NewProvider(model.ChangeProviderGitHub, approved.Identity(), client); !errors.Is(err, ErrProviderContract) {
		t.Fatalf("factory returned a mismatched provider: %v", err)
	}

	registry = NewRegistry()
	if err := registry.RegisterFactory(model.ChangeProviderGitHub, func(host HostIdentity, _ *HTTPClient) (Provider, error) {
		return contractProvider{kind: model.ChangeProviderGitHub, host: host, caps: supportedCapabilities()}, nil
	}); err != nil {
		t.Fatal(err)
	}
	forgedHost := approved.Identity()
	forgedHost.Key = "different-host-key"
	if _, err := registry.NewProvider(model.ChangeProviderGitHub, forgedHost, client); !errors.Is(err, ErrProviderContract) {
		t.Fatalf("factory accepted host not bound to client approval: %v", err)
	}
	provider, err := registry.NewProvider(model.ChangeProviderGitHub, approved.Identity(), client)
	if err != nil || provider.Kind() != model.ChangeProviderGitHub {
		t.Fatalf("valid provider binding rejected: provider=%v err=%v", provider, err)
	}
}
