package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestValidateWorkOrderID(t *testing.T) {
	t.Parallel()
	for _, id := range []string{
		"chrys-20260824-080455",
		"claude-20260101-000000-2",
		"grok-20260824-080455-12",
	} {
		if err := validateWorkOrderID(id); err != nil {
			t.Errorf("validateWorkOrderID(%q) = %v, want nil", id, err)
		}
	}
	for _, id := range []string{
		"",
		".",
		"..",
		"../secret",
		"foo/bar",
		`foo\bar`,
		"/tmp/x",
		"chrys-20260824-080455/..",
	} {
		if err := validateWorkOrderID(id); err == nil {
			t.Errorf("validateWorkOrderID(%q) = nil, want error", id)
		}
	}
}

func TestConfinedWorkOrderDirAcceptsExistingFolder(t *testing.T) {
	t.Parallel()
	checkout := t.TempDir()
	id := "chrys-20260824-080455"
	dir := filepath.Join(workOrderRoot(checkout), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := confinedWorkOrderDir(checkout, id)
	if err != nil {
		t.Fatalf("confinedWorkOrderDir: %v", err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("path = %s, want %s", got, want)
	}
}

func TestConfinedWorkOrderDirRejectsTraversalAndMissing(t *testing.T) {
	t.Parallel()
	checkout := t.TempDir()
	if err := os.MkdirAll(workOrderRoot(checkout), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := confinedWorkOrderDir(checkout, "../secret"); err == nil {
		t.Fatal("traversal id must be rejected")
	}
	if _, err := confinedWorkOrderDir(checkout, "missing-20260824-080455"); err == nil {
		t.Fatal("missing directory must be rejected")
	}
	if _, err := confinedWorkOrderDir(checkout, ""); err == nil {
		t.Fatal("empty id must be rejected")
	}
}

func TestConfinedWorkOrderDirRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()
	checkout := t.TempDir()
	outside := t.TempDir()
	id := "claude-20260101-000000"
	root := workOrderRoot(checkout)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, id)); err != nil {
		t.Fatal(err)
	}
	if _, err := confinedWorkOrderDir(checkout, id); err == nil {
		t.Fatal("symlink out of the work-order root must be rejected")
	}
}

func TestLaunchFolderManagerUsesLinuxOpener(t *testing.T) {
	origGOOS, origLook, origStart := openRuntimeGOOS, lookOpenPath, startOpenCommand
	t.Cleanup(func() {
		openRuntimeGOOS = origGOOS
		lookOpenPath = origLook
		startOpenCommand = origStart
	})
	openRuntimeGOOS = "linux"
	lookOpenPath = func(name string) (string, error) {
		if name == "dolphin" {
			return "/usr/bin/dolphin", nil
		}
		return "", os.ErrNotExist
	}
	var gotArgs []string
	startOpenCommand = func(cmd *exec.Cmd) error {
		gotArgs = cmd.Args
		return nil
	}
	if err := launchFolderManagerOS("/tmp/work-order"); err != nil {
		t.Fatalf("launchFolderManagerOS: %v", err)
	}
	want := []string{"/usr/bin/dolphin", "/tmp/work-order"}
	if len(gotArgs) != 2 || gotArgs[0] != want[0] || gotArgs[1] != want[1] {
		t.Fatalf("argv = %v, want %v", gotArgs, want)
	}
}
