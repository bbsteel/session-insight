package changehost

import (
	"errors"
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
		"https://code.example/team/../admin/reviews/7",
		" https://code.example/team/repo/reviews/7",
	} {
		if _, ok := parser.ParseReference(raw); ok {
			t.Errorf("unsafe generic reference accepted: %q", raw)
		}
	}
}

type testParser struct {
	kind model.ChangeProviderKind
	ref  model.ChangeRequestReference
}

func (p testParser) Kind() model.ChangeProviderKind { return p.kind }
func (p testParser) ParseReference(string) (model.ChangeRequestReference, bool) {
	return p.ref, true
}
func (p testParser) ParseRemote(string) (model.HostedRepositoryReference, bool) {
	return model.HostedRepositoryReference{Provider: p.kind}, true
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
