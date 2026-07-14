package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bbsteel/session-insight/internal/procfind"
)

// claudeRoot is ~/.claude — the parent of the projects dir this reader was
// constructed with. All per-session sidecar stores live directly under it.
func (r *ClaudeReader) claudeRoot() string {
	return filepath.Dir(r.projectsDir)
}

func validSessionID(id string) bool {
	return id != "" && filepath.Base(id) == id && id != "." && id != ".."
}

// heartbeatFile is the shape of ~/.claude/sessions/<pid>.json: Claude Code
// writes one per running process and removes it on clean exit. procStart
// mirrors /proc/<pid>/stat field 22, so a stale file left by a crash can be
// told apart from a live process even across PID reuse.
type heartbeatFile struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	ProcStart string `json:"procStart"`
}

// SessionProcesses returns the exact PIDs of Claude Code processes running
// this session. Claude does not keep the transcript jsonl open (verified
// empirically — the fd probe that works for codex sees nothing), so the
// heartbeat files are the authoritative source instead.
func (r *ClaudeReader) SessionProcesses(id string) ([]int, error) {
	dir := filepath.Join(r.claudeRoot(), "sessions")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var pids []int
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var hb heartbeatFile
		if json.Unmarshal(data, &hb) != nil || hb.SessionID != id || hb.PID <= 0 {
			continue
		}
		if !heartbeatPIDLive(hb) {
			continue
		}
		pids = append(pids, hb.PID)
	}
	return pids, nil
}

// heartbeatPIDLive guards against a crash-orphaned heartbeat whose PID has
// been reused by an unrelated process: on Linux the start token must match
// what claude recorded at launch. Elsewhere the bare existence check is the
// best available — acceptable because heartbeats are removed on clean exit,
// so stale files are rare to begin with.
func heartbeatPIDLive(hb heartbeatFile) bool {
	if !procfind.Alive(hb.PID) {
		return false
	}
	if token, ok := procfind.StartToken(hb.PID); ok && hb.ProcStart != "" && token != hb.ProcStart {
		return false
	}
	return true
}

// DeleteSession permanently removes a claude session: the sidecar stores
// keyed by the session uuid (subagent transcripts, todos, file-history,
// session-env, debug log), then the transcript jsonl itself. Sidecars go
// first because removing them is idempotent — if the jsonl removal then
// fails, a retry still converges — while the reverse order would leave
// orphaned sidecars with no session to retry against. The global
// history.jsonl is untouched: its entries carry no session id (keyed by
// project + timestamp only), so there is nothing to purge.
func (r *ClaudeReader) DeleteSession(id string) error {
	if !validSessionID(id) {
		return fmt.Errorf("invalid claude session id: %q", id)
	}
	jsonlPath := r.findSessionFile(id)
	if jsonlPath == "" {
		return fmt.Errorf("claude session not found: %s", id)
	}

	root := r.claudeRoot()

	// Subagent transcripts: sibling "<uuid>/" directory next to the jsonl.
	if err := os.RemoveAll(strings.TrimSuffix(jsonlPath, ".jsonl")); err != nil {
		return fmt.Errorf("remove subagents dir: %w", err)
	}

	// Todos are "<uuid>-agent-<uuid>.json"; uuid prefix match is exact
	// because all session uuids share the same length.
	todoFiles, _ := filepath.Glob(filepath.Join(root, "todos", id+"*.json"))
	for _, p := range todoFiles {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove todo %s: %w", filepath.Base(p), err)
		}
	}

	for _, dir := range []string{"file-history", "session-env"} {
		if err := os.RemoveAll(filepath.Join(root, dir, id)); err != nil {
			return fmt.Errorf("remove %s: %w", dir, err)
		}
	}
	if err := os.Remove(filepath.Join(root, "debug", id+".txt")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove debug log: %w", err)
	}

	return os.Remove(jsonlPath)
}
