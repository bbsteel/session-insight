package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/bbsteel/session-insight/internal/procfind"
)

const (
	delSessionID   = "11111111-2222-3333-4444-555555555555"
	otherSessionID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
)

// newTestClaudeHome builds a ~/.claude replica with two sessions and every
// sidecar store the deleter touches, returning the reader and the root.
func newTestClaudeHome(t *testing.T) (*ClaudeReader, string) {
	t.Helper()
	root := t.TempDir()
	projDir := filepath.Join(root, "projects", "-home-user-proj")

	for _, id := range []string{delSessionID, otherSessionID} {
		mustWrite(t, filepath.Join(projDir, id+".jsonl"),
			`{"type":"user","sessionId":"`+id+`","timestamp":"2026-07-14T10:00:00Z","cwd":"/home/user/proj","message":{"role":"user","content":"hi"}}`+"\n")
		mustWrite(t, filepath.Join(projDir, id, "subagents", "agent-1.jsonl"), "{}\n")
		mustWrite(t, filepath.Join(root, "todos", id+"-agent-"+id+".json"), "[]")
		mustWrite(t, filepath.Join(root, "file-history", id, "abc@v1"), "old")
		mustWrite(t, filepath.Join(root, "session-env", id, ".keep"), "")
		mustWrite(t, filepath.Join(root, "debug", id+".txt"), "log")
	}

	return New(filepath.Join(root, "projects")), root
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteSessionRemovesTranscriptAndSidecars(t *testing.T) {
	r, root := newTestClaudeHome(t)

	if err := r.DeleteSession(delSessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	gone := []string{
		filepath.Join(root, "projects", "-home-user-proj", delSessionID+".jsonl"),
		filepath.Join(root, "projects", "-home-user-proj", delSessionID),
		filepath.Join(root, "todos", delSessionID+"-agent-"+delSessionID+".json"),
		filepath.Join(root, "file-history", delSessionID),
		filepath.Join(root, "session-env", delSessionID),
		filepath.Join(root, "debug", delSessionID+".txt"),
	}
	for _, p := range gone {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed, stat err=%v", p, err)
		}
	}

	// The other session's data must be untouched.
	kept := []string{
		filepath.Join(root, "projects", "-home-user-proj", otherSessionID+".jsonl"),
		filepath.Join(root, "todos", otherSessionID+"-agent-"+otherSessionID+".json"),
		filepath.Join(root, "file-history", otherSessionID),
		filepath.Join(root, "debug", otherSessionID+".txt"),
	}
	for _, p := range kept {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to survive: %v", p, err)
		}
	}
}

func TestDeleteSessionMissingSidecarsIsFine(t *testing.T) {
	root := t.TempDir()
	projDir := filepath.Join(root, "projects", "-p")
	mustWrite(t, filepath.Join(projDir, delSessionID+".jsonl"), "{}\n")
	r := New(filepath.Join(root, "projects"))

	if err := r.DeleteSession(delSessionID); err != nil {
		t.Fatalf("DeleteSession with no sidecars: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projDir, delSessionID+".jsonl")); !os.IsNotExist(err) {
		t.Error("transcript not removed")
	}
}

func TestDeleteSessionRejectsBadIDs(t *testing.T) {
	r, root := newTestClaudeHome(t)
	for _, id := range []string{"", ".", "..", "../todos", "a/b"} {
		if err := r.DeleteSession(id); err == nil {
			t.Errorf("DeleteSession(%q) should fail", id)
		}
	}
	if err := r.DeleteSession("no-such-session"); err == nil {
		t.Error("DeleteSession for unknown id should fail")
	}
	// Nothing should have been deleted by the failed attempts.
	if _, err := os.Stat(filepath.Join(root, "todos")); err != nil {
		t.Errorf("todos dir damaged: %v", err)
	}
}

func TestSessionProcessesMatchesHeartbeat(t *testing.T) {
	r, root := newTestClaudeHome(t)
	sessDir := filepath.Join(root, "sessions")

	self := os.Getpid()
	token, _ := procfind.StartToken(self)
	writeHeartbeat(t, sessDir, self, delSessionID, token)
	// A heartbeat for a long-dead PID must be ignored.
	writeHeartbeat(t, sessDir, 999999999, delSessionID, "1")
	// A heartbeat for another session must not match.
	writeHeartbeat(t, sessDir, self+1, otherSessionID, "1")

	pids, err := r.SessionProcesses(delSessionID)
	if err != nil {
		t.Fatalf("SessionProcesses: %v", err)
	}
	if len(pids) != 1 || pids[0] != self {
		t.Fatalf("expected [%d], got %v", self, pids)
	}
}

func TestSessionProcessesRejectsReusedPID(t *testing.T) {
	r, root := newTestClaudeHome(t)
	if _, ok := procfind.StartToken(os.Getpid()); !ok {
		t.Skip("start token not available on this platform")
	}
	// Alive PID but a start token that cannot match: simulated PID reuse.
	writeHeartbeat(t, filepath.Join(root, "sessions"), os.Getpid(), delSessionID, "0")

	pids, err := r.SessionProcesses(delSessionID)
	if err != nil {
		t.Fatalf("SessionProcesses: %v", err)
	}
	if len(pids) != 0 {
		t.Fatalf("reused PID should be filtered, got %v", pids)
	}
}

func TestSessionProcessesNoHeartbeatDir(t *testing.T) {
	r, _ := newTestClaudeHome(t)
	pids, err := r.SessionProcesses(delSessionID)
	if err != nil {
		t.Fatalf("SessionProcesses without sessions dir: %v", err)
	}
	if len(pids) != 0 {
		t.Fatalf("expected no pids, got %v", pids)
	}
}

func writeHeartbeat(t *testing.T, dir string, pid int, sessionID, procStart string) {
	t.Helper()
	data, _ := json.Marshal(heartbeatFile{PID: pid, SessionID: sessionID, ProcStart: procStart})
	mustWrite(t, filepath.Join(dir, strconv.Itoa(pid)+".json"), string(data))
}
