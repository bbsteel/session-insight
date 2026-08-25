package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
)

const (
	baselineRef        = "origin/main"
	evidenceLockRelFmt = "internal/reader/%s/presentation.evidence.json"
)

// GitBaseline is the locally available origin/main pointer. The manager never
// fetches remotes unless the user explicitly asks.
type GitBaseline struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

type evidenceLockFile struct {
	SchemaVersion int                         `json:"schema_version"`
	AgentType     string                      `json:"agent_type"`
	Captures      map[string]evidenceLockItem `json:"captures"`
}

type evidenceLockItem struct {
	CurrentSHA256 string   `json:"current_sha256"`
	EvidenceID    string   `json:"evidence_id"`
	FeatureIDs    []string `json:"feature_ids"`
	Claims        []string `json:"claims"`
}

func normalizeSHA256(value string) string {
	s := strings.TrimSpace(strings.ToLower(value))
	s = strings.TrimPrefix(s, "sha256:")
	return s
}

func evidenceLockPath(agent string) string {
	return fmt.Sprintf(evidenceLockRelFmt, agent)
}

// lookupBaseline returns the local origin/main commit. Tests replace this.
var lookupBaseline = readGitBaseline

func readGitBaseline(checkoutDir string) (GitBaseline, error) {
	sha, err := gitOutput(checkoutDir, "rev-parse", "--verify", baselineRef)
	if err != nil {
		return GitBaseline{}, fmt.Errorf("%s is not readable locally: %w", baselineRef, err)
	}
	return GitBaseline{Ref: baselineRef, SHA: sha}, nil
}

// loadAgentLock reads the evidence lock at ref for agent. A missing file is
// an empty lock (nothing used yet), not an unreadable baseline.
var loadAgentLock = readAgentEvidenceLock

func emptyEvidenceLock(agent string) *evidenceLockFile {
	return &evidenceLockFile{SchemaVersion: 1, AgentType: agent, Captures: map[string]evidenceLockItem{}}
}

func readAgentEvidenceLock(checkoutDir, ref, agent string) (*evidenceLockFile, error) {
	if ref == "" {
		return nil, fmt.Errorf("empty git ref")
	}
	refResult, err := gitBatchCheck(checkoutDir, ref)
	if err != nil {
		return nil, fmt.Errorf("git ref %s is unreadable: %w", ref, err)
	}
	if !refResult.Exists {
		return nil, fmt.Errorf("git ref %s is unreadable", ref)
	}
	spec := ref + ":" + evidenceLockPath(agent)
	exists, err := gitObjectExists(checkoutDir, spec)
	if err != nil {
		return nil, err
	}
	if !exists {
		return emptyEvidenceLock(agent), nil
	}
	data, err := gitBatchShow(checkoutDir, spec)
	if err != nil {
		return nil, err
	}
	var lock evidenceLockFile
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("evidence lock at %s:%s is not JSON: %w", ref, evidenceLockPath(agent), err)
	}
	if lock.Captures == nil {
		lock.Captures = map[string]evidenceLockItem{}
	}
	if lock.AgentType == "" {
		lock.AgentType = agent
	}
	return &lock, nil
}

func lockHashesByLogical(lock *evidenceLockFile) map[string]string {
	out := map[string]string{}
	if lock == nil {
		return out
	}
	for key, item := range lock.Captures {
		hash := normalizeSHA256(item.CurrentSHA256)
		if hash == "" {
			continue
		}
		logical := strings.TrimSuffix(key, path.Ext(key))
		if logical == "" {
			logical = key
		}
		out[logical] = hash
		out[key] = hash
	}
	return out
}

func lockHashFor(lockHashes map[string]string, logical, ext string) string {
	if lockHashes == nil {
		return ""
	}
	if hash := lockHashes[logical]; hash != "" {
		return hash
	}
	if ext != "" {
		if hash := lockHashes[logical+ext]; hash != "" {
			return hash
		}
	}
	if hash := lockHashes[logical+".png"]; hash != "" {
		return hash
	}
	return ""
}

func gitOutput(checkoutDir string, args ...string) (string, error) {
	data, err := gitOutputBytes(checkoutDir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func gitEnv() []string {
	return append(os.Environ(), "LC_ALL=C", "LANG=C", "LANGUAGE=C")
}

func gitCommand(checkoutDir string, args ...string) *exec.Cmd {
	return gitCommandContext(context.Background(), checkoutDir, args...)
}

func gitCommandContext(ctx context.Context, checkoutDir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	if checkoutDir != "" {
		cmd.Dir = checkoutDir
	}
	cmd.Env = gitEnv()
	return cmd
}

func gitObjectExists(checkoutDir, spec string) (bool, error) {
	result, err := gitBatchCheck(checkoutDir, spec)
	if err != nil {
		return false, err
	}
	return result.Exists, nil
}

type gitBatchCheckResult struct {
	ObjectType string
	Exists     bool
}

func gitBatchCheck(checkoutDir, spec string) (gitBatchCheckResult, error) {
	return gitBatchCheckContext(context.Background(), checkoutDir, spec)
}

func gitBatchCheckContext(ctx context.Context, checkoutDir, spec string) (gitBatchCheckResult, error) {
	if strings.ContainsAny(spec, "\r\n") {
		return gitBatchCheckResult{}, fmt.Errorf("invalid git object name")
	}
	cmd := gitCommandContext(ctx, checkoutDir, "cat-file", "--batch-check")
	cmd.Stdin = strings.NewReader(spec + "\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return gitBatchCheckResult{}, gitCommandError(err, stderr.String())
	}
	responseLine, err := bufio.NewReader(&stdout).ReadString('\n')
	if err != nil {
		return gitBatchCheckResult{}, fmt.Errorf("read git object status: %w", err)
	}
	responseFields := strings.SplitN(strings.TrimSuffix(responseLine, "\n"), " ", 3)
	if len(responseFields) < 2 {
		return gitBatchCheckResult{}, fmt.Errorf("unexpected git object status")
	}
	if responseFields[1] == "missing" {
		if err := verifyGitRepository(ctx, checkoutDir); err != nil {
			return gitBatchCheckResult{}, err
		}
		return gitBatchCheckResult{}, nil
	}
	if responseFields[1] == "ambiguous" {
		return gitBatchCheckResult{}, fmt.Errorf("git object name is ambiguous")
	}
	if len(responseFields) != 3 {
		return gitBatchCheckResult{}, fmt.Errorf("unexpected git object status")
	}
	return gitBatchCheckResult{ObjectType: responseFields[1], Exists: true}, nil
}

func verifyGitRepository(ctx context.Context, checkoutDir string) error {
	_, err := gitOutputBytesContext(ctx, checkoutDir, "rev-parse", "--git-dir")
	return err
}

func gitBatchShow(checkoutDir, spec string) ([]byte, error) {
	return gitBatchShowContext(context.Background(), checkoutDir, spec)
}

func gitBatchShowContext(ctx context.Context, checkoutDir, spec string) ([]byte, error) {
	if strings.ContainsAny(spec, "\r\n") {
		return nil, fmt.Errorf("invalid git object name")
	}
	cmd := gitCommandContext(ctx, checkoutDir, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(spec + "\n")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, gitCommandError(err, stderr.String())
	}
	reader := bufio.NewReader(&stdout)
	header, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read git object header: %w", err)
	}
	headerFields := strings.SplitN(strings.TrimSuffix(header, "\n"), " ", 3)
	if len(headerFields) < 2 {
		return nil, fmt.Errorf("unexpected git object header")
	}
	if headerFields[1] == "missing" {
		return nil, fmt.Errorf("git object %s is missing", spec)
	}
	if len(headerFields) != 3 {
		return nil, fmt.Errorf("unexpected git object header")
	}
	objectSize, err := strconv.ParseInt(headerFields[2], 10, 64)
	if err != nil || objectSize < 0 || objectSize > 64<<20 {
		return nil, fmt.Errorf("invalid git object size")
	}
	objectData := make([]byte, objectSize)
	if _, err := io.ReadFull(reader, objectData); err != nil {
		return nil, fmt.Errorf("read git object: %w", err)
	}
	separator, err := reader.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("read git object separator: %w", err)
	}
	if separator != '\n' {
		return nil, fmt.Errorf("invalid git object separator")
	}
	return objectData, nil
}

func gitOutputBytes(checkoutDir string, args ...string) ([]byte, error) {
	return gitOutputBytesContext(context.Background(), checkoutDir, args...)
}

func gitOutputBytesContext(ctx context.Context, checkoutDir string, args ...string) ([]byte, error) {
	cmd := gitCommandContext(ctx, checkoutDir, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return nil, gitCommandError(err, stderr.String())
	}
	return stdout.Bytes(), nil
}

func gitCommandError(commandErr error, stderr string) error {
	message := strings.TrimSpace(stderr)
	if message == "" {
		message = commandErr.Error()
	}
	return fmt.Errorf("%s", message)
}

func fetchOriginMain(ctx context.Context, checkoutDir string) error {
	_, err := gitOutputBytesContext(ctx, checkoutDir, "fetch", "origin", "main")
	return err
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
