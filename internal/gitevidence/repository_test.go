package gitevidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

func gitCommand(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = cwd
	command.Env = append(os.Environ(), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %q: %v\n%s", args, err, output)
	}
	return string(output)
}

func createRepository(t *testing.T, parent, name string, extraInit ...string) string {
	t.Helper()
	repository := filepath.Join(parent, name)
	args := []string{"init", "-b", "main"}
	args = append(args, extraInit...)
	args = append(args, repository)
	gitCommand(t, parent, args...)
	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("tracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, repository, "add", "--", "tracked.txt")
	gitCommand(t, repository,
		"-c", "user.name=Fixture Author", "-c", "user.email=fixture@example.invalid",
		"commit", "-m", "fixture commit")
	return repository
}

func defaultResolver(t *testing.T) *Resolver {
	t.Helper()
	runner := testRunner(t, nil)
	resolver, err := NewResolver(runner)
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}

func resolvedRepository(t *testing.T, resolver *Resolver, cwd string) *Repository {
	t.Helper()
	resolution, err := resolver.Resolve(context.Background(), cwd)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Repository == nil || resolution.Assessment.State != model.GitEvidenceExact {
		t.Fatalf("unexpected resolution: %+v", resolution)
	}
	return resolution.Repository
}

func TestResolveRepositoryAndLinkedWorktreeIdentity(t *testing.T) {
	parent := t.TempDir()
	primaryPath := createRepository(t, parent, "primary")
	linkedPath := filepath.Join(parent, "linked")
	gitCommand(t, primaryPath, "worktree", "add", "--detach", linkedPath, "HEAD")
	primarySubdir := filepath.Join(primaryPath, "nested", "dir")
	if err := os.MkdirAll(primarySubdir, 0o755); err != nil {
		t.Fatal(err)
	}

	resolver := defaultResolver(t)
	primary := resolvedRepository(t, resolver, primarySubdir)
	linked := resolvedRepository(t, resolver, linkedPath)
	again := resolvedRepository(t, resolver, primaryPath)

	if primary.WorktreeRoot != primaryPath || linked.WorktreeRoot != linkedPath {
		t.Fatalf("roots = primary %q linked %q", primary.WorktreeRoot, linked.WorktreeRoot)
	}
	if primary.CommonRootID == "" || primary.CommonRootID != linked.CommonRootID {
		t.Fatalf("common identity mismatch: primary %q linked %q", primary.CommonRootID, linked.CommonRootID)
	}
	if primary.WorktreeID == linked.WorktreeID {
		t.Fatal("linked worktrees must have distinct worktree identities")
	}
	if primary.CommonRootID != again.CommonRootID || primary.WorktreeID != again.WorktreeID {
		t.Fatal("repository identity changed across repeated resolution")
	}
	if primary.Branch != "main" || primary.Detached {
		t.Fatalf("primary branch state = branch %q detached %v", primary.Branch, primary.Detached)
	}
	if linked.Branch != "" || !linked.Detached {
		t.Fatalf("linked branch state = branch %q detached %v", linked.Branch, linked.Detached)
	}
	if primary.HeadSHA != linked.HeadSHA || !validObjectID(primary.HeadSHA, primary.ObjectFormat) {
		t.Fatalf("HEAD mismatch: primary %q linked %q", primary.HeadSHA, linked.HeadSHA)
	}
	if primary.ObjectFormat != ObjectFormatSHA1 {
		t.Fatalf("object format = %q", primary.ObjectFormat)
	}

	binding := linked.Binding("server-entry-key-1")
	if binding.RepositoryEntryKey != "server-entry-key-1" || binding.WorktreeID != linked.WorktreeID {
		t.Fatalf("binding projection = %+v", binding)
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), linked.gitDir) || strings.Contains(string(raw), linked.commonDir) {
		t.Fatalf("binding leaked Git administrative path: %s", raw)
	}
}

func TestResolveDetachedHead(t *testing.T) {
	repositoryPath := createRepository(t, t.TempDir(), "detached")
	gitCommand(t, repositoryPath, "checkout", "--detach", "HEAD")
	repository := resolvedRepository(t, defaultResolver(t), repositoryPath)
	if !repository.Detached || repository.Branch != "" {
		t.Fatalf("detached state = branch %q detached %v", repository.Branch, repository.Detached)
	}
}

func TestResolveSHA256Repository(t *testing.T) {
	parent := t.TempDir()
	command := exec.Command("git", "init", "-b", "main", "--object-format=sha256", filepath.Join(parent, "sha256"))
	command.Dir = parent
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("Git SHA-256 repositories unsupported: %v (%s)", err, output)
	}
	repositoryPath := filepath.Join(parent, "sha256")
	if err := os.WriteFile(filepath.Join(repositoryPath, "tracked.txt"), []byte("sha256\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, repositoryPath, "add", "--", "tracked.txt")
	gitCommand(t, repositoryPath,
		"-c", "user.name=Fixture Author", "-c", "user.email=fixture@example.invalid",
		"commit", "-m", "sha256 fixture")
	repository := resolvedRepository(t, defaultResolver(t), repositoryPath)
	if repository.ObjectFormat != ObjectFormatSHA256 || len(repository.HeadSHA) != 64 {
		t.Fatalf("SHA-256 identity = format %q HEAD %q", repository.ObjectFormat, repository.HeadSHA)
	}
}

func TestResolveNonGitAndMissingDirectoryDegrade(t *testing.T) {
	resolver := defaultResolver(t)
	resolution, err := resolver.Resolve(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("non-Git directory returned process error: %v", err)
	}
	if resolution.Repository != nil || resolution.Assessment.State != model.GitEvidenceUnavailable || resolution.Assessment.ReasonCode != model.ReasonNotAGitRepository {
		t.Fatalf("non-Git degradation = %+v", resolution)
	}
	resolution, err = resolver.Resolve(context.Background(), filepath.Join(t.TempDir(), "missing"))
	if typed := typedError(t, err); typed.Code != ErrorInvalidWorkingDir {
		t.Fatalf("missing directory code = %q", typed.Code)
	}
	if resolution.Assessment.ReasonCode != model.ReasonRepositoryNotFound {
		t.Fatalf("missing directory reason = %q", resolution.Assessment.ReasonCode)
	}
}

func TestInjectionLookingWorktreePathIsData(t *testing.T) {
	parent := t.TempDir()
	repositoryPath := createRepository(t, parent, "repo;touch injected-marker")
	marker := filepath.Join(parent, "injected-marker")
	repository := resolvedRepository(t, defaultResolver(t), repositoryPath)
	if repository.WorktreeRoot != repositoryPath {
		t.Fatalf("worktree root = %q, want %q", repository.WorktreeRoot, repositoryPath)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("injection-looking path executed; marker stat = %v", err)
	}
}

func TestMalformedNestedGitEntryDoesNotFallBackToParent(t *testing.T) {
	for _, entryKind := range []string{"file", "directory"} {
		t.Run(entryKind, func(t *testing.T) {
			parent := t.TempDir()
			outer := createRepository(t, parent, "outer")
			nested := filepath.Join(outer, "nested")
			if err := os.MkdirAll(nested, 0o755); err != nil {
				t.Fatal(err)
			}
			entry := filepath.Join(nested, ".git")
			if entryKind == "file" {
				if err := os.WriteFile(entry, []byte("gitdir: /definitely/missing/session-insight-gitdir\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(entry, 0o755); err != nil {
				t.Fatal(err)
			}
			resolution, err := defaultResolver(t).Resolve(context.Background(), nested)
			if err == nil {
				t.Fatalf("malformed nested worktree silently resolved to parent: %+v", resolution)
			}
			wantReason := model.ReasonGitCommandFailed
			if entryKind == "directory" {
				wantReason = model.ReasonNotAGitRepository
			}
			if resolution.Repository != nil || resolution.Assessment.ReasonCode != wantReason {
				t.Fatalf("malformed nested worktree degradation = %+v", resolution)
			}
		})
	}
}

func TestResolveDoesNotMutateRepositoryOrInvokeConfiguredHelpers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	parent := t.TempDir()
	repositoryPath := createRepository(t, parent, "hostile-config")
	marker := filepath.Join(parent, "helper-invoked")
	helper := filepath.Join(parent, "hostile-helper.sh")
	script := "#!/bin/sh\nprintf invoked > '" + marker + "'\nexit 1\n"
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, repositoryPath, "config", "core.fsmonitor", helper)
	gitCommand(t, repositoryPath, "config", "diff.external", helper)
	gitCommand(t, repositoryPath, "config", "core.hooksPath", filepath.Join(parent, "hooks"))
	before := repositoryControlDigest(t, filepath.Join(repositoryPath, ".git"))
	_ = resolvedRepository(t, defaultResolver(t), repositoryPath)
	after := repositoryControlDigest(t, filepath.Join(repositoryPath, ".git"))
	if before != after {
		t.Fatalf("read-only resolution mutated repository: before %s after %s", before, after)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("configured helper executed; marker stat = %v", err)
	}
}

func repositoryControlDigest(t *testing.T, gitDir string) string {
	t.Helper()
	var files []string
	for _, name := range []string{"HEAD", "config", "index", "packed-refs", "refs", "objects"} {
		path := filepath.Join(gitDir, name)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		}
		err := filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() {
				files = append(files, current)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(files)
	digest := sha256.New()
	for _, path := range files {
		relative, err := filepath.Rel(gitDir, path)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = digest.Write([]byte(relative))
		_, _ = digest.Write([]byte{'\x00'})
		_, _ = digest.Write(data)
		_, _ = digest.Write([]byte{'\x00'})
	}
	return hex.EncodeToString(digest.Sum(nil))
}
