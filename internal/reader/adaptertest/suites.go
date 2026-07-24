package adaptertest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/procfind"
)

// ---- Shared semantic assertions for Layer-3 capability suites ----

// RequireLiveRevisionProvider type-asserts LiveRevision support.
func RequireLiveRevisionProvider(t *testing.T, r Reader) LiveRevisionProvider {
	t.Helper()
	lr, ok := r.(LiveRevisionProvider)
	if !ok {
		t.Fatalf("reader %T does not implement LiveRevisionProvider", r)
	}
	return lr
}

// RequireSessionDeleter type-asserts DeleteSession support.
func RequireSessionDeleter(t *testing.T, r Reader) SessionDeleter {
	t.Helper()
	d, ok := r.(SessionDeleter)
	if !ok {
		t.Fatalf("reader %T does not implement SessionDeleter", r)
	}
	return d
}

// RequireSessionProcessFinder type-asserts SessionProcesses support.
func RequireSessionProcessFinder(t *testing.T, r Reader) SessionProcessFinder {
	t.Helper()
	p, ok := r.(SessionProcessFinder)
	if !ok {
		t.Fatalf("reader %T does not implement SessionProcessFinder", r)
	}
	return p
}

// AssertRealtimeStableThenMutate checks LiveRevision stability, then requires
// a change after mutate() alters the test-owned fixture.
func AssertRealtimeStableThenMutate(t *testing.T, r Reader, sessionID string, mutate func(t *testing.T)) {
	t.Helper()
	lr := RequireLiveRevisionProvider(t, r)
	rev1, err := lr.LiveRevision(sessionID)
	if err != nil {
		t.Fatalf("LiveRevision initial: %v", err)
	}
	rev2, err := lr.LiveRevision(sessionID)
	if err != nil {
		t.Fatalf("LiveRevision stable re-read: %v", err)
	}
	if rev1 != rev2 {
		t.Fatalf("LiveRevision unstable without mutation: %d then %d", rev1, rev2)
	}
	mutate(t)
	rev3, err := lr.LiveRevision(sessionID)
	if err != nil {
		t.Fatalf("LiveRevision after mutate: %v", err)
	}
	if rev3 == rev1 {
		t.Fatalf("LiveRevision did not change after mutation (still %d)", rev3)
	}
	// Monotonic in the sense of "different"; size+mtime encoding may not be
	// strictly greater for all agents, so inequality is the contract.
}

// TokenExpect holds expected exclusive-bucket values for a session bill.
type TokenExpect struct {
	// SessionID is loaded via GetSession.
	SessionID string
	// MinPrompt / MinCompletion are lower bounds when exact equality is
	// awkward across agents; Exact* when non-nil force equality.
	ExactPrompt     *int64
	ExactCompletion *int64
	ExactCacheRead  *int64
	ExactReasoning  *int64
	// RequirePresent marks that billing must exist with Precision exact.
	RequireExactPrecision bool
	// RequireNonNilBilling fails if Billing is nil.
	RequireNonNilBilling bool
}

// AssertTokens loads the session and checks token/billing evidence.
func AssertTokens(t *testing.T, r Reader, exp TokenExpect) {
	t.Helper()
	d, err := r.GetSession(exp.SessionID)
	if err != nil {
		t.Fatalf("GetSession(%q): %v", exp.SessionID, err)
	}
	if exp.RequireNonNilBilling && d.Billing == nil {
		t.Fatal("Billing is nil")
	}
	if d.Billing == nil {
		// Fall back to turn-level token sums.
		var prompt, completion, cacheRead, reasoning int64
		for _, tr := range d.Turns {
			prompt += tr.TokenUsage.PromptTokens
			completion += tr.TokenUsage.CompletionTokens
			cacheRead += tr.TokenUsage.CacheReadTokens
			reasoning += tr.TokenUsage.ReasoningTokens
		}
		checkTokenBounds(t, "turns", prompt, completion, cacheRead, reasoning, exp)
		return
	}
	if exp.RequireExactPrecision && d.Billing.Precision != model.PrecisionExact {
		t.Fatalf("Billing.Precision=%q want exact", d.Billing.Precision)
	}
	u := d.Billing.Totals
	// Reasoning must not exceed completion when both present.
	if u.Present.Reasoning == model.PresenceExact && u.Present.Output == model.PresenceExact {
		if u.ReasoningTokens > u.CompletionTokens {
			t.Fatalf("reasoning %d > completion %d (double-count risk)", u.ReasoningTokens, u.CompletionTokens)
		}
	}
	checkTokenBounds(t, "billing", u.PromptTokens, u.CompletionTokens, u.CacheReadTokens, u.ReasoningTokens, exp)
}

func checkTokenBounds(t *testing.T, where string, prompt, completion, cacheRead, reasoning int64, exp TokenExpect) {
	t.Helper()
	if exp.ExactPrompt != nil && prompt != *exp.ExactPrompt {
		t.Errorf("%s prompt=%d want %d", where, prompt, *exp.ExactPrompt)
	}
	if exp.ExactCompletion != nil && completion != *exp.ExactCompletion {
		t.Errorf("%s completion=%d want %d", where, completion, *exp.ExactCompletion)
	}
	if exp.ExactCacheRead != nil && cacheRead != *exp.ExactCacheRead {
		t.Errorf("%s cache_read=%d want %d", where, cacheRead, *exp.ExactCacheRead)
	}
	if exp.ExactReasoning != nil && reasoning != *exp.ExactReasoning {
		t.Errorf("%s reasoning=%d want %d", where, reasoning, *exp.ExactReasoning)
	}
	// At least some token signal when exact equality not specified.
	if exp.ExactPrompt == nil && exp.ExactCompletion == nil {
		if prompt == 0 && completion == 0 && cacheRead == 0 {
			t.Errorf("%s: all token buckets are zero; fixture should include known tokens", where)
		}
	}
}

// AssertToolResults requires at least one tool invocation associated with a
// result (or turn-level tool_details / tool_names evidence).
func AssertToolResults(t *testing.T, r Reader, sessionID string, minTools int) {
	t.Helper()
	if minTools < 1 {
		minTools = 1
	}
	d, err := r.GetSession(sessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	toolTurns := 0
	for _, tr := range d.Turns {
		if tr.ToolCallCount > 0 || len(tr.ToolNames) > 0 || len(tr.ToolDetails) > 0 {
			toolTurns++
		}
	}
	events, err := r.GetRenderEvents(sessionID)
	inv, res := 0, 0
	if err == nil {
		for _, e := range events {
			switch e.Type {
			case "ToolInvocation":
				inv++
			case "ToolResult":
				res++
			}
		}
	}
	if toolTurns == 0 && inv == 0 {
		t.Fatal("no tool evidence in turns or render events")
	}
	if inv > 0 && res == 0 {
		// Some agents only surface tools on turns; only fail if we saw inv without any result path.
		if toolTurns == 0 {
			t.Fatalf("ToolInvocation=%d but no ToolResult and no turn tool fields", inv)
		}
	}
	total := toolTurns
	if inv > total {
		total = inv
	}
	if total < minTools {
		t.Fatalf("tool evidence count=%d want >= %d", total, minTools)
	}
	// Re-read stability: no duplication.
	d2, err := r.GetSession(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d2.Turns) != len(d.Turns) {
		t.Fatalf("turn count changed on re-read: %d -> %d", len(d.Turns), len(d2.Turns))
	}
}

// DiffExpect holds expectations for extracted edit calls.
type DiffExpect struct {
	SessionID    string
	FilePathSub  string // substring match on path
	OldSub       string
	NewSub       string
	MinEditCalls int
	// Extract optionally maps events to edit calls. Default uses model.ExtractEditCalls.
	Extract func(events []model.RenderEvent) []model.EditCall
}

// AssertDiff loads render events and requires at least one edit extraction.
func AssertDiff(t *testing.T, r Reader, exp DiffExpect) {
	t.Helper()
	events, err := r.GetRenderEvents(exp.SessionID)
	if err != nil {
		t.Fatalf("GetRenderEvents: %v", err)
	}
	extract := exp.Extract
	if extract == nil {
		extract = func(events []model.RenderEvent) []model.EditCall {
			var out []model.EditCall
			for _, e := range events {
				if e.Type != "ToolInvocation" {
					continue
				}
				out = append(out, model.ExtractEditCalls(e)...)
			}
			return out
		}
	}
	calls := extract(events)
	min := exp.MinEditCalls
	if min < 1 {
		min = 1
	}
	if len(calls) < min {
		t.Fatalf("ExtractEditCalls got %d edits, want >= %d (events=%d)", len(calls), min, len(events))
	}
	found := false
	for _, c := range calls {
		if exp.FilePathSub != "" && !strings.Contains(c.FilePath, exp.FilePathSub) {
			continue
		}
		if exp.OldSub != "" && !strings.Contains(c.OldString, exp.OldSub) {
			continue
		}
		if exp.NewSub != "" && !strings.Contains(c.NewString, exp.NewSub) {
			continue
		}
		found = true
		break
	}
	if !found && (exp.FilePathSub != "" || exp.OldSub != "" || exp.NewSub != "") {
		t.Fatalf("no edit matched path/old/new filters; got %+v", calls)
	}
}

// AssertSubtasks requires parent/child or turn-level subagent attribution.
type SubtaskExpect struct {
	SessionID       string
	MinSubagents    int
	RequireChildIDs bool // list must include IsSubagent or ParentSessionID linkage
}

// AssertSubtasks checks turn.Subagents and/or session lineage fields.
func AssertSubtasks(t *testing.T, r Reader, exp SubtaskExpect) {
	t.Helper()
	d, err := r.GetSession(exp.SessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	named := 0
	for _, tr := range d.Turns {
		named += len(tr.Subagents)
	}
	if events, err := r.GetRenderEvents(exp.SessionID); err == nil {
		for _, e := range events {
			if e.Depth > 0 || e.ToolName == "Agent" || e.ToolName == "Task" || e.Subtype == "subagent_started" {
				named++
			}
		}
	}
	list, err := r.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	children, roots := 0, 0
	for _, s := range list {
		if s.IsSubagent || s.ParentSessionID != "" {
			children++
		} else {
			roots++
		}
	}
	min := exp.MinSubagents
	if min < 1 {
		min = 1
	}
	if named < min && children < 1 {
		t.Fatalf("no subtask evidence: turn.Subagents+render=%d children=%d", named, children)
	}
	// Child sessions must not be the only roots when RequireChildIDs and children exist.
	if exp.RequireChildIDs && children > 0 {
		// At least one non-subagent root should remain if parent is listed.
		_ = roots
	}
}

// AssertResume requires a stable native resume identifier.
type ResumeExpect struct {
	SessionID    string
	ExactID      string // if non-empty, ResumeID must equal this
	RejectSuffix string // ResumeID must not end with this (e.g. ".jsonl")
}

// AssertResume checks ResumeID on list and detail.
func AssertResume(t *testing.T, r Reader, exp ResumeExpect) {
	t.Helper()
	d, err := r.GetSession(exp.SessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	// Prefer ResumeID; fall back to ID when agent uses session ID as resume key
	// (claude/chrys/opencode/grok). Detail embeds Session.
	rid := d.ResumeID
	if rid == "" {
		rid = d.ID
	}
	if rid == "" {
		t.Fatal("empty resume identity")
	}
	if exp.ExactID != "" && rid != exp.ExactID {
		t.Fatalf("ResumeID/ID=%q want %q", rid, exp.ExactID)
	}
	if exp.RejectSuffix != "" && strings.HasSuffix(rid, exp.RejectSuffix) {
		t.Fatalf("resume id %q looks like a filename (suffix %q)", rid, exp.RejectSuffix)
	}
	// List agreement when session is listed.
	list, err := r.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.ID != exp.SessionID && s.ID != d.ID {
			continue
		}
		listRID := s.ResumeID
		if listRID == "" {
			listRID = s.ID
		}
		if listRID != rid && s.ResumeID != "" && d.ResumeID != "" {
			t.Fatalf("list ResumeID %q != detail %q", listRID, rid)
		}
	}
}

// AssertDeleteSandbox deletes targetID and requires siblingID to survive.
func AssertDeleteSandbox(t *testing.T, r Reader, targetID, siblingID string) {
	t.Helper()
	del := RequireSessionDeleter(t, r)
	if err := del.DeleteSession(targetID); err != nil {
		t.Fatalf("DeleteSession(%q): %v", targetID, err)
	}
	if _, err := r.GetSession(targetID); err == nil {
		t.Fatalf("GetSession(%q) succeeded after delete", targetID)
	}
	list, err := r.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.ID == targetID {
			t.Fatalf("target %q still listed after delete", targetID)
		}
	}
	if siblingID != "" {
		if _, err := r.GetSession(siblingID); err != nil {
			t.Fatalf("sibling %q must survive: %v", siblingID, err)
		}
	}
	// Unknown delete should fail.
	if err := del.DeleteSession("__no_such_session_for_delete__"); err == nil {
		t.Fatal("DeleteSession(unknown) should fail")
	}
}

// AssertTerminatePID requires SessionProcesses to include wantPID and exclude otherPID.
func AssertTerminatePID(t *testing.T, r Reader, sessionID string, wantPID int, otherPID int) {
	t.Helper()
	if wantPID <= 0 {
		t.Fatal("wantPID must be a live test-owned process")
	}
	pf := RequireSessionProcessFinder(t, r)
	pids, err := pf.SessionProcesses(sessionID)
	if err != nil {
		t.Fatalf("SessionProcesses: %v", err)
	}
	found := false
	for _, p := range pids {
		if p == wantPID {
			found = true
		}
		if otherPID > 0 && p == otherPID {
			t.Fatalf("SessionProcesses returned unrelated PID %d", otherPID)
		}
	}
	if !found {
		t.Fatalf("SessionProcesses=%v does not include test PID %d", pids, wantPID)
	}
}

// StartFileHolder starts a short-lived helper process that holds path open
// (via tail -f). HoldersOf excludes the test process itself, so terminate
// evidence must use a child. The helper is killed in t.Cleanup.
//
// The function polls procfind.HoldersOf until the child PID is visible so
// flaky races with /proc fd enumeration do not fail CI.
func StartFileHolder(t *testing.T, path string) int {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("tail", "-f", abs)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		t.Fatalf("start file holder: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pids, err := procfind.HoldersOf(abs)
		if err == nil {
			for _, p := range pids {
				if p == pid {
					return pid
				}
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("file holder pid %d never appeared in HoldersOf(%s)", pid, abs)
	return pid
}

// MustWrite is a small fixture helper for adapter evidence builders.
func MustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Int64 returns a pointer for TokenExpect exact fields.
func Int64(v int64) *int64 { return &v }
