package shared

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveProjectSlugUnchanged(t *testing.T) {
	// Copilot-style repository slugs must stay as-is.
	if got := ResolveProject("/any/cwd", "owner/repo"); got != "owner/repo" {
		t.Fatalf("slug: got %q, want owner/repo", got)
	}
	if got := ResolveProject("", "bbsteel/session-insight"); got != "bbsteel/session-insight" {
		t.Fatalf("slug without cwd: got %q", got)
	}
}

func TestResolveProjectAbsoluteRepoPathIsBasename(t *testing.T) {
	// Grok stores git_root_dir as an absolute path, often with a trailing slash.
	// That must become a short project name, not appear as a second filter entry.
	cases := []struct {
		cwd, repo, want string
	}{
		{"/home/deck/projects/session-insight", "/home/deck/projects/session-insight/", "session-insight"},
		{"/home/deck/projects/session-insight/subdir", "/home/deck/projects/session-insight/", "session-insight"},
		{"/home/deck/projects/lego-lookup", "/home/deck/projects/lego-lookup/", "lego-lookup"},
		// Missing path: still basename (no ancestor walk that could pick /tmp/.git).
		{"/tmp/nonexistent/workspace/foo", "/tmp/nonexistent/workspace/foo/", "foo"},
	}
	for _, c := range cases {
		got := ResolveProject(c.cwd, c.repo)
		if got != c.want {
			t.Errorf("ResolveProject(%q, %q) = %q, want %q", c.cwd, c.repo, got, c.want)
		}
	}
}

func TestResolveProjectFromCwdBasename(t *testing.T) {
	if got := ResolveProject("/tmp/nonexistent/workspace/projects/collab/lego-lookup", ""); got != "lego-lookup" {
		t.Fatalf("cwd-only missing path: got %q", got)
	}
	if got := ResolveProject("", ""); got != "" {
		t.Fatalf("empty: got %q", got)
	}
}

func TestResolveProjectGitRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "pkg", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Base(root)
	if got := ResolveProject(sub, ""); got != want {
		t.Fatalf("git root from cwd: got %q, want %q", got, want)
	}
	// Absolute repo path that exists and is the git root.
	if got := ResolveProject(sub, root+"/"); got != want {
		t.Fatalf("git root from repo path: got %q, want %q", got, want)
	}
}

func TestResolveProjectClaudeWorktreeLayout(t *testing.T) {
	path := "/home/deck/projects/session-insight/.claude/worktrees/fix-foo/internal"
	if got := ResolveProject(path, ""); got != "session-insight" {
		t.Fatalf("worktree layout: got %q", got)
	}
	// Absolute "repo" path under a worktree layout should still prefer layout.
	if got := ResolveProject("/unused", path); got != "session-insight" {
		t.Fatalf("worktree layout via repo: got %q", got)
	}
}

func TestIsFilesystemPath(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"/home/deck/projects/foo/", true},
		{"/home/deck/projects/foo", true},
		{"~/projects/foo", true},
		{"owner/repo", false},
		{"bbsteel/session-insight", false},
		{"", false},
		{"  ", false},
	}
	for _, c := range cases {
		if got := isFilesystemPath(c.in); got != c.want {
			t.Errorf("isFilesystemPath(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
