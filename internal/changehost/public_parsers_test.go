package changehost

import (
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestGitHubParserRecognizesSanitizedPRAndRemote(t *testing.T) {
	parser := GitHubParser{}
	reference, ok := parser.ParseReference("https://github.com/acme/widgets/pull/42/")
	if !ok || reference.Provider != model.ChangeProviderGitHub ||
		reference.TargetRepositorySlug != "acme/widgets" || reference.DisplayNumber != "42" ||
		reference.NormalizedURL != "https://github.com/acme/widgets/pull/42" {
		t.Fatalf("GitHub reference = %+v ok=%v", reference, ok)
	}
	for _, remote := range []string{
		"https://github.com/acme/widgets.git",
		"git@github.com:acme/widgets.git",
		"ssh://git@github.com/acme/widgets.git",
	} {
		parsed, ok := parser.ParseRemote(remote)
		if !ok || parsed.Slug != "acme/widgets" || parsed.SanitizedRemote != "https://github.com/acme/widgets" {
			t.Fatalf("GitHub remote %q = %+v ok=%v", remote, parsed, ok)
		}
	}
}

func TestGitLabParserRecognizesNestedMRAndRemote(t *testing.T) {
	parser := GitLabParser{}
	reference, ok := parser.ParseReference("https://gitlab.com/group/subgroup/widgets/-/merge_requests/17")
	if !ok || reference.Provider != model.ChangeProviderGitLab ||
		reference.TargetRepositorySlug != "group/subgroup/widgets" || reference.DisplayNumber != "17" {
		t.Fatalf("GitLab reference = %+v ok=%v", reference, ok)
	}
	parsed, ok := parser.ParseRemote("git@gitlab.com:group/subgroup/widgets.git")
	if !ok || parsed.Slug != "group/subgroup/widgets" || parsed.SanitizedRemote != "https://gitlab.com/group/subgroup/widgets" {
		t.Fatalf("GitLab remote = %+v ok=%v", parsed, ok)
	}
}

func TestPublicParsersRejectSecretBearingAndAmbiguousInputs(t *testing.T) {
	for _, test := range []struct {
		name   string
		parser ReferenceParser
		raw    string
		remote bool
	}{
		{name: "GitHub query", parser: GitHubParser{}, raw: "https://github.com/acme/widgets/pull/42?token=secret"},
		{name: "GitHub credential", parser: GitHubParser{}, raw: "https://user:secret@github.com/acme/widgets.git", remote: true},
		{name: "GitHub encoded slash", parser: GitHubParser{}, raw: "https://github.com/acme%2Fother/widgets/pull/42"},
		{name: "GitLab query", parser: GitLabParser{}, raw: "https://gitlab.com/group/widgets/-/merge_requests/7?private_token=secret"},
		{name: "GitLab wrong marker", parser: GitLabParser{}, raw: "https://gitlab.com/group/widgets/merge_requests/7"},
		{name: "SSH unexpected user", parser: GitLabParser{}, raw: "ssh://admin@gitlab.com/group/widgets.git", remote: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.remote {
				if _, ok := test.parser.ParseRemote(test.raw); ok {
					t.Fatal("unsafe remote accepted")
				}
				return
			}
			if _, ok := test.parser.ParseReference(test.raw); ok {
				t.Fatal("unsafe reference accepted")
			}
		})
	}
}

func TestPublicHostContractsAreFrozen(t *testing.T) {
	for _, host := range []HostIdentity{PublicGitHubHost(), PublicGitLabHost()} {
		if errs := ValidateHostIdentity(host); len(errs) != 0 {
			t.Fatalf("public host rejected: %+v: %v", host, errs)
		}
	}
}
