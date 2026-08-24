package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
			"LC_ALL=C",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.name", "test")
	run("config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README")
	run("commit", "-m", "init")
	return dir
}

func TestReadAgentEvidenceLockMissingIsEmpty(t *testing.T) {
	dir := gitRepo(t)
	sha, err := gitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	lock, err := readAgentEvidenceLock(dir, sha, "chrys")
	if err != nil {
		t.Fatalf("missing lock must be empty, not an error: %v", err)
	}
	if lock == nil || len(lock.Captures) != 0 || lock.AgentType != "chrys" {
		t.Fatalf("lock = %+v", lock)
	}
}

func TestReadAgentEvidenceLockReadsJSON(t *testing.T) {
	dir := gitRepo(t)
	path := filepath.Join(dir, "internal", "reader", "chrys")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"schema_version":1,"agent_type":"chrys","captures":{"04-thinking.png":{"current_sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}`)
	if err := os.WriteFile(filepath.Join(path, "presentation.evidence.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := gitCommand(dir, "add", "internal/reader/chrys/presentation.evidence.json")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	cmd = gitCommand(dir, "commit", "-m", "lock")
	cmd.Env = append(cmd.Env,
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	sha, err := gitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	lock, err := readAgentEvidenceLock(dir, sha, "chrys")
	if err != nil {
		t.Fatal(err)
	}
	got := lockHashFor(lockHashesByLogical(lock), "04-thinking", ".png")
	if got != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("hash = %s", got)
	}
}
