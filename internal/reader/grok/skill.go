package grok

import (
	"path/filepath"
	"strings"
)

// skillNameFromPath returns the skill directory name when path points at a
// SKILL.md under a skills/ folder (Grok native TUI labels these as
// "Skill <name>", even though the wire tool is still read_file).
//
// Examples:
//
//	~/.claude/skills/git-commit-with-context/SKILL.md → "git-commit-with-context"
//	~/.grok/skills/help/SKILL.md                       → "help"
//	./.agents/skills/foo/SKILL.md                      → "foo"
//	/tmp/a.go                                          → ""
func skillNameFromPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	// Normalize separators (including Windows paths when SI runs on Linux);
	// keep original casing for the skill name.
	slash := strings.ReplaceAll(filepath.ToSlash(path), "\\", "/")
	base := filepath.Base(slash)
	if !strings.EqualFold(base, "SKILL.md") {
		return ""
	}
	dir := filepath.Dir(slash) // .../skills/<name>
	name := filepath.Base(dir)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return ""
	}
	parent := filepath.Base(filepath.Dir(dir)) // "skills"
	if !strings.EqualFold(parent, "skills") {
		return ""
	}
	return name
}

// skillNameFromRead detects a Grok skill activation disguised as a read tool.
// Wire format uses read_file (or Read / ReadFile) with target_file/path set to
// .../skills/<name>/SKILL.md; native TUI rewrites the row to "Skill <name>".
func skillNameFromRead(toolName string, input map[string]any) string {
	switch toolName {
	case "read_file", "Read", "ReadFile", "read":
	default:
		return ""
	}
	if input == nil {
		return ""
	}
	for _, key := range []string{"target_file", "path", "file_path"} {
		if p, ok := input[key].(string); ok {
			if name := skillNameFromPath(p); name != "" {
				return name
			}
		}
	}
	return ""
}
