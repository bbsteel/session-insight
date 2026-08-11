package gitevidence

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// Operation is a closed set of read-only Git invocations. Run accepts no raw
// argv, flags, revisions, paths, or subcommands from callers.
type Operation string

const (
	OperationInsideWorktree Operation = "inside_worktree"
	OperationWorktreeRoot   Operation = "worktree_root"
	OperationGitDir         Operation = "git_dir"
	OperationCommonDir      Operation = "common_dir"
	OperationObjectFormat   Operation = "object_format"
	OperationHead           Operation = "head"
	OperationBranch         Operation = "branch"
	OperationIndexState     Operation = "index_state"
	OperationStatusState    Operation = "status_state"
	OperationSnapshotPaths  Operation = "snapshot_paths"
)

func operations() []Operation {
	return []Operation{
		OperationInsideWorktree,
		OperationWorktreeRoot,
		OperationGitDir,
		OperationCommonDir,
		OperationObjectFormat,
		OperationHead,
		OperationBranch,
		OperationIndexState,
		OperationStatusState,
		OperationSnapshotPaths,
	}
}

func isKnownOperation(operation Operation) bool {
	for _, known := range operations() {
		if operation == known {
			return true
		}
	}
	return false
}

// ErrorCode is a stable, non-localized runner or resolver failure.
type ErrorCode string

const (
	ErrorInvalidConfig       ErrorCode = "invalid_config"
	ErrorInvalidOperation    ErrorCode = "invalid_operation"
	ErrorInvalidWorkingDir   ErrorCode = "invalid_working_directory"
	ErrorStartFailed         ErrorCode = "start_failed"
	ErrorCommandFailed       ErrorCode = "command_failed"
	ErrorTimedOut            ErrorCode = "timed_out"
	ErrorOutputLimitExceeded ErrorCode = "output_limit_exceeded"
	ErrorMalformedOutput     ErrorCode = "malformed_output"
	ErrorNotRepository       ErrorCode = "not_a_git_repository"
	ErrorUnsupportedFormat   ErrorCode = "unsupported_object_format"
)

// Error exposes only typed, bounded metadata. Error() intentionally omits the
// working directory, argv, Git stderr, and wrapped error text.
type Error struct {
	Code      ErrorCode
	Operation Operation
	ExitCode  int
	Cause     error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Operation == "" {
		return fmt.Sprintf("Git evidence failed: %s", e.Code)
	}
	return fmt.Sprintf("Git evidence %s failed: %s", e.Operation, e.Code)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// EvidenceReason maps local runner failures onto the frozen Git evidence
// reason vocabulary without leaking raw process output.
func (e *Error) EvidenceReason() model.GitEvidenceReasonCode {
	if e == nil {
		return model.ReasonGitCommandFailed
	}
	switch e.Code {
	case ErrorInvalidWorkingDir:
		return model.ReasonRepositoryNotFound
	case ErrorNotRepository:
		return model.ReasonNotAGitRepository
	case ErrorTimedOut:
		return model.ReasonGitCommandTimedOut
	default:
		return model.ReasonGitCommandFailed
	}
}

type Limits struct {
	Timeout     time.Duration
	StdoutBytes int64
	StderrBytes int64
}

// Config is copied by NewRunner. Timeouts may override the default for a
// specific closed Operation; every configured duration and byte cap must be
// positive.
type Config struct {
	Binary   string
	Default  Limits
	Timeouts map[Operation]time.Duration
}

func DefaultConfig() Config {
	return Config{
		Binary: "git",
		Default: Limits{
			Timeout:     3 * time.Second,
			StdoutBytes: 1 << 20,
			StderrBytes: 64 << 10,
		},
		Timeouts: map[Operation]time.Duration{},
	}
}

type commandFactory func(ctx context.Context, binary string, args ...string) *exec.Cmd

type Runner struct {
	binary   string
	limits   Limits
	timeouts map[Operation]time.Duration
	command  commandFactory
}

func NewRunner(config Config) (*Runner, error) {
	if config.Binary == "" || config.Default.Timeout <= 0 || config.Default.StdoutBytes <= 0 || config.Default.StderrBytes <= 0 {
		return nil, &Error{Code: ErrorInvalidConfig}
	}
	binary, err := exec.LookPath(config.Binary)
	if err != nil {
		return nil, &Error{Code: ErrorInvalidConfig, Cause: err}
	}
	if absolute, err := filepath.Abs(binary); err == nil {
		binary = filepath.Clean(absolute)
	}
	timeouts := make(map[Operation]time.Duration, len(config.Timeouts))
	for operation, timeout := range config.Timeouts {
		if !isKnownOperation(operation) || timeout <= 0 {
			return nil, &Error{Code: ErrorInvalidConfig, Operation: operation}
		}
		timeouts[operation] = timeout
	}
	return &Runner{
		binary: binary, limits: config.Default, timeouts: timeouts,
		command: exec.CommandContext,
	}, nil
}

// Result contains bounded stdout only. Stderr is consumed with an independent
// cap but never returned, logged, or included in typed errors.
type Result struct {
	Stdout []byte
}

type cappedBuffer struct {
	buffer   bytes.Buffer
	limit    int64
	exceeded bool
}

func (w *cappedBuffer) Write(p []byte) (int, error) {
	remaining := w.limit - int64(w.buffer.Len())
	if remaining > 0 {
		keep := int64(len(p))
		if keep > remaining {
			keep = remaining
		}
		_, _ = w.buffer.Write(p[:keep])
	}
	if int64(len(p)) > remaining {
		w.exceeded = true
	}
	// Consume excess bytes so a noisy child cannot deadlock on a full pipe.
	return len(p), nil
}

func (r *Runner) timeout(operation Operation) time.Duration {
	if timeout, ok := r.timeouts[operation]; ok {
		return timeout
	}
	return r.limits.Timeout
}

func operationArgs(operation Operation) ([]string, bool) {
	switch operation {
	case OperationInsideWorktree:
		return []string{"rev-parse", "--is-inside-work-tree"}, true
	case OperationWorktreeRoot:
		return []string{"rev-parse", "--path-format=absolute", "--show-toplevel"}, true
	case OperationGitDir:
		return []string{"rev-parse", "--path-format=absolute", "--absolute-git-dir"}, true
	case OperationCommonDir:
		return []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}, true
	case OperationObjectFormat:
		return []string{"rev-parse", "--show-object-format=storage"}, true
	case OperationHead:
		return []string{"rev-parse", "--verify", "--end-of-options", "HEAD^{commit}"}, true
	case OperationBranch:
		return []string{"symbolic-ref", "--quiet", "--short", "HEAD"}, true
	case OperationIndexState:
		return []string{"ls-files", "--stage", "--full-name", "-z"}, true
	case OperationStatusState:
		return []string{"status", "--porcelain=v2", "-z", "--untracked-files=all", "--no-renames", "--ignore-submodules=none"}, true
	case OperationSnapshotPaths:
		return []string{"ls-files", "--cached", "--others", "--exclude-standard", "--full-name", "-z"}, true
	default:
		return nil, false
	}
}

func fixedArgs(operation Operation) ([]string, bool) {
	operationArgv, ok := operationArgs(operation)
	if !ok {
		return nil, false
	}
	argv := []string{
		"--no-pager",
		"--no-optional-locks",
		"-c", "diff.external=",
		"-c", "status.renames=false",
		"-c", "core.fsmonitor=false",
		"-c", "maintenance.auto=false",
		"-c", "gc.auto=0",
		"-c", "credential.helper=",
		"-c", "core.hooksPath=" + os.DevNull,
	}
	return append(argv, operationArgv...), true
}

func commandEnvironment() []string {
	// Preserve only process-launch basics. Git-specific and locale variables
	// are then supplied explicitly, so caller-controlled GIT_* values, pagers,
	// credential prompts, alternates, replace refs, lazy fetch and config paths
	// cannot change command behavior.
	allowed := map[string]bool{
		"PATH": true, "TMPDIR": true, "TMP": true, "TEMP": true,
	}
	if runtime.GOOS == "windows" {
		allowed["SystemRoot"] = true
		allowed["SystemDrive"] = true
		allowed["ComSpec"] = true
		allowed["PATHEXT"] = true
	}
	environment := make([]string, 0, len(allowed)+12)
	for _, pair := range os.Environ() {
		key, _, ok := strings.Cut(pair, "=")
		if ok && allowed[key] {
			environment = append(environment, pair)
		}
	}
	environment = append(environment,
		"LC_ALL=C",
		"LANG=C",
		"GIT_PAGER=cat",
		"PAGER=cat",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_NO_LAZY_FETCH=1",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_LITERAL_PATHSPECS=1",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_ATTR_NOSYSTEM=1",
	)
	return environment
}

func validateWorkingDirectory(cwd string) (string, error) {
	if cwd == "" || strings.ContainsRune(cwd, '\x00') || !filepath.IsAbs(cwd) {
		return "", &Error{Code: ErrorInvalidWorkingDir}
	}
	cleaned := filepath.Clean(cwd)
	info, err := os.Stat(cleaned)
	if err != nil || !info.IsDir() {
		return "", &Error{Code: ErrorInvalidWorkingDir, Cause: err}
	}
	return cleaned, nil
}

// Run executes exactly one allowlisted read-only operation in cwd. It never
// invokes a shell and never accepts user-controlled Git arguments.
func (r *Runner) Run(ctx context.Context, cwd string, operation Operation) (Result, error) {
	if r == nil {
		return Result{}, &Error{Code: ErrorInvalidConfig, Operation: operation}
	}
	argv, ok := fixedArgs(operation)
	if !ok {
		return Result{}, &Error{Code: ErrorInvalidOperation, Operation: operation}
	}
	workingDirectory, err := validateWorkingDirectory(cwd)
	if err != nil {
		return Result{}, withOperation(err, operation)
	}
	runContext, cancel := context.WithTimeout(ctx, r.timeout(operation))
	defer cancel()
	command := r.command(runContext, r.binary, argv...)
	command.Dir = workingDirectory
	command.Env = commandEnvironment()
	command.WaitDelay = 250 * time.Millisecond
	stdout := &cappedBuffer{limit: r.limits.StdoutBytes}
	stderr := &cappedBuffer{limit: r.limits.StderrBytes}
	command.Stdout = stdout
	command.Stderr = stderr
	err = command.Run()
	if errors.Is(runContext.Err(), context.DeadlineExceeded) {
		return Result{}, &Error{Code: ErrorTimedOut, Operation: operation, ExitCode: -1, Cause: runContext.Err()}
	}
	if stdout.exceeded || stderr.exceeded {
		return Result{}, &Error{Code: ErrorOutputLimitExceeded, Operation: operation, ExitCode: exitCode(err), Cause: err}
	}
	if err != nil {
		code := ErrorCommandFailed
		var pathError *os.PathError
		if errors.As(err, &pathError) {
			code = ErrorStartFailed
		}
		return Result{}, &Error{Code: code, Operation: operation, ExitCode: exitCode(err), Cause: err}
	}
	return Result{Stdout: append([]byte(nil), stdout.buffer.Bytes()...)}, nil
}

func withOperation(err error, operation Operation) error {
	var typed *Error
	if errors.As(err, &typed) {
		return &Error{Code: typed.Code, Operation: operation, ExitCode: typed.ExitCode, Cause: typed.Cause}
	}
	return &Error{Code: ErrorCommandFailed, Operation: operation, Cause: err}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return exitError.ExitCode()
	}
	return -1
}

// parseSingleLine accepts one LF-terminated or unterminated line. Rejecting
// embedded CR/LF avoids ambiguous path/object parsing and raw output leakage.
func parseSingleLine(operation Operation, raw []byte) (string, error) {
	value := string(raw)
	value = strings.TrimSuffix(value, "\n")
	value = strings.TrimSuffix(value, "\r")
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", &Error{Code: ErrorMalformedOutput, Operation: operation}
	}
	return value, nil
}

func parseBool(operation Operation, raw []byte) (bool, error) {
	value, err := parseSingleLine(operation, raw)
	if err != nil {
		return false, err
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, &Error{Code: ErrorMalformedOutput, Operation: operation, Cause: err}
	}
	return parsed, nil
}
