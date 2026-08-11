package gitevidence

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

type helperObservation struct {
	Args []string `json:"args"`
	Env  []string `json:"env"`
}

func TestGitHelperProcess(t *testing.T) {
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	mode := os.Args[separator+1]
	gitArgs := os.Args[separator+2:]
	switch mode {
	case "observe":
		if err := json.NewEncoder(os.Stdout).Encode(helperObservation{Args: gitArgs, Env: os.Environ()}); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	case "sleep":
		time.Sleep(5 * time.Second)
		os.Exit(0)
	case "oversize":
		_, _ = os.Stdout.WriteString(strings.Repeat("o", 4096))
		_, _ = os.Stderr.WriteString(strings.Repeat("e", 4096))
		os.Exit(0)
	case "secret-error":
		_, _ = os.Stderr.WriteString("https://token@example.invalid/repo?access_token=secret")
		os.Exit(9)
	default:
		os.Exit(3)
	}
}

func helperFactory(mode string) commandFactory {
	return func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		helperArgs := []string{"-test.run=^TestGitHelperProcess$", "--", mode}
		helperArgs = append(helperArgs, args...)
		return exec.CommandContext(ctx, os.Args[0], helperArgs...)
	}
}

func testRunner(t *testing.T, mutate func(*Config)) *Runner {
	t.Helper()
	config := DefaultConfig()
	if mutate != nil {
		mutate(&config)
	}
	runner, err := NewRunner(config)
	if err != nil {
		t.Skipf("Git unavailable: %v", err)
	}
	return runner
}

func typedError(t *testing.T, err error) *Error {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) {
		t.Fatalf("error %T is not *gitevidence.Error: %v", err, err)
	}
	return typed
}

func TestRunnerUsesFixedArgvAndIsolatedEnvironment(t *testing.T) {
	t.Setenv("GIT_DIR", "/tmp/should-not-leak")
	t.Setenv("GIT_EXEC_PATH", "/tmp/should-not-leak")
	t.Setenv("GIT_CONFIG_COUNT", "99")
	t.Setenv("PAGER", "untrusted-pager")
	t.Setenv("LC_ALL", "untrusted-locale")

	runner := testRunner(t, nil)
	runner.command = helperFactory("observe")
	result, err := runner.Run(context.Background(), t.TempDir(), OperationHead)
	if err != nil {
		t.Fatal(err)
	}
	var observation helperObservation
	if err := json.Unmarshal(result.Stdout, &observation); err != nil {
		t.Fatalf("decode helper observation: %v", err)
	}
	wantArgs, _ := fixedArgs(OperationHead)
	if !reflect.DeepEqual(observation.Args, wantArgs) {
		t.Fatalf("argv drift:\n got %q\nwant %q", observation.Args, wantArgs)
	}
	environment := map[string]string{}
	for _, pair := range observation.Env {
		key, value, _ := strings.Cut(pair, "=")
		environment[key] = value
	}
	for key, want := range map[string]string{
		"LC_ALL": "C", "LANG": "C", "GIT_PAGER": "cat", "PAGER": "cat",
		"GIT_OPTIONAL_LOCKS": "0", "GIT_TERMINAL_PROMPT": "0",
		"GIT_NO_LAZY_FETCH": "1", "GIT_NO_REPLACE_OBJECTS": "1",
		"GIT_LITERAL_PATHSPECS": "1", "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_CONFIG_SYSTEM": os.DevNull, "GIT_CONFIG_GLOBAL": os.DevNull,
		"GIT_ATTR_NOSYSTEM": "1",
	} {
		if environment[key] != want {
			t.Errorf("environment %s = %q, want %q", key, environment[key], want)
		}
	}
	for _, forbidden := range []string{"GIT_DIR", "GIT_EXEC_PATH", "GIT_CONFIG_COUNT", "HOME"} {
		if _, exists := environment[forbidden]; exists {
			t.Errorf("caller-controlled environment %s leaked to Git", forbidden)
		}
	}
}

func TestSnapshotOperationsUseOnlyFixedReadOnlyArgv(t *testing.T) {
	for operation, suffix := range map[Operation][]string{
		OperationIndexState:    {"ls-files", "--stage", "--full-name", "-z"},
		OperationStatusState:   {"status", "--porcelain=v2", "-z", "--untracked-files=all", "--no-renames", "--ignore-submodules=none"},
		OperationSnapshotPaths: {"ls-files", "--cached", "--others", "--exclude-standard", "--full-name", "-z"},
	} {
		t.Run(string(operation), func(t *testing.T) {
			argv, ok := fixedArgs(operation)
			if !ok {
				t.Fatalf("operation %q is not allowlisted", operation)
			}
			if len(argv) < len(suffix) || !reflect.DeepEqual(argv[len(argv)-len(suffix):], suffix) {
				t.Fatalf("argv suffix = %q, want %q", argv, suffix)
			}
			joined := strings.Join(argv, " ")
			for _, required := range []string{"--no-pager", "--no-optional-locks", "diff.external=", "status.renames=false", "core.fsmonitor=false", "credential.helper="} {
				if !strings.Contains(joined, required) {
					t.Errorf("argv %q is missing %q", argv, required)
				}
			}
		})
	}
}

func TestRunnerRejectsInjectionLookingOperationWithoutStartingProcess(t *testing.T) {
	runner := testRunner(t, nil)
	started := false
	runner.command = func(context.Context, string, ...string) *exec.Cmd {
		started = true
		return nil
	}
	_, err := runner.Run(context.Background(), t.TempDir(), Operation("status; touch /tmp/pwned"))
	if typed := typedError(t, err); typed.Code != ErrorInvalidOperation {
		t.Fatalf("code = %q, want %q", typed.Code, ErrorInvalidOperation)
	}
	if started {
		t.Fatal("invalid operation reached the process boundary")
	}
}

func TestRunnerTimesOutAndKillsCommand(t *testing.T) {
	runner := testRunner(t, func(config *Config) {
		config.Default.Timeout = 60 * time.Millisecond
	})
	runner.command = helperFactory("sleep")
	started := time.Now()
	_, err := runner.Run(context.Background(), t.TempDir(), OperationHead)
	if typed := typedError(t, err); typed.Code != ErrorTimedOut {
		t.Fatalf("code = %q, want %q", typed.Code, ErrorTimedOut)
	} else if typed.EvidenceReason() != model.ReasonGitCommandTimedOut {
		t.Fatalf("reason = %q", typed.EvidenceReason())
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("timeout took %s", elapsed)
	}
}

func TestRunnerCapsStdoutAndStderr(t *testing.T) {
	runner := testRunner(t, func(config *Config) {
		config.Default.StdoutBytes = 32
		config.Default.StderrBytes = 16
	})
	runner.command = helperFactory("oversize")
	_, err := runner.Run(context.Background(), t.TempDir(), OperationHead)
	if typed := typedError(t, err); typed.Code != ErrorOutputLimitExceeded {
		t.Fatalf("code = %q, want %q", typed.Code, ErrorOutputLimitExceeded)
	}
}

func TestRunnerErrorNeverIncludesStderr(t *testing.T) {
	runner := testRunner(t, nil)
	runner.command = helperFactory("secret-error")
	_, err := runner.Run(context.Background(), t.TempDir(), OperationHead)
	typed := typedError(t, err)
	if typed.Code != ErrorCommandFailed || typed.ExitCode != 9 {
		t.Fatalf("typed error = %+v", typed)
	}
	if strings.Contains(typed.Error(), "secret") || strings.Contains(typed.Error(), "example.invalid") {
		t.Fatalf("typed error leaked stderr: %q", typed.Error())
	}
}

func TestCandidateRunnerKeepsLiteralPathAfterRevisionSeparator(t *testing.T) {
	runner := testRunner(t, nil)
	runner.command = helperFactory("observe")
	maliciousPath := "--all; touch candidate-injected"
	result, err := runner.commitLog(context.Background(), t.TempDir(), commitLogRequest{
		mode: commitLogPaths, final: strings.Repeat("a", 40),
		paths: []string{maliciousPath}, since: time.Unix(100, 0), until: time.Unix(200, 0), limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	var observation helperObservation
	if err := json.Unmarshal(result.Stdout, &observation); err != nil {
		t.Fatalf("decode helper observation: %v", err)
	}
	separator := -1
	for index, arg := range observation.Args {
		if arg == "--" {
			separator = index
		}
	}
	if separator < 0 || separator+1 >= len(observation.Args) || observation.Args[separator+1] != maliciousPath {
		t.Fatalf("literal path was not isolated after --: %q", observation.Args)
	}
	if observation.Args[len(observation.Args)-1] != maliciousPath {
		t.Fatalf("unexpected argv after literal path: %q", observation.Args)
	}
	for _, required := range []string{"--no-textconv", "--no-ext-diff", "--no-show-signature"} {
		found := false
		for _, arg := range observation.Args {
			if arg == required {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("candidate argv omitted %s: %q", required, observation.Args)
		}
	}
	for _, arg := range observation.Args {
		if strings.Contains(arg, "%ae") || strings.Contains(arg, "%aE") || strings.Contains(arg, "%ce") || strings.Contains(arg, "%cE") {
			t.Fatalf("candidate format requested author/committer email: %q", observation.Args)
		}
	}
}

func TestCandidateRunnerRejectsUnvalidatedDynamicArguments(t *testing.T) {
	runner := testRunner(t, nil)
	started := false
	runner.command = func(context.Context, string, ...string) *exec.Cmd {
		started = true
		return nil
	}
	for name, request := range map[string]commitLogRequest{
		"revision": {
			mode: commitLogWindow, final: "HEAD;touch-pwned",
			since: time.Unix(100, 0), until: time.Unix(200, 0), limit: 2,
		},
		"path": {
			mode: commitLogPaths, final: strings.Repeat("a", 40), paths: []string{"../outside"},
			since: time.Unix(100, 0), until: time.Unix(200, 0), limit: 2,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runner.commitLog(context.Background(), t.TempDir(), request)
			if typed := typedError(t, err); typed.Code != ErrorInvalidInput {
				t.Fatalf("code = %q, want %q", typed.Code, ErrorInvalidInput)
			}
		})
	}
	if started {
		t.Fatal("invalid dynamic argument reached the process boundary")
	}
}

func TestCandidateRunnerUsesOperationTimeout(t *testing.T) {
	runner := testRunner(t, func(config *Config) {
		config.Timeouts[OperationCommitWindow] = 60 * time.Millisecond
	})
	runner.command = helperFactory("sleep")
	started := time.Now()
	_, err := runner.commitLog(context.Background(), t.TempDir(), commitLogRequest{
		mode: commitLogWindow, final: strings.Repeat("a", 40),
		since: time.Unix(100, 0), until: time.Unix(200, 0), limit: 2,
	})
	if typed := typedError(t, err); typed.Code != ErrorTimedOut || typed.Operation != OperationCommitWindow {
		t.Fatalf("typed error = %+v", typed)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("candidate timeout took %s", elapsed)
	}
}

func TestCandidateRunnerUsesSharedOutputCaps(t *testing.T) {
	runner := testRunner(t, func(config *Config) {
		config.Default.StdoutBytes = 32
		config.Default.StderrBytes = 16
	})
	runner.command = helperFactory("oversize")
	_, err := runner.commitLog(context.Background(), t.TempDir(), commitLogRequest{
		mode: commitLogWindow, final: strings.Repeat("a", 40),
		since: time.Unix(100, 0), until: time.Unix(200, 0), limit: 2,
	})
	if typed := typedError(t, err); typed.Code != ErrorOutputLimitExceeded || typed.Operation != OperationCommitWindow {
		t.Fatalf("typed error = %+v", typed)
	}
}

func TestNewRunnerRejectsInvalidConfiguration(t *testing.T) {
	config := DefaultConfig()
	config.Default.StdoutBytes = 0
	if _, err := NewRunner(config); typedError(t, err).Code != ErrorInvalidConfig {
		t.Fatal("zero output cap accepted")
	}
	config = DefaultConfig()
	config.Timeouts[Operation("fetch")] = time.Second
	if _, err := NewRunner(config); typedError(t, err).Code != ErrorInvalidConfig {
		t.Fatal("unknown per-command timeout accepted")
	}
}
