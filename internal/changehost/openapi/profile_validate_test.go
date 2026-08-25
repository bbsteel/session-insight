package openapi

import (
	"strings"
	"testing"
)

// validTestProfile is the smallest profile that satisfies every structural
// rule, so each test can break exactly one rule at a time.
func validTestProfile() Profile {
	return Profile{
		SchemaVersion:   SchemaVersion,
		ProfileID:       "profile-01J4Z8EXAMPLE",
		ProfileRevision: 1,
		DisplayName:     "Internal Review",
		Adapter:         AdapterKind,
		HostID:          "review-company-internal",
		DisplayOrigin:   "https://review.internal",
		EndpointOrigins: []string{"https://review.internal", "https://api.review.internal"},
		Reference: ReferenceTemplate{
			Origin:              "https://review.internal",
			PathTemplate:        "/projects/{repository}/reviews/{number}",
			RepositoryParameter: "repository",
			NumberParameter:     "number",
		},
		Authentication: Authentication{
			Scheme:              "header",
			HeaderName:          "PRIVATE-TOKEN",
			CredentialReference: "keyring:profile-01J4Z8EXAMPLE",
		},
		Operations: ProfileOperations{
			ResolveChange: &Operation{
				Method:       "GET",
				Origin:       "https://api.review.internal",
				PathTemplate: "/api/projects/{repository}/reviews/{number}",
				Parameters: map[string]string{
					"repository": "reference.repository",
					"number":     "reference.number",
				},
				Headers:    map[string]string{"Accept": "application/json"},
				Pagination: Pagination{Mode: PaginationNone},
				Response: OperationResponse{
					ItemPointer: "",
					Fields: map[string]FieldSelector{
						"provider_object_id": {Pointer: "/id", Transform: &FieldTransform{Name: TransformIntegerToStr}},
						"display_number":     {Pointer: "/number", Transform: &FieldTransform{Name: TransformIntegerToStr}},
						"title":              {Pointer: "/title"},
						"lifecycle_state": {Pointer: "/state", Transform: &FieldTransform{
							Name:    TransformEnumMap,
							Mapping: map[string]string{"open": "open", "merged": "merged", "closed": "closed"},
						}},
						"head_sha": {Pointer: "/source/latestCommit/id", Transform: &FieldTransform{Name: TransformGitSHA}},
					},
				},
			},
			ListFiles: &Operation{
				Method:       "GET",
				Origin:       "https://api.review.internal",
				PathTemplate: "/api/projects/{repository}/reviews/{number}/files",
				Parameters: map[string]string{
					"repository": "reference.repository",
					"number":     "reference.number",
				},
				Pagination: Pagination{
					Mode:              PaginationCursorBody,
					CursorParameter:   "cursor",
					NextCursorPointer: "/meta/nextCursor",
				},
				Response: OperationResponse{
					ItemsPointer: "/values",
					Fields: map[string]FieldSelector{
						"path":   {Pointer: "/new/path"},
						"status": {Pointer: "/status", Transform: &FieldTransform{Name: TransformFileStatus}},
						"patch":  {Pointer: "/diff"},
					},
				},
			},
		},
		Capabilities: Capabilities{
			Metadata:      CapabilitySupported,
			FileSet:       CapabilitySupported,
			Patches:       CapabilitySupported,
			Modes:         CapabilityUnsupported,
			Commits:       CapabilityUnsupported,
			ContentAnchor: "head_sha",
			RepositoryID:  CapabilityUnsupported,
		},
		SpecDigest: "sha256:" + strings.Repeat("a", 64),
	}
}

func TestValidateProfileAcceptsValidProfile(t *testing.T) {
	if issues := ValidateProfile(validTestProfile()); !issues.OK() {
		t.Fatalf("valid profile rejected: %+v", issues)
	}
}

func TestValidateProfileRejectsStructuralViolations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Profile)
		code   ProfileIssueCode
	}{
		{"schema version", func(p *Profile) { p.SchemaVersion = 2 }, IssueProfileContractInvalid},
		{"adapter kind", func(p *Profile) { p.Adapter = "rest" }, IssueProfileContractInvalid},
		{"profile id", func(p *Profile) { p.ProfileID = "has space" }, IssueProfileContractInvalid},
		{"revision", func(p *Profile) { p.ProfileRevision = 0 }, IssueProfileContractInvalid},
		{"host id", func(p *Profile) { p.HostID = "" }, IssueProfileContractInvalid},
		{"display origin with query", func(p *Profile) { p.DisplayOrigin = "https://review.internal?token=x" }, IssueProfileContractInvalid},
		{"endpoint origin with userinfo", func(p *Profile) {
			p.EndpointOrigins = []string{"https://review.internal", "https://user@api.review.internal"}
		}, IssueProfileContractInvalid},
		{"endpoint origins miss display origin", func(p *Profile) {
			p.EndpointOrigins = []string{"https://api.review.internal"}
		}, IssueProfileContractInvalid},
		{"reference origin mismatch", func(p *Profile) {
			p.Reference.Origin = "https://other.internal"
		}, IssueProfileContractInvalid},
		{"reference traversal", func(p *Profile) {
			p.Reference.PathTemplate = "/projects/../reviews/{number}"
		}, IssueProfileContractInvalid},
		{"reference undeclared repository parameter", func(p *Profile) {
			p.Reference.RepositoryParameter = "repo"
		}, IssueMappingIncomplete},
		{"unsupported auth scheme", func(p *Profile) {
			p.Authentication.Scheme = "query"
		}, IssueProfileContractInvalid},
		{"authorization without reviewed prefix", func(p *Profile) {
			p.Authentication.HeaderName = "Authorization"
			p.Authentication.ValuePrefix = ""
		}, IssueProfileContractInvalid},
		{"custom header with prefix", func(p *Profile) {
			p.Authentication.HeaderName = "X-API-Key"
			p.Authentication.ValuePrefix = "Key "
		}, IssueProfileContractInvalid},
		{"cookie credential header", func(p *Profile) {
			p.Authentication.HeaderName = "Cookie"
		}, IssueProfileContractInvalid},
		{"invalid credential reference", func(p *Profile) {
			p.Authentication.CredentialReference = "file:/etc/passwd"
		}, IssueCredentialUnavailable},
		{"missing resolve change", func(p *Profile) {
			p.Operations.ResolveChange = nil
		}, IssueMappingIncomplete},
		{"write method", func(p *Profile) {
			p.Operations.ResolveChange.Method = "POST"
		}, IssueProfileContractInvalid},
		{"operation origin outside allowlist", func(p *Profile) {
			p.Operations.ResolveChange.Origin = "https://evil.example"
		}, IssueProfileContractInvalid},
		{"unbound path parameter", func(p *Profile) {
			delete(p.Operations.ResolveChange.Parameters, "number")
		}, IssueMappingIncomplete},
		{"unknown binding grammar", func(p *Profile) {
			p.Operations.ResolveChange.Parameters["number"] = "request.url"
		}, IssueProfileContractInvalid},
		{"undeclared operation binding", func(p *Profile) {
			p.Operations.ResolveChange.Parameters["number"] = "operation.list_commits.sha"
		}, IssueMappingIncomplete},
		{"self operation binding", func(p *Profile) {
			p.Operations.ResolveChange.Parameters["number"] = "operation.resolve_change.display_number"
		}, IssueProfileContractInvalid},
		{"credential header override", func(p *Profile) {
			p.Operations.ResolveChange.Headers["PRIVATE-TOKEN"] = "hardcoded"
		}, IssueProfileContractInvalid},
		{"authorization header injection", func(p *Profile) {
			p.Operations.ResolveChange.Headers["Authorization"] = "Bearer x"
		}, IssueProfileContractInvalid},
		{"pagination cursor missing pointer", func(p *Profile) {
			p.Operations.ListFiles.Pagination.NextCursorPointer = ""
		}, IssueMappingIncomplete},
		{"pagination none with fields", func(p *Profile) {
			p.Operations.ResolveChange.Pagination = Pagination{Mode: PaginationNone, PageParameter: "page"}
		}, IssueProfileContractInvalid},
		{"pointer traversal", func(p *Profile) {
			p.Operations.ResolveChange.Response.Fields["title"] = FieldSelector{Pointer: "/../title"}
		}, IssueProfileContractInvalid},
		{"pointer bad escape", func(p *Profile) {
			p.Operations.ResolveChange.Response.Fields["title"] = FieldSelector{Pointer: "/a~2b"}
		}, IssueProfileContractInvalid},
		{"list operation without items pointer", func(p *Profile) {
			p.Operations.ListFiles.Response.ItemsPointer = ""
		}, IssueMappingIncomplete},
		{"unknown standard field", func(p *Profile) {
			p.Operations.ResolveChange.Response.Fields["payload"] = FieldSelector{Pointer: "/payload"}
		}, IssueProfileContractInvalid},
		{"enum map without mapping", func(p *Profile) {
			p.Operations.ResolveChange.Response.Fields["lifecycle_state"] = FieldSelector{
				Pointer: "/state", Transform: &FieldTransform{Name: TransformEnumMap},
			}
		}, IssueMappingIncomplete},
		{"mapping on non-enum transform", func(p *Profile) {
			p.Operations.ResolveChange.Response.Fields["title"] = FieldSelector{
				Pointer: "/title", Transform: &FieldTransform{Name: TransformLowercase, Mapping: map[string]string{"a": "b"}},
			}
		}, IssueProfileContractInvalid},
		{"content anchor missing", func(p *Profile) {
			p.Capabilities.ContentAnchor = ""
		}, IssueContentAnchorMissing},
		{"updated_at style anchor rejected", func(p *Profile) {
			p.Capabilities.ContentAnchor = "updated_at"
		}, IssueProfileContractInvalid},
		{"head anchor without head field", func(p *Profile) {
			delete(p.Operations.ResolveChange.Response.Fields, "head_sha")
		}, IssueContentAnchorMissing},
		{"commits capability without operation", func(p *Profile) {
			p.Capabilities.Commits = CapabilitySupported
		}, IssueMappingIncomplete},
		{"bad spec digest", func(p *Profile) { p.SpecDigest = "md5:abc" }, IssueProfileContractInvalid},
		{"negative limit", func(p *Profile) { p.Limits.MaximumFiles = -1 }, IssueProfileContractInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile := validTestProfile()
			tc.mutate(&profile)
			issues := ValidateProfile(profile)
			if issues.OK() {
				t.Fatalf("expected %s violation, got none", tc.code)
			}
			found := false
			for _, issue := range issues {
				if issue.Code == tc.code {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected issue code %s, got %+v", tc.code, issues)
			}
		})
	}
}

func TestProfileEncodeDecodeRoundTrip(t *testing.T) {
	profile := validTestProfile()
	encoded, err := EncodeProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeProfile(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if issues := ValidateProfile(decoded); !issues.OK() {
		t.Fatalf("decoded profile rejected: %+v", issues)
	}
	if decoded.ProfileID != profile.ProfileID || decoded.HostID != profile.HostID ||
		decoded.Operations.ResolveChange.PathTemplate != profile.Operations.ResolveChange.PathTemplate {
		t.Fatal("profile did not survive the encode/decode round trip")
	}
	if _, err := DecodeProfile([]byte("{")); err == nil {
		t.Fatal("invalid JSON decoded without error")
	}
}

func TestValidateProfileRejectsDraftShapedMissingMappings(t *testing.T) {
	profile := validTestProfile()
	profile.Operations.ResolveChange.Response.Fields = map[string]FieldSelector{}
	issues := ValidateProfile(profile)
	found := false
	for _, issue := range issues {
		if issue.Code == IssueMappingIncomplete {
			found = true
		}
	}
	if !found {
		t.Fatalf("empty field mapping must be reported incomplete: %+v", issues)
	}
}
