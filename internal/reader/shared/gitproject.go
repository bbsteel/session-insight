package shared

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveProject derives a display project name for a session.
//
// If repo is non-empty (Copilot sessions carry the actual repository slug),
// it is returned directly. Otherwise the project is inferred from cwd:
//
//  1. Known worktree manager layouts are checked first, since the directory
//     name at the git-root level would be a branch or generated id, not the
//     project name.
//  2. The directory tree is walked upward looking for a .git entry.
//     A .git directory terminates the walk immediately.
//     A .git file (linked worktree) is followed back to the main repo root.
//  3. If no git root is found, the last path component of cwd is used.
func ResolveProject(cwd, repo string) string {
	if repo != "" {
		return repo
	}
	if cwd == "" {
		return ""
	}

	cleaned := filepath.Clean(cwd)

	if p := detectWorktreeLayout(cleaned); p != "" {
		return p
	}

	if root := gitRootOf(cleaned); root != "" {
		return filepath.Base(root)
	}

	base := filepath.Base(cleaned)
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

// detectWorktreeLayout recognises path conventions used by worktree managers
// where the directory containing the worktrees encodes the project name.
// Returns the project name, or "" if no layout matches.
func detectWorktreeLayout(path string) string {
	// Claude Code: <project>/.claude/worktrees/<branch>[/subdir...]
	const claudeMarker = "/.claude/worktrees/"
	if idx := strings.Index(path, claudeMarker); idx >= 0 {
		if base := filepath.Base(path[:idx]); base != "" && base != "." {
			return base
		}
	}

	// OpenClaw: .openclaw/workspace/projects/<group>/<project>[/subdir...]
	const openClawMarker = "/.openclaw/workspace/projects/"
	if idx := strings.Index(path, openClawMarker); idx >= 0 {
		rest := strings.Trim(path[idx+len(openClawMarker):], string(filepath.Separator))
		parts := strings.Split(rest, string(filepath.Separator))
		if len(parts) >= 2 && parts[1] != "" {
			return parts[1]
		}
	}
	return ""
}

// gitRootOf walks upward from dir looking for the enclosing git repository
// root. It handles both ordinary repos (.git directory) and linked worktrees
// (.git file pointing to the main repo).
func gitRootOf(dir string) string {
	for {
		entry := filepath.Join(dir, ".git")
		info, err := os.Stat(entry)
		if err == nil {
			if info.IsDir() {
				return dir
			}
			if info.Mode().IsRegular() {
				if root := mainRepoFromWorktreeFile(dir, entry); root != "" {
					return root
				}
				// File exists but resolution failed; keep walking up so we
				// don't stop at a nested worktree when the main repo is
				// further up the tree.
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// mainRepoFromWorktreeFile reads a .git file (linked-worktree pointer) and
// resolves it to the main repository root. Returns "" when it cannot resolve.
func mainRepoFromWorktreeFile(enclosingDir, gitFilePath string) string {
	target := readGitdirLine(gitFilePath)
	if target == "" {
		return ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(enclosingDir, target)
	}
	target = filepath.Clean(target)

	// Linked worktrees contain a "commondir" file that points to the main
	// .git directory, e.g. "../.." or an absolute path ending in ".git".
	if common := readCommondir(target); common != "" {
		if filepath.Base(common) == ".git" {
			return filepath.Dir(common)
		}
	}

	// Second fallback: the gitdir path itself contains /.git/worktrees/
	// which lets us extract the main repo root by splitting on that marker.
	const marker = "/.git/worktrees/"
	if root, _, ok := strings.Cut(target, marker); ok && root != "" {
		return filepath.Clean(root)
	}

	return enclosingDir
}

// readGitdirLine parses the "gitdir: <path>" line from a .git pointer file.
func readGitdirLine(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	const prefix = "gitdir:"
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	return ""
}

// readCommondir reads the "commondir" file inside a worktree's git directory.
// That file holds a relative or absolute path back to the main .git directory.
func readCommondir(gitDir string) string {
	data, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return ""
	}
	val := strings.TrimSpace(string(data))
	if val == "" {
		return ""
	}
	if filepath.IsAbs(val) {
		return filepath.Clean(val)
	}
	return filepath.Clean(filepath.Join(gitDir, val))
}
