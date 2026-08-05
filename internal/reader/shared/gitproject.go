package shared

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveProject derives a display project name for a session.
//
// If repo is a non-empty repository slug (e.g. Copilot's "owner/repo"), it is
// returned directly. Absolute filesystem paths are not slugs — they are
// resolved the same way as cwd (Grok's git_root_dir is such a path).
//
// Path resolution order:
//
//  1. Known worktree manager layouts are checked first, since the directory
//     name at the git-root level would be a branch or generated id, not the
//     project name.
//  2. The directory tree is walked upward looking for a .git entry.
//     A .git directory terminates the walk immediately.
//     A .git file (linked worktree) is followed back to the main repo root.
//  3. If no git root is found, the last path component of the path is used.
func ResolveProject(cwd, repo string) string {
	cwd = strings.TrimSpace(cwd)
	repo = strings.TrimSpace(repo)

	if repo != "" {
		if isFilesystemPath(repo) {
			// Prefer the explicit root path when callers pass one (e.g. Grok
			// git_root_dir) so sessions opened from a subdirectory still group
			// under the repository basename rather than the leaf cwd.
			return projectNameFromPath(repo)
		}
		return repo
	}
	if cwd == "" {
		return ""
	}
	return projectNameFromPath(cwd)
}

// isFilesystemPath reports whether s looks like a local path rather than a
// remote repository slug such as "owner/repo".
//
// Detection is OS-agnostic so a Linux build still treats Windows drive/UNC
// paths from session metadata as paths (not slugs), and vice versa.
func isFilesystemPath(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// Unix absolute paths (including trailing-slash forms Grok stores).
	if strings.HasPrefix(s, "/") {
		return true
	}
	// Home-relative shorthand occasionally appears in session metadata.
	if strings.HasPrefix(s, "~/") || s == "~" {
		return true
	}
	// Windows drive paths: C:\... or C:/... (independent of build GOOS).
	if len(s) >= 3 && isDriveLetter(s[0]) && s[1] == ':' && (s[2] == '\\' || s[2] == '/') {
		return true
	}
	// Windows UNC: \\server\share or //server/share
	if strings.HasPrefix(s, `\\`) || strings.HasPrefix(s, "//") {
		return true
	}
	// Host-native absolute paths (covers any remaining platform forms).
	return filepath.IsAbs(s)
}

func isDriveLetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// projectNameFromPath reduces a workspace path to a short display project name.
func projectNameFromPath(path string) string {
	path = expandHome(path)
	// Normalize separators for layout detection and basename so Windows paths
	// from session metadata still yield a short name on a Unix build.
	slashPath := strings.ReplaceAll(path, `\`, "/")
	cleaned := filepath.Clean(slashPath)

	if p := detectWorktreeLayout(cleaned); p != "" {
		return p
	}

	// Prefer host-native path for filesystem probes (home expansion, real
	// local workspaces). Windows-shaped paths usually miss on non-Windows
	// hosts and fall through to basename below.
	probe := filepath.Clean(path)
	if _, err := os.Stat(probe); err == nil {
		if root := gitRootOf(probe); root != "" {
			return portableBase(root)
		}
	}

	base := portableBase(cleaned)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return ""
	}
	return base
}

// portableBase returns the last path element using both / and \ separators so
// session metadata recorded on another OS still maps to a short project name.
func portableBase(path string) string {
	s := strings.ReplaceAll(path, `\`, "/")
	s = strings.TrimRight(s, "/")
	if s == "" {
		return ""
	}
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// expandHome resolves a leading "~" or "~/" against the current user's home
// so os.Stat / git walks do not treat "~" as a relative path under cwd.
func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return home + path[1:]
		}
	}
	return path
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
