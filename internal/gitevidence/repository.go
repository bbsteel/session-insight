package gitevidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bbsteel/session-insight/internal/model"
)

type ObjectFormat string

const (
	ObjectFormatSHA1   ObjectFormat = "sha1"
	ObjectFormatSHA256 ObjectFormat = "sha256"
)

// Repository is a resolved, stable worktree identity. Git administrative
// paths remain private to this package and are represented publicly only by
// CommonRootID and WorktreeID hashes.
type Repository struct {
	WorktreeRoot string
	CommonRootID string
	WorktreeID   string
	Branch       string
	Detached     bool
	HeadSHA      string
	ObjectFormat ObjectFormat

	gitDir    string
	commonDir string
}

// Binding projects safe repository facts into the shared API model using a
// server-issued opaque repository entry key. The resolver never derives that
// client-facing key from a local path.
func (repository Repository) Binding(repositoryEntryKey string) model.GitRepositoryBinding {
	return model.GitRepositoryBinding{
		RepositoryEntryKey: repositoryEntryKey,
		WorktreeRoot:       repository.WorktreeRoot,
		CommonRootID:       repository.CommonRootID,
		WorktreeID:         repository.WorktreeID,
		Branch:             repository.Branch,
		HeadSHA:            repository.HeadSHA,
		Assessment:         model.ExactGitEvidence(),
	}
}

// Resolution always carries a stable evidence assessment. Expected states
// such as a non-Git directory return an unavailable assessment without a raw
// process error; execution failures return both this degradation and *Error.
type Resolution struct {
	Repository *Repository
	Assessment model.GitEvidenceAssessment
}

type Resolver struct {
	runner *Runner
}

func NewResolver(runner *Runner) (*Resolver, error) {
	if runner == nil {
		return nil, &Error{Code: ErrorInvalidConfig}
	}
	return &Resolver{runner: runner}, nil
}

func unavailable(reason model.GitEvidenceReasonCode) Resolution {
	return Resolution{Assessment: model.NonExactGitEvidence(model.GitEvidenceUnavailable, reason)}
}

func resolutionForError(err error) Resolution {
	var typed *Error
	if errors.As(err, &typed) {
		return unavailable(typed.EvidenceReason())
	}
	return unavailable(model.ReasonGitCommandFailed)
}

// Resolve discovers one existing worktree from an absolute start directory.
// It never walks into a parent repository when a nested .git entry is present
// but malformed: Git remains the authority for that entry and the failure is
// returned as typed degradation.
func (resolver *Resolver) Resolve(ctx context.Context, start string) (Resolution, error) {
	if resolver == nil || resolver.runner == nil {
		err := &Error{Code: ErrorInvalidConfig}
		return resolutionForError(err), err
	}
	canonicalStart, err := canonicalDirectory(start)
	if err != nil {
		typed := &Error{Code: ErrorInvalidWorkingDir, Cause: err}
		return resolutionForError(typed), typed
	}
	hasRepository, malformedShadow := inspectGitEntries(canonicalStart)
	if malformedShadow {
		typed := &Error{Code: ErrorNotRepository, Operation: OperationInsideWorktree}
		return resolutionForError(typed), typed
	}
	if !hasRepository {
		return unavailable(model.ReasonNotAGitRepository), nil
	}

	insideResult, err := resolver.runner.Run(ctx, canonicalStart, OperationInsideWorktree)
	if err != nil {
		return resolutionForError(err), err
	}
	inside, err := parseBool(OperationInsideWorktree, insideResult.Stdout)
	if err != nil {
		return resolutionForError(err), err
	}
	if !inside {
		return unavailable(model.ReasonNotAGitRepository), nil
	}

	worktreeRoot, err := resolver.runCanonicalDirectory(ctx, canonicalStart, OperationWorktreeRoot)
	if err != nil {
		return resolutionForError(err), err
	}
	if !containsPath(worktreeRoot, canonicalStart) {
		typed := &Error{Code: ErrorMalformedOutput, Operation: OperationWorktreeRoot}
		return resolutionForError(typed), typed
	}
	gitDir, err := resolver.runCanonicalDirectory(ctx, canonicalStart, OperationGitDir)
	if err != nil {
		return resolutionForError(err), err
	}
	commonDir, err := resolver.runCanonicalDirectory(ctx, canonicalStart, OperationCommonDir)
	if err != nil {
		return resolutionForError(err), err
	}

	formatLine, err := resolver.runLine(ctx, canonicalStart, OperationObjectFormat)
	if err != nil {
		return resolutionForError(err), err
	}
	format := ObjectFormat(formatLine)
	if format != ObjectFormatSHA1 && format != ObjectFormatSHA256 {
		typed := &Error{Code: ErrorUnsupportedFormat, Operation: OperationObjectFormat}
		return resolutionForError(typed), typed
	}
	head, err := resolver.runLine(ctx, canonicalStart, OperationHead)
	if err != nil {
		return resolutionForError(err), err
	}
	if !validObjectID(head, format) {
		typed := &Error{Code: ErrorMalformedOutput, Operation: OperationHead}
		return resolutionForError(typed), typed
	}

	branch := ""
	detached := false
	branchResult, branchErr := resolver.runner.Run(ctx, canonicalStart, OperationBranch)
	if branchErr != nil {
		var typed *Error
		if errors.As(branchErr, &typed) && typed.Code == ErrorCommandFailed && typed.ExitCode == 1 {
			detached = true
		} else {
			return resolutionForError(branchErr), branchErr
		}
	} else {
		branch, err = parseSingleLine(OperationBranch, branchResult.Stdout)
		if err != nil {
			return resolutionForError(err), err
		}
	}

	repository := &Repository{
		WorktreeRoot: worktreeRoot,
		CommonRootID: hashIdentity("common", commonDir),
		WorktreeID:   hashIdentity("worktree", commonDir, gitDir, worktreeRoot),
		Branch:       branch,
		Detached:     detached,
		HeadSHA:      head,
		ObjectFormat: format,
		gitDir:       gitDir,
		commonDir:    commonDir,
	}
	return Resolution{Repository: repository, Assessment: model.ExactGitEvidence()}, nil
}

func (resolver *Resolver) runLine(ctx context.Context, cwd string, operation Operation) (string, error) {
	result, err := resolver.runner.Run(ctx, cwd, operation)
	if err != nil {
		return "", err
	}
	return parseSingleLine(operation, result.Stdout)
}

func (resolver *Resolver) runCanonicalDirectory(ctx context.Context, cwd string, operation Operation) (string, error) {
	value, err := resolver.runLine(ctx, cwd, operation)
	if err != nil {
		return "", err
	}
	canonical, err := canonicalDirectory(value)
	if err != nil {
		return "", &Error{Code: ErrorMalformedOutput, Operation: operation, Cause: err}
	}
	return canonical, nil
}

func canonicalDirectory(path string) (string, error) {
	if path == "" || strings.ContainsRune(path, '\x00') || !filepath.IsAbs(path) {
		return "", &Error{Code: ErrorInvalidWorkingDir}
	}
	absolute := filepath.Clean(path)
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory")
	}
	return filepath.Clean(canonical), nil
}

func inspectGitEntries(start string) (hasRepository, malformedShadow bool) {
	current := start
	malformedDirectory := false
	for {
		entry := filepath.Join(current, ".git")
		if info, err := os.Lstat(entry); err == nil {
			if !info.IsDir() || looksLikeGitDirectory(entry) {
				// Files and symlinks are valid worktree shapes whose target Git
				// must validate. A malformed directory below a real parent entry
				// is instead an explicit shadowing boundary.
				if malformedDirectory {
					return false, true
				}
				return true, false
			}
			malformedDirectory = true
		}
		parent := filepath.Dir(current)
		if parent == current {
			// Some sandboxes use an empty .git directory as a filesystem
			// marker. Without a real parent repository it is simply non-Git.
			return false, false
		}
		current = parent
	}
}

func looksLikeGitDirectory(path string) bool {
	if info, err := os.Stat(filepath.Join(path, "HEAD")); err != nil || info.IsDir() {
		return false
	}
	if info, err := os.Stat(filepath.Join(path, "objects")); err != nil || !info.IsDir() {
		return false
	}
	return true
}

func containsPath(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func validObjectID(value string, format ObjectFormat) bool {
	want := 40
	if format == ObjectFormatSHA256 {
		want = 64
	}
	if len(value) != want {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func hashIdentity(kind string, values ...string) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("session-insight/git-identity/v1\x00"))
	_, _ = digest.Write([]byte(kind))
	for _, value := range values {
		_, _ = digest.Write([]byte{'\x00'})
		_, _ = digest.Write([]byte(value))
	}
	return kind + ":sha256:" + hex.EncodeToString(digest.Sum(nil))
}
