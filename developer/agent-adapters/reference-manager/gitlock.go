package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
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
	if _, err := gitOutput(checkoutDir, "rev-parse", "--verify", ref); err != nil {
		return nil, fmt.Errorf("git ref %s is unreadable: %w", ref, err)
	}
	spec := ref + ":" + evidenceLockPath(agent)
	exists, err := gitObjectExists(checkoutDir, spec)
	if err != nil {
		return nil, err
	}
	if !exists {
		return emptyEvidenceLock(agent), nil
	}
	data, err := gitOutputBytes(checkoutDir, "show", spec)
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
	cmd := gitCommand(checkoutDir, "cat-file", "-e", spec)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if exitErr.ExitCode() == 1 {
			return false, nil
		}
		// Git versions commonly return 128 for a missing ref:path entry,
		// while using the same code for repository-level failures. The ref is
		// verified by readAgentEvidenceLock before this helper is called, so
		// only treat 128 as a missing path when its base ref is readable.
		if exitErr.ExitCode() == 128 {
			if ref, _, hasPath := strings.Cut(spec, ":"); hasPath {
				if _, verifyErr := gitOutput(checkoutDir, "rev-parse", "--verify", ref); verifyErr == nil {
					return false, nil
				}
			}
		}
		return false, err
	}
	return false, err
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
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return stdout.Bytes(), nil
}

func fetchOriginMain(ctx context.Context, checkoutDir string) error {
	_, err := gitOutputBytesContext(ctx, checkoutDir, "fetch", "origin", "main")
	return err
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
