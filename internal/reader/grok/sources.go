package grok

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/provenance"
)

// sourceInventory lists Grok Build TUI files this adapter cares about — any
// per-session file SI uses or that helps explain what SI indexed.
// Layout (under ~/.grok/sessions/<url-encoded-cwd>/<uuid>/):
//
//	summary.json            metadata (title, cwd, model, timestamps)
//	system_prompt.txt       metadata (system prompt SI’s host agent ran with)
//	prompt_context.json     metadata (AGENTS.md / project context blobs)
//	signals.json            metadata (turn/error/cancel counters)
//	resources_state.json    metadata (tool/resource flags for the run)
//	announcement_state.json metadata (skill/MCP announcement fingerprints)
//	updates.jsonl           primary ACP stream (preferred for replay)
//	chat_history.jsonl      compact transcript (fallback / secondary stream)
//	events.jsonl            turn_started / turn_ended brackets
//	rewind_points.jsonl     rewind checkpoints (snapshot)
//	hunk_records.jsonl      file-edit hunk cache (edit_cache)
//	subagents/*/meta.json   collaboration (+ child summary/updates when present)
//
// Skips *.lock and the session directory itself. Global stores
// (session_search.sqlite, active_sessions.json) are not per-session.
func sourceInventory(sessionDir, summaryPath string) []model.SessionSourceFile {
	var sources []model.SessionSourceFile
	seen := map[string]struct{}{}
	add := func(role, path string) {
		path = filepath.Clean(path)
		if path == "" || path == "." || path == filepath.Clean(sessionDir) {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return
		}
		if strings.HasSuffix(filepath.Base(path), ".lock") {
			return
		}
		seen[path] = struct{}{}
		sources = append(sources, provenance.StatSource(role, path))
	}

	// Session identity + run context (all useful to SI / operators).
	if summaryPath != "" {
		add(model.SourceRoleMetadata, summaryPath)
	} else {
		add(model.SourceRoleMetadata, filepath.Join(sessionDir, "summary.json"))
	}
	for _, name := range []string{
		"system_prompt.txt",
		"prompt_context.json",
		"signals.json",
		"resources_state.json",
		"announcement_state.json",
	} {
		add(model.SourceRoleMetadata, filepath.Join(sessionDir, name))
	}

	updatesPath := filepath.Join(sessionDir, "updates.jsonl")
	chatPath := filepath.Join(sessionDir, "chat_history.jsonl")
	eventsPath := filepath.Join(sessionDir, "events.jsonl")

	// Same preference as turn parsing: updates primary when present.
	if _, err := os.Stat(updatesPath); err == nil {
		add(model.SourceRolePrimaryTranscript, updatesPath)
		add(model.SourceRoleUpdates, chatPath)
	} else {
		add(model.SourceRolePrimaryTranscript, chatPath)
	}
	add(model.SourceRoleEvents, eventsPath)
	add(model.SourceRoleSnapshot, filepath.Join(sessionDir, "rewind_points.jsonl"))
	add(model.SourceRoleEditCache, filepath.Join(sessionDir, "hunk_records.jsonl"))

	// Child agents discovered under subagents/ (collaboration package).
	subRoot := filepath.Join(sessionDir, "subagents")
	if entries, err := os.ReadDir(subRoot); err == nil {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			childDir := filepath.Join(subRoot, name)
			add(model.SourceRoleCollaboration, filepath.Join(childDir, "meta.json"))
			add(model.SourceRoleCollaboration, filepath.Join(childDir, "summary.json"))
			// Child may have its own streams; list if present.
			add(model.SourceRoleCollaboration, filepath.Join(childDir, "updates.jsonl"))
			add(model.SourceRoleCollaboration, filepath.Join(childDir, "chat_history.jsonl"))
		}
	}

	return sources
}
