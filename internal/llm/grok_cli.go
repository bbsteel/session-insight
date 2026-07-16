package llm

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// grokCLIClient drives the Grok Build TUI in headless mode. Grok does not
// expose an ACP stdio server (unlike Claude/Codex adapters or Gemini CLI), so
// model listing uses `grok models` and generation uses
// `grok --prompt-file … -m …` with tools disabled.
//
// Sessions are forced into a scratch cwd named with scratchDirPrefix so the
// session list can filter generation byproducts (see IsScratchCWD).
type grokCLIClient struct {
	cfg Config
}

func (c *grokCLIClient) ListModels(ctx context.Context) ([]Model, error) {
	cmd := exec.CommandContext(ctx, "grok", "models")
	cmd.Env = childEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("grok models: %s", msg)
	}
	models := parseGrokModelsOutput(stdout.String())
	if len(models) == 0 {
		return nil, fmt.Errorf("grok models: no models advertised (are you logged in?)")
	}
	return models, nil
}

// parseGrokModelsOutput turns the human-readable `grok models` listing into
// selectable models. Expected shape:
//
//	Default model: grok-4.5
//	Available models:
//	  * grok-4.5 (default)
//	  - grok-composer-2.5-fast
func parseGrokModelsOutput(out string) []Model {
	var models []Model
	var defaultID string
	seen := map[string]bool{}

	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "Default model:") {
			defaultID = strings.TrimSpace(strings.TrimPrefix(line, "Default model:"))
			continue
		}
		// Bullet lines: "* id …" or "- id …"
		var rest string
		switch {
		case strings.HasPrefix(line, "* "):
			rest = strings.TrimSpace(line[2:])
		case strings.HasPrefix(line, "- "):
			rest = strings.TrimSpace(line[2:])
		default:
			continue
		}
		if rest == "" {
			continue
		}
		id := strings.Fields(rest)[0]
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		label := id
		if strings.Contains(rest, "(default)") {
			label = id + " (default)"
		}
		models = append(models, Model{ID: id, Label: label})
	}

	// Prefer the advertised default at the front of the list for UI convenience.
	if defaultID != "" {
		for i, m := range models {
			if m.ID == defaultID {
				if i > 0 {
					models[0], models[i] = models[i], models[0]
				}
				return models
			}
		}
		// Default mentioned but not in bullets — still surface it.
		if !seen[defaultID] {
			models = append([]Model{{ID: defaultID, Label: defaultID + " (default)"}}, models...)
		}
	}
	return models
}

func (c *grokCLIClient) Generate(ctx context.Context, prompt string, onStatus StatusFunc) (string, error) {
	return c.generate(ctx, "", prompt, onStatus)
}

// GenerateWithSystem uses Grok's --rules flag for the immutable instruction
// and the prompt file for the untrusted user payload. This is stronger than a
// single concatenated prompt, but weaker than a true system role.
func (c *grokCLIClient) GenerateWithSystem(ctx context.Context, system, user string, onStatus StatusFunc) (string, error) {
	return c.generate(ctx, system, user, onStatus)
}

func (c *grokCLIClient) generate(ctx context.Context, rules, prompt string, onStatus StatusFunc) (string, error) {
	if c.cfg.ModelID == "" {
		return "", fmt.Errorf("no model selected for this provider")
	}
	if onStatus != nil {
		onStatus("准备 Grok CLI")
	}

	tmpDir, err := os.MkdirTemp("", scratchDirPrefix)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	promptPath := filepath.Join(tmpDir, "prompt.txt")
	if err := os.WriteFile(promptPath, []byte(prompt), 0o600); err != nil {
		return "", err
	}

	// Text-only generation: empty tool allowlist, no subagents, single turn,
	// scratch cwd. --verbatim keeps the prompt from being rewritten.
	args := []string{
		"--prompt-file", promptPath,
		"-m", c.cfg.ModelID,
		"--output-format", "plain",
		"--tools", "",
		"--no-subagents",
		"--max-turns", "1",
		"--cwd", tmpDir,
		"--no-memory",
		"--verbatim",
	}
	if strings.TrimSpace(rules) != "" {
		args = append(args, "--rules", rules)
	}

	if onStatus != nil {
		onStatus("调用 Grok CLI")
	}

	cmd := exec.CommandContext(ctx, "grok", args...)
	cmd.Dir = tmpDir
	cmd.Env = childEnv()
	setProcAttr(cmd)
	cmd.Cancel = func() error { return killProc(cmd) }
	cmd.WaitDelay = 3 * time.Second

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("grok: %w", ctx.Err())
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("grok generate: %s", msg)
	}

	if onStatus != nil {
		onStatus("整理模型结果")
	}
	output := strings.TrimSpace(stdout.String())
	if output == "" {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = "(no stderr)"
		}
		return "", fmt.Errorf("grok returned empty content; stderr: %s", msg)
	}
	return output, nil
}
