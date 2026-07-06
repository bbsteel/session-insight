package shared

import "testing"

func TestResolveProjectDetectsOpenClawWorkspacePath(t *testing.T) {
	got := ResolveProject("/tmp/nonexistent/.openclaw/workspace/projects/collab/sample-project", "")
	if got != "sample-project" {
		t.Fatalf("expected sample-project, got %q", got)
	}
}

func TestResolveProjectDetectsOpenClawWorkspaceSubdir(t *testing.T) {
	got := ResolveProject("/tmp/nonexistent/.openclaw/workspace/projects/collab/sample-project/internal/pkg", "")
	if got != "sample-project" {
		t.Fatalf("expected sample-project, got %q", got)
	}
}
