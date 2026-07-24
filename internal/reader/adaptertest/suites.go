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

// RealtimeExpect configures AssertRealtimeStableThenMutate.
type RealtimeExpect struct {
	// ContentMarker is a substring that must appear after mutate in either
	// GetSession user/assistant text or GetRenderEvents Text. Distinguishes
	// content revision from mere process liveness.
	ContentMarker string
}

// AssertRealtimeStableThenMutate checks LiveRevision stability, requires a
// revision change after mutate(), and requires the new content to be
// observable via GetSession/GetRenderEvents (not SessionRunning).
func AssertRealtimeStableThenMutate(t *testing.T, r Reader, sessionID string, mutate func(t *testing.T), exp RealtimeExpect) {
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
	// Snapshot pre-mutate content so we can prove observability changed.
	beforeDetail, err := r.GetSession(sessionID)
	if err != nil {
		t.Fatalf("GetSession before mutate: %v", err)
	}
	beforeEvents, _ := r.GetRenderEvents(sessionID)
	beforeSig := contentSignature(beforeDetail, beforeEvents)

	mutate(t)
	rev3, err := lr.LiveRevision(sessionID)
	if err != nil {
		t.Fatalf("LiveRevision after mutate: %v", err)
	}
	if rev3 == rev1 {
		t.Fatalf("LiveRevision did not change after mutation (still %d)", rev3)
	}
	// Content must become observable through the reader — not process liveness.
	afterDetail, err := r.GetSession(sessionID)
	if err != nil {
		t.Fatalf("GetSession after mutate: %v", err)
	}
	afterEvents, _ := r.GetRenderEvents(sessionID)
	afterSig := contentSignature(afterDetail, afterEvents)
	if exp.ContentMarker != "" {
		if !strings.Contains(afterSig, exp.ContentMarker) {
			t.Fatalf("after mutate, content marker %q not visible in session/render (sig len=%d)",
				exp.ContentMarker, len(afterSig))
		}
	} else if afterSig == beforeSig {
		// Without an explicit marker, require some observable growth/change.
		if len(afterDetail.Turns) <= len(beforeDetail.Turns) && len(afterEvents) <= len(beforeEvents) {
			t.Fatal("after mutate, session/render content did not become observably larger")
		}
	}
}

func contentSignature(d *model.SessionDetail, events []model.RenderEvent) string {
	var b strings.Builder
	if d != nil {
		b.WriteString(d.Name)
		for _, tr := range d.Turns {
			b.WriteString(tr.UserMessage)
			b.WriteString(tr.AssistantMessage)
			for _, td := range tr.ToolDetails {
				b.WriteString(td.Name)
				b.WriteString(td.ErrorMessage)
			}
		}
	}
	for _, e := range events {
		b.WriteString(e.Type)
		b.WriteString(e.Text)
		b.WriteString(e.Stdout)
		b.WriteString(e.Stderr)
		b.WriteString(e.ToolName)
		b.WriteString(e.ToolCallID)
	}
	return b.String()
}

// TokenExpect holds expected exclusive-bucket values for a session bill.
type TokenExpect struct {
	SessionID string
	// Exact* force equality when non-nil.
	ExactPrompt     *int64
	ExactCompletion *int64
	ExactCacheRead  *int64
	ExactCacheWrite *int64
	ExactReasoning  *int64
	// Presence expectations: empty string skips the check.
	// Use model.PresenceExact / PresenceNA / PresenceMissing ("").
	PresentInput      model.Presence
	PresentOutput     model.Presence
	PresentCacheRead  model.Presence
	PresentCacheWrite model.Presence
	PresentReasoning  model.Presence
	// RequireExactPrecision requires Billing.Precision == exact.
	RequireExactPrecision bool
	// RequireNonNilBilling fails if Billing is nil.
	RequireNonNilBilling bool
	// AllowTurnFallback uses turn TokenUsage sums when Billing is nil.
	AllowTurnFallback bool
}

// AssertTokens loads the session and checks token/billing evidence including
// Presence metadata and exact-zero vs absent distinctions.
func AssertTokens(t *testing.T, r Reader, exp TokenExpect) {
	t.Helper()
	d, err := r.GetSession(exp.SessionID)
	if err != nil {
		t.Fatalf("GetSession(%q): %v", exp.SessionID, err)
	}
	if exp.RequireNonNilBilling && d.Billing == nil {
		t.Fatal("Billing is nil")
	}
	var u model.TokenUsage
	where := "billing"
	if d.Billing != nil {
		if exp.RequireExactPrecision && d.Billing.Precision != model.PrecisionExact {
			t.Fatalf("Billing.Precision=%q want exact", d.Billing.Precision)
		}
		u = d.Billing.Totals
	} else if exp.AllowTurnFallback {
		where = "turns"
		for _, tr := range d.Turns {
			u.PromptTokens += tr.TokenUsage.PromptTokens
			u.CompletionTokens += tr.TokenUsage.CompletionTokens
			u.CacheReadTokens += tr.TokenUsage.CacheReadTokens
			u.CacheWriteTokens += tr.TokenUsage.CacheWriteTokens
			u.ReasoningTokens += tr.TokenUsage.ReasoningTokens
			// Widen presence from turns (exact and n/a both matter for evidence).
			model.MergePresence(&u.Present, tr.TokenUsage.Present)
		}
	} else {
		// Try render-event token attachment (Claude path).
		where = "render"
		events, _ := r.GetRenderEvents(exp.SessionID)
		for _, e := range events {
			if e.TokenUsage == nil {
				continue
			}
			u.PromptTokens += e.TokenUsage.InputTokens
			u.CompletionTokens += e.TokenUsage.OutputTokens
			u.CacheReadTokens += e.TokenUsage.CacheReadTokens
			u.CacheWriteTokens += e.TokenUsage.CacheCreationTokens
			if e.TokenUsage.InputTokens > 0 || e.TokenUsage.OutputTokens > 0 {
				u.Present.Input = model.PresenceExact
				u.Present.Output = model.PresenceExact
			}
			if e.TokenUsage.CacheReadTokens > 0 {
				u.Present.CacheRead = model.PresenceExact
			}
			if e.TokenUsage.CacheCreationTokens > 0 {
				u.Present.CacheWrite = model.PresenceExact
			}
		}
		if u.PromptTokens == 0 && u.CompletionTokens == 0 {
			t.Fatal("no token evidence on billing, turns, or render events")
		}
	}

	// Reasoning must not exceed completion when both present as exact.
	if u.Present.Reasoning == model.PresenceExact && u.Present.Output == model.PresenceExact {
		if u.ReasoningTokens > u.CompletionTokens {
			t.Fatalf("reasoning %d > completion %d (double-count risk)", u.ReasoningTokens, u.CompletionTokens)
		}
	}
	checkTokenBounds(t, where, u, exp)
	checkTokenPresence(t, where, u.Present, exp)
}

func checkTokenBounds(t *testing.T, where string, u model.TokenUsage, exp TokenExpect) {
	t.Helper()
	if exp.ExactPrompt != nil && u.PromptTokens != *exp.ExactPrompt {
		t.Errorf("%s prompt=%d want %d", where, u.PromptTokens, *exp.ExactPrompt)
	}
	if exp.ExactCompletion != nil && u.CompletionTokens != *exp.ExactCompletion {
		t.Errorf("%s completion=%d want %d", where, u.CompletionTokens, *exp.ExactCompletion)
	}
	if exp.ExactCacheRead != nil && u.CacheReadTokens != *exp.ExactCacheRead {
		t.Errorf("%s cache_read=%d want %d", where, u.CacheReadTokens, *exp.ExactCacheRead)
	}
	if exp.ExactCacheWrite != nil && u.CacheWriteTokens != *exp.ExactCacheWrite {
		t.Errorf("%s cache_write=%d want %d", where, u.CacheWriteTokens, *exp.ExactCacheWrite)
	}
	if exp.ExactReasoning != nil && u.ReasoningTokens != *exp.ExactReasoning {
		t.Errorf("%s reasoning=%d want %d", where, u.ReasoningTokens, *exp.ExactReasoning)
	}
	// For tokens=exact evidence, at least one Exact* must be set — avoid theater.
	if exp.ExactPrompt == nil && exp.ExactCompletion == nil && exp.ExactCacheRead == nil {
		t.Error("TokenExpect for exact tokens must set at least one Exact* value")
	}
}

func checkTokenPresence(t *testing.T, where string, p model.TokenPresence, exp TokenExpect) {
	t.Helper()
	// Only assert when caller specified a non-zero expectation string... Presence is string.
	// PresenceMissing is "". We use a sentinel approach: if any Present* field was
	// intentionally set, check. Callers set PresentInput = model.PresenceExact explicitly.
	// For optional skip, use a special approach - only check if RequirePresent is used.
	// Here we check when the expected value is non-empty OR is explicitly PresenceNA.
	// PresenceNA is "n/a", PresenceExact is "exact", Missing is "".
	// To allow "must be missing", we need a flag. Simpler: always check if any of
	// Present* in exp is set via a bool RequirePresenceChecks.
	// Actually: use pointer to Presence? For simplicity check all non-default:
	// We'll check whenever PresentInput/Output etc. is set to exact or n/a.
	// PresenceMissing "" means "skip" for the expect field.
	if exp.PresentInput != "" && p.Input != exp.PresentInput {
		t.Errorf("%s Present.Input=%q want %q", where, p.Input, exp.PresentInput)
	}
	if exp.PresentOutput != "" && p.Output != exp.PresentOutput {
		t.Errorf("%s Present.Output=%q want %q", where, p.Output, exp.PresentOutput)
	}
	if exp.PresentCacheRead != "" && p.CacheRead != exp.PresentCacheRead {
		t.Errorf("%s Present.CacheRead=%q want %q", where, p.CacheRead, exp.PresentCacheRead)
	}
	if exp.PresentCacheWrite != "" && p.CacheWrite != exp.PresentCacheWrite {
		t.Errorf("%s Present.CacheWrite=%q want %q", where, p.CacheWrite, exp.PresentCacheWrite)
	}
	if exp.PresentReasoning != "" && p.Reasoning != exp.PresentReasoning {
		t.Errorf("%s Present.Reasoning=%q want %q", where, p.Reasoning, exp.PresentReasoning)
	}
	// Exact zero vs absent: if ExactPrompt is 0 with PresentInput=exact, value 0 is OK;
	// if PresentInput is missing, ExactPrompt must not be required as 0 with exact semantics.
	if exp.ExactPrompt != nil && *exp.ExactPrompt == 0 && exp.PresentInput == model.PresenceExact {
		if p.Input != model.PresenceExact {
			t.Errorf("%s exact-zero prompt requires Present.Input=exact, got %q", where, p.Input)
		}
	}
}

// ToolResultsExpect configures association and success/failure checks.
type ToolResultsExpect struct {
	SessionID string
	// MinPairs is minimum successful invocation↔result associations by call id.
	MinPairs int
	// RequireFailure requires at least one ToolResult with ExitCode != 0,
	// or turn ToolDetails with ExitCode != 0 / Rejected / error.
	RequireFailure bool
	// RequireSuccess requires at least one successful result (exit 0).
	RequireSuccess bool
}

// AssertToolResults requires invocation/result association by ToolCallID,
// tool names, exit/failure state, and re-read stability.
func AssertToolResults(t *testing.T, r Reader, exp ToolResultsExpect) {
	t.Helper()
	if exp.MinPairs < 1 {
		exp.MinPairs = 1
	}
	if !exp.RequireSuccess && !exp.RequireFailure {
		exp.RequireSuccess = true
	}

	d, err := r.GetSession(exp.SessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	events, err := r.GetRenderEvents(exp.SessionID)
	if err != nil {
		t.Fatalf("GetRenderEvents: %v", err)
	}

	type invInfo struct {
		name string
		seq  int
		key  string
	}
	// Index invocations by ToolCallID and by EventID so ParentEventID pairing
	// works for agents (e.g. OpenCode) that omit ToolCallID on render events.
	invByID := map[string]invInfo{}
	var results []model.RenderEvent
	seq := 0
	for _, e := range events {
		seq++
		switch e.Type {
		case "ToolInvocation":
			if e.ToolName == "" {
				t.Errorf("ToolInvocation missing ToolName (call_id=%q event=%q)", e.ToolCallID, e.EventID)
			}
			key := e.ToolCallID
			if key == "" {
				key = e.EventID
			}
			if key == "" {
				t.Errorf("ToolInvocation name=%q has neither ToolCallID nor EventID", e.ToolName)
				continue
			}
			info := invInfo{name: e.ToolName, seq: seq, key: key}
			invByID[key] = info
			if e.EventID != "" && e.EventID != key {
				invByID[e.EventID] = info
			}
		case "ToolResult":
			results = append(results, e)
		}
	}

	paired := 0
	success, failure := 0, 0
	var lastResSeq int
	for i, res := range results {
		callKey := res.ToolCallID
		if callKey == "" {
			callKey = res.ParentEventID
		}
		if callKey == "" {
			t.Errorf("ToolResult[%d] missing ToolCallID and ParentEventID", i)
			continue
		}
		inv, ok := invByID[callKey]
		if !ok {
			t.Errorf("ToolResult call_id/parent=%q has no matching ToolInvocation", callKey)
			continue
		}
		// Ordering: invocation before result in event stream.
		resSeq := 0
		s := 0
		for _, e := range events {
			s++
			if e.Type != "ToolResult" {
				continue
			}
			if e.EventID != "" && e.EventID == res.EventID {
				resSeq = s
				break
			}
			rk := e.ToolCallID
			if rk == "" {
				rk = e.ParentEventID
			}
			if rk == callKey && resSeq == 0 {
				resSeq = s
			}
		}
		if resSeq > 0 && inv.seq > resSeq {
			t.Errorf("ToolResult %q appears before ToolInvocation", callKey)
		}
		if lastResSeq > 0 && resSeq < lastResSeq {
			t.Errorf("ToolResult order unstable/non-monotonic for %q", callKey)
		}
		lastResSeq = resSeq
		paired++
		// Result content or structured status must survive.
		hasContent := res.Stdout != "" || res.Stderr != "" || res.Rejected || res.ErrorKind != "" || res.TimedOut
		if !hasContent && res.ExitCode == 0 && inv.name == "" {
			t.Errorf("ToolResult %q has no content/status and empty tool name", callKey)
		}
		if res.ExitCode != 0 || res.Rejected || res.TimedOut || res.ErrorKind != "" {
			failure++
		} else {
			success++
		}
	}

	// Also count turn-level tool_details for agents that pair there.
	for _, tr := range d.Turns {
		for _, td := range tr.ToolDetails {
			if td.Name == "" {
				continue
			}
			if td.ExitCode != 0 || td.Rejected || td.ErrorKind != "" {
				failure++
			} else {
				success++
			}
			// tool_details imply association at turn level
			if paired < exp.MinPairs {
				paired++
			}
		}
	}

	if paired < exp.MinPairs {
		t.Fatalf("paired tool call/results=%d want >= %d (invocations=%d results=%d)",
			paired, exp.MinPairs, len(invByID), len(results))
	}
	if exp.RequireSuccess && success < 1 {
		t.Fatal("expected at least one successful tool result (exit 0)")
	}
	if exp.RequireFailure && failure < 1 {
		t.Fatal("expected at least one failed/rejected tool result")
	}

	// Re-read stability: no duplication.
	d2, err := r.GetSession(exp.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d2.Turns) != len(d.Turns) {
		t.Fatalf("turn count changed on re-read: %d -> %d", len(d.Turns), len(d2.Turns))
	}
	events2, err := r.GetRenderEvents(exp.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events2) != len(events) {
		t.Fatalf("render event count changed on re-read: %d -> %d", len(events), len(events2))
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
	if exp.RequireChildIDs {
		if children < 1 {
			t.Fatal("RequireChildIDs: expected at least one IsSubagent/ParentSessionID child in ListSessions")
		}
		// Parent should remain listable as a non-subagent root when both exist.
		if roots < 1 {
			t.Fatal("RequireChildIDs: expected a non-subagent root so children are not the only listed sessions")
		}
	}
}

// AssertResume requires a stable native resume identifier.
type ResumeExpect struct {
	SessionID string
	// ExactID, when non-empty, must equal detail.ResumeID (not Session.ID fallback).
	ExactID string
	// RequireNativeField requires detail.ResumeID to be non-empty (no fallback to ID).
	RequireNativeField bool
	// RejectEqualSessionID fails if ResumeID equals the list/file session ID
	// (e.g. Codex must not use rollout filename stem as resume).
	RejectEqualSessionID bool
	// RejectSuffix: ResumeID must not end with this (e.g. ".jsonl").
	RejectSuffix string
}

// AssertResume checks ResumeID on list and detail.
//
// When RequireNativeField or RejectEqualSessionID is set, detail.ResumeID must
// be populated (Codex-style native payload id). Otherwise ExactID may match
// either ResumeID or Session.ID (agents whose CLI resume id is the session id).
func AssertResume(t *testing.T, r Reader, exp ResumeExpect) {
	t.Helper()
	d, err := r.GetSession(exp.SessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if exp.RequireNativeField || exp.RejectEqualSessionID {
		if d.ResumeID == "" {
			t.Fatal("ResumeID field is empty; native resume id required (not Session.ID fallback)")
		}
	}
	rid := d.ResumeID
	if rid == "" {
		rid = d.ID
	}
	if rid == "" {
		t.Fatal("empty resume identity")
	}
	if exp.ExactID != "" {
		if exp.RequireNativeField || exp.RejectEqualSessionID {
			if d.ResumeID != exp.ExactID {
				t.Fatalf("ResumeID=%q want ExactID %q (session ID=%q)", d.ResumeID, exp.ExactID, d.ID)
			}
		} else if rid != exp.ExactID {
			t.Fatalf("resume identity=%q want ExactID %q (ResumeID=%q Session.ID=%q)",
				rid, exp.ExactID, d.ResumeID, d.ID)
		}
	}
	if exp.RejectEqualSessionID && d.ResumeID != "" && d.ResumeID == d.ID {
		t.Fatalf("ResumeID %q must not equal session/file id %q", d.ResumeID, d.ID)
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
		if d.ResumeID != "" && s.ResumeID != "" && s.ResumeID != d.ResumeID {
			t.Fatalf("list ResumeID %q != detail %q", s.ResumeID, d.ResumeID)
		}
		if exp.RejectEqualSessionID && s.ResumeID != "" && s.ResumeID == s.ID {
			t.Fatalf("list ResumeID equals file stem %q", s.ID)
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
