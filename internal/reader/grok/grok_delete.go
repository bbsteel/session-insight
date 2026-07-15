package grok

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"

	"github.com/bbsteel/session-insight/internal/procfind"
	"github.com/bbsteel/session-insight/internal/reader/shared"
)

// SessionProcesses returns PIDs that own this session.
// Sources (union, deduped):
//  1. ~/.grok/active_sessions.json entry with a live PID
//  2. fd holders of the session's growing content files
//
// Stale active_sessions rows (process dead) are ignored — lock residuals
// must never land on a kill list.
func (r *GrokReader) SessionProcesses(id string) ([]int, error) {
	if !validSessionID(id) {
		return nil, fmt.Errorf("invalid grok session id: %q", id)
	}
	loc, err := r.findSession(id)
	if err != nil {
		return nil, err
	}

	seen := map[int]bool{}
	var pids []int
	add := func(pid int) {
		if pid > 0 && !seen[pid] && procfind.Alive(pid) {
			seen[pid] = true
			pids = append(pids, pid)
		}
	}

	for _, pid := range r.activePIDs(id) {
		add(pid)
	}
	for _, name := range []string{"updates.jsonl", "chat_history.jsonl", "events.jsonl"} {
		holders, err := procfind.HoldersOf(filepath.Join(loc.Dir, name))
		if err != nil {
			return nil, err
		}
		for _, pid := range holders {
			add(pid)
		}
	}
	return pids, nil
}

// SessionRunning is the block-only fallback when no PID is available:
// trailing in_progress (already LiveWindow-bounded at emission).
func (r *GrokReader) SessionRunning(id string) (bool, error) {
	if !validSessionID(id) {
		return false, fmt.Errorf("invalid grok session id: %q", id)
	}
	// Live PID is authoritative when present.
	if pids, err := r.SessionProcesses(id); err == nil && len(pids) > 0 {
		return true, nil
	}
	events, err := r.GetRenderEvents(id)
	if err != nil {
		return false, err
	}
	return shared.HasTrailingInProgress(events), nil
}

type activeSession struct {
	SessionID string `json:"session_id"`
	PID       int    `json:"pid"`
	CWD       string `json:"cwd"`
}

func (r *GrokReader) activeSessionsPath() string {
	return filepath.Join(r.grokHome, "active_sessions.json")
}

func (r *GrokReader) activePIDs(id string) []int {
	data, err := os.ReadFile(r.activeSessionsPath())
	if err != nil {
		return nil
	}
	var list []activeSession
	if json.Unmarshal(data, &list) != nil {
		return nil
	}
	var pids []int
	for _, s := range list {
		if s.SessionID == id && s.PID > 0 {
			pids = append(pids, s.PID)
		}
	}
	return pids
}

// DeleteSession removes the session directory and related global/project
// sidecars. Order: sidecars first (idempotent), main directory last.
func (r *GrokReader) DeleteSession(id string) error {
	if !validSessionID(id) {
		return fmt.Errorf("invalid grok session id: %q", id)
	}
	loc, err := r.findSession(id)
	if err != nil {
		return err
	}

	// 1. Global search index (FTS cleared via DELETE trigger).
	if err := r.purgeSessionSearch(id); err != nil {
		return fmt.Errorf("purge session_search.sqlite: %w", err)
	}
	// 2. Project prompt history lines.
	if err := stripPromptHistory(filepath.Join(loc.ProjectDir, "prompt_history.jsonl"), id); err != nil {
		return fmt.Errorf("strip prompt_history: %w", err)
	}
	// 3. active_sessions entry (if present).
	if err := r.stripActiveSession(id); err != nil {
		return fmt.Errorf("strip active_sessions: %w", err)
	}
	// 4. Session directory last.
	return os.RemoveAll(loc.Dir)
}

func (r *GrokReader) purgeSessionSearch(id string) error {
	dbPath := filepath.Join(r.sessionsDir, "session_search.sqlite")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil
	}
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer db.Close()

	// session_docs_ad trigger keeps FTS in sync on DELETE.
	_, err = db.Exec(`DELETE FROM session_docs WHERE session_id = ?`, id)
	return err
}

func stripPromptHistory(path, sessionID string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	var kept []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		var row struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal([]byte(line), &row) == nil && row.SessionID == sessionID {
			continue
		}
		kept = append(kept, line)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	// Atomic rewrite.
	tmp := path + ".tmp"
	out := strings.Join(kept, "\n")
	if len(kept) > 0 {
		out += "\n"
	}
	if err := os.WriteFile(tmp, []byte(out), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (r *GrokReader) stripActiveSession(id string) error {
	path := r.activeSessionsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var list []activeSession
	if err := json.Unmarshal(data, &list); err != nil {
		// Malformed — leave alone rather than wipe other sessions.
		return nil
	}
	out := list[:0]
	changed := false
	for _, s := range list {
		if s.SessionID == id {
			changed = true
			continue
		}
		out = append(out, s)
	}
	if !changed {
		return nil
	}
	if out == nil {
		out = []activeSession{}
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
