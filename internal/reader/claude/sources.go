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
// Known primary is always listed (even when missing/unreadable) so provenance
// can classify SourceMissing / SourceUnreadable; optional sidecars are omitted
// when absent.
func sourceInventory(jsonlPath, claudeRoot string) []model.SessionSourceFile {
	var sources []model.SessionSourceFile
	seen := map[string]struct{}{}
	// required=true keeps the path even when missing so StatSource can classify it.
	add := func(role, path string, required bool) {
		path = filepath.Clean(path)
		if path == "" || path == "." {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) && !required {
				return
			}
			// Missing required path, or unreadable for any reason: still inventory.
			seen[path] = struct{}{}
			sources = append(sources, provenance.StatSource(role, path))
			return
		}
		if !info.Mode().IsRegular() {
			return
		}
		seen[path] = struct{}{}
		sources = append(sources, provenance.StatSource(role, path))
	}

	if jsonlPath != "" {
		add(model.SourceRolePrimaryTranscript, jsonlPath, true)
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
				add(model.SourceRoleCollaboration, p, false)
			case strings.HasSuffix(name, ".meta.json"):
				add(model.SourceRoleMetadata, p, false)
			}
		}
	}

	// Per-session todo files: only <sessionID>-agent-*.json (documented layout).
	sessionID := strings.TrimSuffix(filepath.Base(jsonlPath), ".jsonl")
	if claudeRoot != "" && sessionID != "" {
		todoDir := filepath.Join(claudeRoot, "todos")
		if entries, err := os.ReadDir(todoDir); err == nil {
			prefix := sessionID + "-agent-"
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := e.Name()
				if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".json") {
					names = append(names, name)
				}
			}
			sort.Strings(names)
			for _, name := range names {
				add(model.SourceRoleToolResults, filepath.Join(todoDir, name), false)
			}
		}
	}

	return sources
}
