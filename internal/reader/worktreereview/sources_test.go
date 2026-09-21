package worktreereview

import (
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

// TestSourceInventory covers the sources.go contract: real files with
// precise roles, no directory dump, no `other` for known layout paths.
func TestSourceInventory(t *testing.T) {
	sources := sourceInventory("testdata", fixturePassed)
	if len(sources) != 3 {
		t.Fatalf("sources=%d want 3", len(sources))
	}

	byRole := map[string]model.SessionSourceFile{}
	for _, source := range sources {
		if source.Path == "testdata" || source.Path == "" {
			t.Fatalf("source path must be a file, got %q", source.Path)
		}
		if source.Role == model.SourceRoleOther {
			t.Fatalf("known layout path %q mapped to role other", source.Path)
		}
		byRole[source.Role] = source
	}

	for _, role := range []string{
		model.SourceRolePrimaryTranscript,
		model.SourceRoleMetadata,
		model.SourceRoleSnapshot,
	} {
		if _, ok := byRole[role]; !ok {
			t.Errorf("missing role %s", role)
		}
	}
	if got := byRole[model.SourceRolePrimaryTranscript].Path; got != "testdata/"+fixturePassed+"/events.jsonl" {
		t.Errorf("primary=%q", got)
	}
	// result.json exists in this fixture and must be reported present.
	if byRole[model.SourceRoleSnapshot].State != "present" {
		t.Errorf("result.json state=%q", byRole[model.SourceRoleSnapshot].State)
	}
}

func TestSourceInventoryMissingResult(t *testing.T) {
	sources := sourceInventory("testdata", fixtureRunning)
	for _, source := range sources {
		if source.Role == model.SourceRoleSnapshot && source.State == "present" {
			t.Error("running fixture has no result.json; snapshot must not be present")
		}
	}
}

func TestSourceInventoryInvalidID(t *testing.T) {
	if sources := sourceInventory("testdata", "../escape"); sources != nil {
		t.Fatalf("hostile id produced sources: %v", sources)
	}
}
