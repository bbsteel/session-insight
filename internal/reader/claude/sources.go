package claude

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/provenance"
)

// sourceInventory lists Claude Code files this adapter cares about for one
// session. Layout (under ~/.claude/):
//
//	projects/<project-key>/<sessionID>.jsonl           primary transcript
//	projects/<project-key>/<sessionID>/subagents/
//	  agent-<id>.jsonl                                 collaboration
//	  agent-<id>.meta.json                             metadata (join keys)
//	todos/<sessionID>-agent-*.json                     tool / todo sidecars
//
// Only these paths are inventoried — not the whole project directory.
func sourceInventory(jsonlPath, claudeRoot string) []model.SessionSourceFile {
	var sources []model.SessionSourceFile
	seen := map[string]struct{}{}
	add := func(role, path string) {
		path = filepath.Clean(path)
		if path == "" || path == "." {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return
		}
		seen[path] = struct{}{}
		sources = append(sources, provenance.StatSource(role, path))
	}

	if jsonlPath != "" {
		add(model.SourceRolePrimaryTranscript, jsonlPath)
	}

	// Sidechain / subagent transcripts next to the main jsonl.
	sessionDir := strings.TrimSuffix(jsonlPath, ".jsonl")
	subDir := filepath.Join(sessionDir, "subagents")
	if entries, err := os.ReadDir(subDir); err == nil {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for _, name := range names {
			p := filepath.Join(subDir, name)
			switch {
			case strings.HasSuffix(name, ".jsonl"):
				add(model.SourceRoleCollaboration, p)
			case strings.HasSuffix(name, ".meta.json"):
				add(model.SourceRoleMetadata, p)
			}
		}
	}

	// Per-session todo files under ~/.claude/todos/.
	sessionID := strings.TrimSuffix(filepath.Base(jsonlPath), ".jsonl")
	if claudeRoot != "" && sessionID != "" {
		todoDir := filepath.Join(claudeRoot, "todos")
		if entries, err := os.ReadDir(todoDir); err == nil {
			prefix := sessionID + "-"
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				if strings.HasPrefix(e.Name(), prefix) || strings.HasPrefix(e.Name(), sessionID+"-agent-") {
					names = append(names, e.Name())
				}
			}
			sort.Strings(names)
			for _, name := range names {
				add(model.SourceRoleToolResults, filepath.Join(todoDir, name))
			}
		}
	}

	return sources
}
