package changehost

import (
	"errors"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

// stubHostParser is a host-bound OpenAPI profile parser fixture: it matches
// URLs on its fixed origin and stamps the reference with its host ID, the way
// a verified profile parser will.
type stubHostParser struct {
	hostID  string
	origin  string
	remote  bool
	seenRaw []string
}

func (p *stubHostParser) Kind() model.ChangeProviderKind { return model.ChangeProviderOpenAPI }

func (p *stubHostParser) ParseReference(raw string) (model.ChangeRequestReference, bool) {
	p.seenRaw = append(p.seenRaw, raw)
	if len(raw) <= len(p.origin)+1 || raw[:len(p.origin)] != p.origin {
		return model.ChangeRequestReference{}, false
	}
	number := raw[len(raw)-4:]
	return model.ChangeRequestReference{
		Provider:             model.ChangeProviderOpenAPI,
		HostID:               p.hostID,
		DisplayOrigin:        p.origin,
		TargetRepositorySlug: "team/repo",
		DisplayNumber:        number,
		NormalizedURL:        raw,
	}, true
}

func (p *stubHostParser) ParseRemote(raw string) (model.HostedRepositoryReference, bool) {
	if !p.remote {
		return model.HostedRepositoryReference{}, false
	}
	return model.HostedRepositoryReference{
		Provider: model.ChangeProviderOpenAPI, HostID: p.hostID,
		DisplayOrigin: p.origin,
		Slug:          "team/repo", SanitizedRemote: p.origin + "/team/repo",
	}, true
}

func TestRegistryResolvesReferenceThroughHostParser(t *testing.T) {
	registry := NewDefaultRegistry()
	parser := &stubHostParser{hostID: "review-company-internal", origin: "https://review.internal"}
	if err := registry.RegisterHostParser(RegisteredReferenceParser{
		ID: "review-company-internal/profile-1", HostID: parser.hostID, Parser: parser,
	}); err != nil {
		t.Fatal(err)
	}
	reference, err := registry.ResolveReference("https://review.internal/projects/team/repo/pulls/1234")
	if err != nil {
		t.Fatal(err)
	}
	if reference.Provider != model.ChangeProviderOpenAPI || reference.HostID != "review-company-internal" {
		t.Fatalf("host parser reference lost its identity: %+v", reference)
	}
	// Built-in parsing still wins on its own URLs and carries its fixed host.
	githubRef, err := registry.ResolveReference("https://github.com/acme/widgets/pull/42")
	if err != nil {
		t.Fatal(err)
	}
	if githubRef.Provider != model.ChangeProviderGitHub || githubRef.HostID != PublicGitHubHostKey {
		t.Fatalf("github reference regressed: %+v", githubRef)
	}
}

func TestRegistryRejectsInvalidHostParsers(t *testing.T) {
	registry := NewDefaultRegistry()
	cases := []RegisteredReferenceParser{
		{ID: "", HostID: "host", Parser: &stubHostParser{hostID: "host"}},
		{ID: "id", HostID: "", Parser: &stubHostParser{hostID: "host"}},
		{ID: "id", HostID: "host", Parser: nil},
		{ID: "id", HostID: "host", Parser: GitHubParser{}},
	}
	for i, parser := range cases {
		if err := registry.RegisterHostParser(parser); err == nil {
			t.Fatalf("case %d: invalid host parser registered", i)
		}
	}
	if len(registry.HostParsers()) != 0 {
		t.Fatal("rejected host parsers leaked into the registry")
	}
}

func TestRegistryHostParserDuplicatesAndAtomicReplace(t *testing.T) {
	registry := NewDefaultRegistry()
	first := &stubHostParser{hostID: "host-a", origin: "https://a.internal"}
	second := &stubHostParser{hostID: "host-b", origin: "https://b.internal"}
	entry := RegisteredReferenceParser{ID: "host-a/profile-1", HostID: "host-a", Parser: first}
	if err := registry.RegisterHostParser(entry); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterHostParser(entry); !errors.Is(err, ErrDuplicateProvider) {
		t.Fatalf("duplicate host parser accepted: %v", err)
	}
	// An invalid replacement must leave the previous set untouched.
	err := registry.ReplaceHostParsers([]RegisteredReferenceParser{{ID: "bad", HostID: "", Parser: second}})
	if err == nil {
		t.Fatal("invalid replacement accepted")
	}
	if got := registry.HostParsers(); len(got) != 1 || got[0].ID != "host-a/profile-1" {
		t.Fatalf("failed replace mutated the parser set: %+v", got)
	}
	if err := registry.ReplaceHostParsers([]RegisteredReferenceParser{
		{ID: "host-b/profile-2", HostID: "host-b", Parser: second},
	}); err != nil {
		t.Fatal(err)
	}
	if got := registry.HostParsers(); len(got) != 1 || got[0].HostID != "host-b" {
		t.Fatalf("replace did not swap the parser set: %+v", got)
	}
	// Profile revocation clears the set.
	if err := registry.ReplaceHostParsers(nil); err != nil {
		t.Fatal(err)
	}
	if len(registry.HostParsers()) != 0 {
		t.Fatal("nil replace did not clear host parsers")
	}
}

func TestRegistryHostParserAmbiguityFailsClosed(t *testing.T) {
	registry := NewDefaultRegistry()
	left := &stubHostParser{hostID: "host-a", origin: "https://review.internal"}
	right := &stubHostParser{hostID: "host-b", origin: "https://review.internal"}
	if err := registry.RegisterHostParser(RegisteredReferenceParser{ID: "a", HostID: "host-a", Parser: left}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterHostParser(RegisteredReferenceParser{ID: "b", HostID: "host-b", Parser: right}); err != nil {
		t.Fatal(err)
	}
	_, err := registry.ResolveReference("https://review.internal/projects/team/repo/pulls/1234")
	if !errors.Is(err, ErrAmbiguousReference) {
		t.Fatalf("overlapping host profiles must fail closed: %v", err)
	}
}

func TestRegistryHostParserHostIDMismatchIsNotAMatch(t *testing.T) {
	registry := NewDefaultRegistry()
	// The parser stamps a different host ID than its registration; the
	// registry must not trust it, and the reference must not fall through to
	// a silent match.
	parser := &stubHostParser{hostID: "other-host", origin: "https://review.internal"}
	if err := registry.RegisterHostParser(RegisteredReferenceParser{ID: "a", HostID: "expected-host", Parser: parser}); err != nil {
		t.Fatal(err)
	}
	_, err := registry.ResolveReference("https://review.internal/projects/team/repo/pulls/1234")
	if errors.Is(err, ErrAmbiguousReference) {
		t.Fatalf("mismatched host ID must not be ambiguous: %v", err)
	}
	if reference, ok := registry.ParseReference("https://review.internal/projects/team/repo/pulls/1234"); ok &&
		reference.Provider == model.ChangeProviderOpenAPI {
		t.Fatalf("mismatched host ID produced an openapi reference: %+v", reference)
	}
}

func TestRegistryResolveRemoteBindsHostParserHostID(t *testing.T) {
	registry := NewDefaultRegistry()
	parser := &stubHostParser{hostID: "review-company-internal", origin: "https://review.internal", remote: true}
	if err := registry.RegisterHostParser(RegisteredReferenceParser{
		ID: "review-company-internal/profile-1", HostID: parser.hostID, Parser: parser,
	}); err != nil {
		t.Fatal(err)
	}
	remote, err := registry.ResolveRemote("https://review.internal/team/repo")
	if err != nil {
		t.Fatal(err)
	}
	if remote.Provider != model.ChangeProviderOpenAPI || remote.HostID != "review-company-internal" {
		t.Fatalf("host parser remote lost its host binding: %+v", remote)
	}
}

func TestRegistryResolveRemoteRejectsMismatchedHostID(t *testing.T) {
	registry := NewDefaultRegistry()
	// The parser stamps a different host ID than its registration; the
	// registry must not trust the reference.
	parser := &stubHostParser{hostID: "other-host", origin: "https://review.internal", remote: true}
	if err := registry.RegisterHostParser(RegisteredReferenceParser{ID: "a", HostID: "expected-host", Parser: parser}); err != nil {
		t.Fatal(err)
	}
	// A generic fallback reference is fine; an openapi-bound one is not.
	remote, err := registry.ResolveRemote("https://review.internal/team/repo")
	if err == nil && remote.Provider == model.ChangeProviderOpenAPI {
		t.Fatalf("mismatched host ID produced an openapi remote reference: %+v", remote)
	}
}

func TestRegistryResolveRemoteRejectsHostlessOpenAPIReference(t *testing.T) {
	registry := NewDefaultRegistry()
	parser := &stubHostParser{hostID: "", origin: "https://review.internal", remote: true}
	if err := registry.RegisterHostParser(RegisteredReferenceParser{ID: "a", HostID: "expected-host", Parser: parser}); err != nil {
		t.Fatal(err)
	}
	remote, err := registry.ResolveRemote("https://review.internal/team/repo")
	if err == nil && remote.Provider == model.ChangeProviderOpenAPI {
		t.Fatalf("hostless openapi remote reference must not validate: %+v", remote)
	}
}
