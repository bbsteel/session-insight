package pi

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestParseBasicSession(t *testing.T) {
	root := installTestdata(t, basicID, "basic.jsonl")
	detail, err := New(root).GetSession(basicID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Name != "demo session" {
		t.Fatalf("name=%q", detail.Name)
	}
	if detail.CWD != "/tmp/demo" {
		t.Fatalf("cwd=%q", detail.CWD)
	}
	if detail.ModelName != "claude-sonnet-4" || detail.ModelProvider != "anthropic" {
		t.Fatalf("model=%s provider=%s", detail.ModelName, detail.ModelProvider)
	}
	if detail.ResumeID != basicID {
		t.Fatalf("resume=%q", detail.ResumeID)
	}
	if detail.TurnCount != 1 || len(detail.Turns) != 1 {
		t.Fatalf("turns=%d count=%d", len(detail.Turns), detail.TurnCount)
	}
	if detail.Turns[0].UserMessage != "hello conformance" {
		t.Fatalf("user=%q", detail.Turns[0].UserMessage)
	}
	if detail.Turns[0].AssistantMessage != "hi there" {
		t.Fatalf("assistant=%q", detail.Turns[0].AssistantMessage)
	}
}

func TestParseRichTokensAndTools(t *testing.T) {
	id := "cccccccc-dddd-eeee-ffff-000000000001"
	root := installTestdata(t, id, "rich.jsonl")
	r := New(root)
	detail, err := r.GetSession(id)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Billing == nil || detail.Billing.Precision != model.PrecisionExact {
		t.Fatalf("billing=%+v", detail.Billing)
	}
	if detail.Billing.Totals.PromptTokens != 83 || detail.Billing.Totals.CompletionTokens != 28 {
		t.Fatalf("tokens prompt=%d completion=%d", detail.Billing.Totals.PromptTokens, detail.Billing.Totals.CompletionTokens)
	}
	if detail.Billing.Totals.ReasoningTokens != 5 {
		t.Fatalf("reasoning=%d", detail.Billing.Totals.ReasoningTokens)
	}
	events, err := r.GetRenderEvents(id)
	if err != nil {
		t.Fatal(err)
	}
	var edits []model.EditCall
	var results int
	for _, e := range events {
		if e.Type == "ToolInvocation" {
			edits = append(edits, model.ExtractEditCalls(e)...)
		}
		if e.Type == "ToolResult" {
			results++
			if e.ParentEventID == "" {
				t.Fatalf("result %s missing parent", e.ToolCallID)
			}
		}
	}
	if results < 3 {
		t.Fatalf("results=%d", results)
	}
	if len(edits) != 1 || edits[0].FilePath != "/tmp/demo/a.go" || edits[0].OldString != "foo" {
		t.Fatalf("edits=%+v", edits)
	}
}

func TestInterruptedTurnStaysOpenWithinLiveWindow(t *testing.T) {
	id := "dddddddd-eeee-ffff-0000-111111111111"
	root := t.TempDir()
	body, err := os.ReadFile(filepath.Join("testdata", "interrupted.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path := writeSessionFile(t, root, id, string(body))
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}
	events, err := New(root).GetRenderEvents(id)
	if err != nil {
		t.Fatal(err)
	}
	if !sharedHasInProgress(events) {
		t.Fatalf("expected trailing in_progress, last=%+v", events[len(events)-1])
	}
}

func sharedHasInProgress(events []model.RenderEvent) bool {
	if len(events) == 0 {
		return false
	}
	last := events[len(events)-1]
	return last.Type == "AgentSpecific" && last.Subtype == "in_progress"
}

func TestResolveSessionsDirOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", dir)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	got, ok := ResolveSessionsDir()
	if !ok || got != dir {
		t.Fatalf("got=%q ok=%v", got, ok)
	}
}

func TestSessionIDFromFilename(t *testing.T) {
	got := sessionIDFromFilename("2026-01-01T00-00-00-000Z_aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.jsonl")
	if got != basicID {
		t.Fatalf("got=%q", got)
	}
}

func TestDeleteRemovesDuplicateSessionFiles(t *testing.T) {
	root := t.TempDir()
	body, err := os.ReadFile(filepath.Join("testdata", "basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	first := writeSessionFileIn(t, root, "--tmp-a--", basicID, string(body))
	second := writeSessionFileIn(t, root, "--tmp-b--", basicID, string(body))
	r := New(root)
	if err := r.DeleteSession(basicID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("first copy still present: %v", err)
	}
	if _, err := os.Stat(second); !os.IsNotExist(err) {
		t.Fatalf("second copy still present: %v", err)
	}
	list, err := r.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range list {
		if session.ID == basicID {
			t.Fatal("deleted id still listed")
		}
	}
}

// TestDeleteRemovesDuplicateAfterListing primes the path cache the way a normal
// list does, then deletes. Every same-id file must go, not just the cached one,
// or the next list rebinds the id to the surviving copy.
func TestDeleteRemovesDuplicateAfterListing(t *testing.T) {
	root := t.TempDir()
	body, err := os.ReadFile(filepath.Join("testdata", "basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	first := writeSessionFileIn(t, root, "--tmp-a--", basicID, string(body))
	second := writeSessionFileIn(t, root, "--tmp-b--", basicID, string(body))
	r := New(root)
	list, err := r.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("list n=%d, want the duplicate collapsed into one id", len(list))
	}
	if err := r.DeleteSession(basicID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{first, second} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("copy %s still present after delete: %v", path, err)
		}
	}
	if _, err := r.GetSession(basicID); err == nil {
		t.Fatal("deleted session still resolvable")
	}
	list, err = r.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range list {
		if session.ID == basicID {
			t.Fatal("deleted id reappeared in listing")
		}
	}
}

// TestAssistantUsageFallsBackToCurrentModel covers an assistant message that
// bills usage but omits "model" after a model_change. The request belongs to
// the current model, not to "unknown".
func TestAssistantUsageFallsBackToCurrentModel(t *testing.T) {
	id := "11111111-2222-3333-4444-555555555555"
	root := t.TempDir()
	body := `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-01-04T00:00:00.000Z","cwd":"/tmp/demo"}
{"type":"model_change","id":"m1","parentId":null,"timestamp":"2026-01-04T00:00:01.000Z","provider":"anthropic","modelId":"claude-sonnet-4"}
{"type":"message","id":"u1","parentId":"m1","timestamp":"2026-01-04T00:00:02.000Z","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}
{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-01-04T00:00:03.000Z","message":{"role":"assistant","content":[{"type":"text","text":"hi"}],"usage":{"input":10,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":12,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0.001}},"stopReason":"stop"}}
`
	writeSessionFile(t, root, id, body)
	detail, err := New(root).GetSession(id)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Billing == nil || len(detail.Billing.ByModel) != 1 {
		t.Fatalf("billing=%+v", detail.Billing)
	}
	if got := detail.Billing.ByModel[0].Model; got != "claude-sonnet-4" {
		t.Fatalf("by-model key=%q, want claude-sonnet-4 (ModelName=%q)", got, detail.ModelName)
	}
}

// TestListTurnCountMatchesDetail locks list metadata to the same turn rules the
// replay parser uses: an assistant/tool-only opening counts as a turn, a user
// message that FilterEmptyTurns drops does not, and a user turn whose assistant
// reply is an empty error record still counts.
func TestListTurnCountMatchesDetail(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantTurns int
	}{
		{
			name:      "assistant opens the transcript",
			wantTurns: 1,
			body: `{"type":"session","version":3,"id":"ffffffff-0000-1111-2222-333333333333","timestamp":"2026-01-05T00:00:00.000Z","cwd":"/tmp/demo"}
{"type":"message","id":"a1","parentId":null,"timestamp":"2026-01-05T00:00:01.000Z","message":{"role":"assistant","content":[{"type":"text","text":"hi"}],"stopReason":"stop"}}
`,
		},
		{
			name:      "empty user messages are dropped",
			wantTurns: 0,
			body: `{"type":"session","version":3,"id":"ffffffff-0000-1111-2222-333333333333","timestamp":"2026-01-05T00:00:00.000Z","cwd":"/tmp/demo"}
{"type":"message","id":"u1","parentId":null,"timestamp":"2026-01-05T00:00:01.000Z","message":{"role":"user","content":[]}}
{"type":"message","id":"u2","parentId":"u1","timestamp":"2026-01-05T00:00:02.000Z","message":{"role":"user","content":[]}}
`,
		},
		{
			// A compaction summary is an event, not turn content, so both the
			// metadata scan and the detail view drop a compaction-only turn.
			name:      "compaction-only transcript has no turns",
			wantTurns: 0,
			body: `{"type":"session","version":3,"id":"ffffffff-0000-1111-2222-333333333333","timestamp":"2026-01-05T00:00:00.000Z","cwd":"/tmp/demo"}
{"type":"compaction","id":"c1","parentId":null,"timestamp":"2026-01-05T00:00:01.000Z","summary":"compacted earlier work"}
`,
		},
		{
			name:      "compaction does not split the open turn",
			wantTurns: 1,
			body: `{"type":"session","version":3,"id":"ffffffff-0000-1111-2222-333333333333","timestamp":"2026-01-05T00:00:00.000Z","cwd":"/tmp/demo"}
{"type":"message","id":"u1","parentId":null,"timestamp":"2026-01-05T00:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}
{"type":"compaction","id":"c1","parentId":"u1","timestamp":"2026-01-05T00:00:02.000Z","summary":"compacted earlier work"}
{"type":"message","id":"a1","parentId":"c1","timestamp":"2026-01-05T00:00:03.000Z","message":{"role":"assistant","content":[{"type":"text","text":"hello"}],"stopReason":"stop"}}
`,
		},
		{
			name:      "tool-only turn closes before next user turn",
			wantTurns: 1,
			body: `{"type":"session","version":3,"id":"ffffffff-0000-1111-2222-333333333333","timestamp":"2026-01-05T00:00:00.000Z","cwd":"/tmp/demo"}
{"type":"message","id":"a1","parentId":null,"timestamp":"2026-01-05T00:00:01.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"t1","name":"bash","arguments":{"command":"ls"}}],"stopReason":"toolUse"}}
{"type":"message","id":"t1r","parentId":"a1","timestamp":"2026-01-05T00:00:02.000Z","message":{"role":"toolResult","toolCallId":"t1","toolName":"bash","content":[{"type":"text","text":"ok"}]}}
`,
		},
		{
			// Pi records failed requests as an assistant message with empty
			// content and stopReason error. Those turns still carry the user
			// message, so the detail view keeps them and metadata must too.
			name:      "user turn with only an empty assistant error",
			wantTurns: 2,
			body: `{"type":"session","version":3,"id":"ffffffff-0000-1111-2222-333333333333","timestamp":"2026-01-05T00:00:00.000Z","cwd":"/tmp/demo"}
{"type":"message","id":"u1","parentId":null,"timestamp":"2026-01-05T00:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}
{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-01-05T00:00:02.000Z","message":{"role":"assistant","content":[],"model":"kimi-for-coding","stopReason":"error","errorMessage":"404 not found"}}
{"type":"message","id":"u2","parentId":"a1","timestamp":"2026-01-05T00:00:03.000Z","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}
{"type":"message","id":"a2","parentId":"u2","timestamp":"2026-01-05T00:00:04.000Z","message":{"role":"assistant","content":[],"model":"k3-256k","stopReason":"error","errorMessage":"404 not found"}}
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := "ffffffff-0000-1111-2222-333333333333"
			root := t.TempDir()
			writeSessionFile(t, root, id, tc.body)
			r := New(root)
			list, err := r.ListSessions()
			if err != nil || len(list) != 1 {
				t.Fatalf("list n=%d err=%v", len(list), err)
			}
			detail, err := r.GetSession(id)
			if err != nil {
				t.Fatal(err)
			}
			if list[0].TurnCount != detail.TurnCount {
				t.Fatalf("list TurnCount=%d, detail TurnCount=%d", list[0].TurnCount, detail.TurnCount)
			}
			if list[0].TurnCount != len(detail.Turns) {
				t.Fatalf("TurnCount=%d, detail turns=%d", list[0].TurnCount, len(detail.Turns))
			}
			if list[0].TurnCount != tc.wantTurns {
				t.Fatalf("TurnCount=%d, want %d", list[0].TurnCount, tc.wantTurns)
			}
		})
	}
}

// TestListTurnCountMatchesDetailForFixtures pins the metadata scan to the
// replay parser for every shipped fixture, so list and detail cannot drift.
func TestListTurnCountMatchesDetailForFixtures(t *testing.T) {
	fixtures := []struct {
		id   string
		file string
	}{
		{basicID, "basic.jsonl"},
		{"cccccccc-dddd-eeee-ffff-000000000001", "rich.jsonl"},
		{"dddddddd-eeee-ffff-0000-111111111111", "interrupted.jsonl"},
		{"bbbbbbbb-bbbb-cccc-dddd-eeeeeeeeeeee", "header-only.jsonl"},
		{"eeeeeeee-0000-1111-2222-333333333333", "failed-turns.jsonl"},
		{"99999999-0000-1111-2222-333333333333", "shell-and-usage.jsonl"},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.file, func(t *testing.T) {
			root := installTestdata(t, fixture.id, fixture.file)
			r := New(root)
			list, err := r.ListSessions()
			if err != nil || len(list) != 1 {
				t.Fatalf("list n=%d err=%v", len(list), err)
			}
			detail, err := r.GetSession(fixture.id)
			if err != nil {
				t.Fatal(err)
			}
			if list[0].TurnCount != detail.TurnCount || detail.TurnCount != len(detail.Turns) {
				t.Fatalf("list TurnCount=%d, detail TurnCount=%d, turns=%d",
					list[0].TurnCount, detail.TurnCount, len(detail.Turns))
			}
		})
	}
}

func TestIncompleteTailIsNotMalformed(t *testing.T) {
	root := t.TempDir()
	body, err := os.ReadFile(filepath.Join("testdata", "basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	writeSessionFile(t, root, basicID, string(body)+"{\"type\":\"message\",\"id\":\"tail\"")
	detail, err := New(root).GetSession(basicID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Provenance == nil || detail.Provenance.State != "complete" {
		t.Fatalf("provenance=%+v", detail.Provenance)
	}
}

func TestListUsesMetadataScan(t *testing.T) {
	root := installTestdata(t, basicID, "basic.jsonl")
	r := New(root)
	list, err := r.ListSessions()
	if err != nil || len(list) != 1 {
		t.Fatalf("list n=%d err=%v", len(list), err)
	}
	if list[0].ID != basicID || list[0].Name != "demo session" || list[0].ModelName != "claude-sonnet-4" {
		t.Fatalf("list=%+v", list[0])
	}
	if list[0].TurnCount != 1 || list[0].PreviewText == "" {
		t.Fatalf("counts=%+v", list[0])
	}
	detail, err := r.GetSession(basicID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Name != list[0].Name || detail.ResumeID != list[0].ResumeID {
		t.Fatalf("list/detail disagree: %+v vs %+v", list[0], detail.Session)
	}
}

func TestFindSessionFileUsesFilenameNotHeaderWalk(t *testing.T) {
	root := t.TempDir()
	body, err := os.ReadFile(filepath.Join("testdata", "basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	writeSessionFile(t, root, basicID, string(body))
	decoyDir := filepath.Join(root, "--tmp-decoy--")
	if err := os.MkdirAll(decoyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	decoy := filepath.Join(decoyDir, "2026-01-01T00-00-00-000Z_zzzzzzzz-zzzz-zzzz-zzzz-zzzzzzzzzzzz.jsonl")
	if err := os.WriteFile(decoy, body, 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(root)
	path := r.findSessionFile(basicID)
	if path == "" || filepath.Base(path) != "2026-01-01T00-00-00-000Z_"+basicID+".jsonl" {
		t.Fatalf("path=%q", path)
	}
}

// TestListScanCacheInvalidatesOnChange pins the size+mtime keyed listing cache:
// reconciliation must not re-decode an unchanged transcript, but a write has to
// be observed on the next list.
func TestListScanCacheInvalidatesOnChange(t *testing.T) {
	root := installTestdata(t, basicID, "basic.jsonl")
	r := New(root)
	list, err := r.ListSessions()
	if err != nil || len(list) != 1 {
		t.Fatalf("list n=%d err=%v", len(list), err)
	}
	path := r.knownSessionFile(basicID)
	if path == "" {
		t.Fatal("missing cached path")
	}

	// Prime the cache, then prove a change is picked up.
	if _, err := r.ListSessions(); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"session_info","id":"n2","parentId":"n1","timestamp":"2026-01-01T00:00:09.000Z","name":"renamed session"}` + "\n"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	list, err = r.ListSessions()
	if err != nil || len(list) != 1 {
		t.Fatalf("list after append n=%d err=%v", len(list), err)
	}
	if list[0].Name != "renamed session" {
		t.Fatalf("name=%q, want the appended rename", list[0].Name)
	}
}

func writeSessionFileIn(t *testing.T, root, cwdEnc, id, body string) string {
	t.Helper()
	dir := filepath.Join(root, cwdEnc)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "2026-01-01T00-00-00-000Z_"+id+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestDuplicateSessionIDBindsNewestCopy covers a crash copy or in-progress
// rewrite of one session id: listing and every lookup must bind to the newest
// file, not to whichever directory ReadDir happened to return first.
func TestDuplicateSessionIDBindsNewestCopy(t *testing.T) {
	root := t.TempDir()
	oldBody := `{"type":"session","version":3,"id":"` + basicID + `","timestamp":"2020-01-01T00:00:00.000Z","cwd":"/tmp/demo"}
{"type":"message","id":"u1","parentId":null,"timestamp":"2020-01-01T00:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"old copy"}]}}
`
	newBody := `{"type":"session","version":3,"id":"` + basicID + `","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/tmp/demo"}
{"type":"message","id":"u1","parentId":null,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"new copy"}]}}
`
	// "--tmp-a--" sorts before "--tmp-z--", so directory order alone would
	// pick the stale copy.
	oldPath := writeSessionFileIn(t, root, "--tmp-a--", basicID, oldBody)
	newPath := writeSessionFileIn(t, root, "--tmp-z--", basicID, newBody)
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(oldPath, base, base); err != nil {
		t.Fatal(err)
	}
	newer := base.Add(24 * time.Hour)
	if err := os.Chtimes(newPath, newer, newer); err != nil {
		t.Fatal(err)
	}

	r := New(root)
	list, err := r.ListSessions()
	if err != nil || len(list) != 1 {
		t.Fatalf("list n=%d err=%v", len(list), err)
	}
	if got := r.knownSessionFile(basicID); got != newPath {
		t.Fatalf("listed path=%q, want newest %q", got, newPath)
	}
	detail, err := r.GetSession(basicID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Turns) != 1 || detail.Turns[0].UserMessage != "new copy" {
		t.Fatalf("detail came from the stale copy: %+v", detail.Turns)
	}

	// A newer duplicate appearing after the cache was primed must win too;
	// the cached path may not outlive a newer same-id file.
	if got := r.findSessionFile(basicID); got != newPath {
		t.Fatalf("findSessionFile=%q, want %q", got, newPath)
	}
	if err := os.Chtimes(oldPath, newer.Add(time.Hour), newer.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := r.findSessionFile(basicID); got != oldPath {
		t.Fatalf("findSessionFile=%q after the other copy became newest, want %q", got, oldPath)
	}
}

// TestDeleteRefusesLiveSession pins the delete safety signal that replaces an
// exact PID: Pi holds no transcript descriptor while idle, so a recently
// written transcript is treated as an owned live session.
func TestDeleteRefusesLiveSession(t *testing.T) {
	root := t.TempDir()
	path := writeSessionFile(t, root, basicID, `{"type":"session","version":3,"id":"`+basicID+`","timestamp":"`+time.Now().UTC().Format(time.RFC3339Nano)+`","cwd":"/tmp/demo"}
{"type":"message","id":"u1","parentId":null,"timestamp":"`+time.Now().UTC().Format(time.RFC3339Nano)+`","message":{"role":"user","content":[{"type":"text","text":"still working"}]}}
`)
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}
	r := New(root)
	running, err := r.SessionRunning(basicID)
	if err != nil {
		t.Fatal(err)
	}
	if !running {
		t.Fatal("a transcript written inside LiveWindow must count as running (Pi holds no fd while idle)")
	}

	// Once the transcript is older than LiveWindow the session is treated as
	// stopped, so the delete guard releases.
	stale := now.Add(-(model.LiveWindow + time.Minute))
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatal(err)
	}
	running, err = New(root).SessionRunning(basicID)
	if err != nil {
		t.Fatal(err)
	}
	if running {
		t.Fatal("a transcript older than LiveWindow must not count as running")
	}
}

// TestBashExecutionMessageReplaysCommandAndOutput covers Pi's direct !/!!
// shell messages, which are persisted as their own message role rather than as
// tool results.
func TestBashExecutionMessageReplaysCommandAndOutput(t *testing.T) {
	id := "99999999-0000-1111-2222-333333333333"
	root := installTestdata(t, id, "shell-and-usage.jsonl")
	r := New(root)
	events, err := r.GetRenderEvents(id)
	if err != nil {
		t.Fatal(err)
	}
	var invocations, results int
	var sawCommand, sawOutput, sawFailure, sawCancelled bool
	for _, e := range events {
		switch e.Type {
		case "ToolInvocation":
			invocations++
			if e.ToolName != "bash" {
				t.Fatalf("invocation tool=%q", e.ToolName)
			}
			if cmd, _ := e.ToolInput["command"].(string); cmd == "git status --short" {
				sawCommand = true
			}
		case "ToolResult":
			results++
			if e.ParentEventID == "" {
				t.Fatal("bash result has no parent invocation")
			}
			if e.Stdout == " M a.go" {
				sawOutput = true
			}
			if e.ExitCode == 3 && e.ErrorKind != "" {
				sawFailure = true
			}
			if e.Rejected && e.ErrorKind == "cancelled" {
				sawCancelled = true
			}
		}
	}
	if invocations != 3 || results != 3 {
		t.Fatalf("invocations=%d results=%d, want 3/3", invocations, results)
	}
	if !sawCommand || !sawOutput {
		t.Fatalf("command/output lost: command=%v output=%v", sawCommand, sawOutput)
	}
	if !sawFailure {
		t.Fatal("non-zero exit must carry an error outcome")
	}
	if !sawCancelled {
		t.Fatal("a cancelled command must not render as success")
	}

	detail, err := r.GetSession(id)
	if err != nil {
		t.Fatal(err)
	}
	if detail.TurnCount != 1 || len(detail.Turns) != 1 {
		t.Fatalf("turns=%d count=%d", len(detail.Turns), detail.TurnCount)
	}
	if detail.Turns[0].ToolCallCount != 3 {
		t.Fatalf("tool calls=%d", detail.Turns[0].ToolCallCount)
	}
	if detail.Turns[0].AssistantMessage != "a.go is modified" {
		t.Fatalf("assistant=%q", detail.Turns[0].AssistantMessage)
	}
	list, err := r.ListSessions()
	if err != nil || len(list) != 1 {
		t.Fatalf("list n=%d err=%v", len(list), err)
	}
	if list[0].TurnCount != detail.TurnCount {
		t.Fatalf("list TurnCount=%d, detail TurnCount=%d", list[0].TurnCount, detail.TurnCount)
	}
}

// TestStandaloneUsageEntryBillsItsOwnModel covers Pi's UsageEntry.model, which
// is distinct from model_change's modelId (cache warming bills another model).
func TestStandaloneUsageEntryBillsItsOwnModel(t *testing.T) {
	id := "99999999-0000-1111-2222-333333333333"
	root := installTestdata(t, id, "shell-and-usage.jsonl")
	detail, err := New(root).GetSession(id)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Billing == nil || len(detail.Billing.ByModel) != 2 {
		t.Fatalf("billing=%+v", detail.Billing)
	}
	buckets := map[string]model.ModelUsage{}
	for _, usage := range detail.Billing.ByModel {
		buckets[usage.Model] = usage
	}
	secondary, ok := buckets["secondary-review-model"]
	if !ok {
		t.Fatalf("by-model=%v, want a secondary-review-model bucket", detail.Billing.ByModel)
	}
	if secondary.Usage.PromptTokens != 10 || secondary.Usage.CompletionTokens != 1 {
		t.Fatalf("secondary bucket=%+v", secondary.Usage)
	}
	if secondary.BillingAmount != 0.002 {
		t.Fatalf("secondary cost=%v", secondary.BillingAmount)
	}
	// The selected session model must stay unchanged by an auxiliary record.
	if detail.ModelName != "claude-sonnet-4" {
		t.Fatalf("ModelName=%q", detail.ModelName)
	}
}
