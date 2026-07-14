package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/bbsteel/session-insight/internal/procfind"
)

// Codex keeps the rollout file open for append for the whole session
// lifetime (verified empirically, including while idle waiting for input),
// so "who holds the file" is an exact liveness check — unlike mtime, which
// goes stale on an idle-but-alive session.
func (r *CodexReader) SessionProcesses(id string) ([]int, error) {
	path := r.findSessionFile(id)
	if path == "" {
		return nil, fmt.Errorf("codex session not found: %s", id)
	}
	return procfind.HoldersOf(path)
}

// Session IDs look like rollout-2026-07-12T10-09-30-<uuid>; history.jsonl
// records the bare uuid.
var sessionUUIDRe = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// DeleteSession permanently removes a codex session from disk: its lines in
// the global ~/.codex/history.jsonl, then the rollout file itself. History
// goes first because purging is idempotent — if the file removal then fails,
// a retry still converges — while the reverse order would leave orphaned
// history lines with no session to retry against.
func (r *CodexReader) DeleteSession(id string) error {
	path := r.findSessionFile(id)
	if path == "" {
		return fmt.Errorf("codex session not found: %s", id)
	}
	if uuid := sessionUUIDRe.FindString(id); uuid != "" {
		if err := r.purgeHistory(uuid); err != nil {
			return fmt.Errorf("purge history.jsonl: %w", err)
		}
	}
	return os.Remove(path)
}

// purgeHistory rewrites history.jsonl without the given session's lines,
// via temp file + rename so a crash mid-write can't truncate codex's global
// history. Codex opens/appends/closes the file per write (it holds no
// persistent fd), so the only loss window is an append landing between our
// scan and the rename — milliseconds, and history is auxiliary data.
func (r *CodexReader) purgeHistory(sessionUUID string) error {
	historyPath := filepath.Join(filepath.Dir(r.sessionsDir), "history.jsonl")
	src, err := os.Open(historyPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer src.Close()

	tmp, err := os.CreateTemp(filepath.Dir(historyPath), ".history-purge-*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name()) // no-op after successful rename
	}()

	// Match on the parsed session_id field, not a substring scan: the uuid
	// could legitimately appear inside another session's message text.
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	removed := false
	w := bufio.NewWriter(tmp)
	for scanner.Scan() {
		line := scanner.Bytes()
		var entry struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(line, &entry) == nil && entry.SessionID == sessionUUID {
			removed = true
			continue
		}
		if _, err := w.Write(line); err != nil {
			return err
		}
		if err := w.WriteByte('\n'); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !removed {
		return nil
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if info, err := src.Stat(); err == nil {
		tmp.Chmod(info.Mode())
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), historyPath)
}
