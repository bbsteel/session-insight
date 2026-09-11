package worktreereview

import (
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
)

// Regression tests for PR #176 review findings.

func TestOversizedLineIsSkippedNotFatal(t *testing.T) {
	dir := t.TempDir()
	attemptID := "attempt_oversized_line"
	var b strings.Builder
	b.WriteString(`{"schema":"worktree-review.event/v1","sequence":1,"occurred_at":"2026-09-09T12:00:00.000Z","attempt_id":"` + attemptID + `","surface":"web","event_type":"attempt.created","payload":{"repository_display":"acme/big"},"reconstructed":false}` + "\n")
	// One pathological line beyond the per-line bound, then a valid line.
	b.WriteString(`{"schema":"worktree-review.event/v1","sequence":2,"big":"` + strings.Repeat("x", maxEventLineBytes+1024) + `"}` + "\n")
	b.WriteString(`{"schema":"worktree-review.event/v1","sequence":3,"occurred_at":"2026-09-09T12:00:02.000Z","attempt_id":"` + attemptID + `","surface":"web","event_type":"attempt.completed","payload":{"gate_state":"Passed"},"reconstructed":false}` + "\n")
	path := dir + "/events.jsonl"
	adaptertest.MustWrite(t, path, b.String())

	result, err := ReadEventsFile(path)
	if err != nil {
		t.Fatalf("oversized line must not fail the read: %v", err)
	}
	if len(result.Events) != 2 {
		t.Fatalf("events=%d want 2 (valid lines survive)", len(result.Events))
	}
	if result.SkippedMalformed != 1 {
		t.Fatalf("skippedMalformed=%d want 1", result.SkippedMalformed)
	}
	if result.Events[1].EventType != "attempt.completed" {
		t.Fatalf("second surviving event=%q", result.Events[1].EventType)
	}
}

func TestPathHelpersRejectInvalidIDs(t *testing.T) {
	for _, fn := range []func(string, string) string{MetadataPath, EventsPath, ResultPath} {
		if got := fn("/root", "../escape"); got != "" {
			t.Fatalf("invalid id produced path %q", got)
		}
	}
	if got := EventsPath("/root", fixturePassed); !strings.HasSuffix(got, "events.jsonl") {
		t.Fatalf("valid id path=%q", got)
	}
	// Containment: a hostile id can never escape the journal root, even via
	// a root that itself carries traversal segments.
	if got := AttemptDir("/root/../root", "attempt_ok"); got != "/root/attempt_ok" {
		t.Fatalf("containment-normalized dir=%q", got)
	}
}

func TestModelEvidencePreservesPartialPair(t *testing.T) {
	events := []ReviewEvent{
		{
			Sequence:  1,
			AttemptID: "attempt_partial",
			EventType: "provider_call.completed",
			Payload:   map[string]any{"model": "gpt-5.6"},
		},
	}
	modelName, provider := modelEvidence(events)
	if modelName != "gpt-5.6" || provider != "" {
		t.Fatalf("model=%q provider=%q", modelName, provider)
	}
}

func TestBuildTurnsToleratesLeadingNonStageEvent(t *testing.T) {
	events := []ReviewEvent{
		{
			Sequence:  1,
			AttemptID: "attempt_leading",
			EventType: "dimension.completed",
			Payload:   map[string]any{"dimension_id": "security", "elapsed_seconds": 1.0},
		},
	}
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("buildTurns panicked on leading dimension event: %v", rec)
		}
	}()
	turns := buildTurns(events)
	if len(turns) != 1 {
		t.Fatalf("turns=%d want 1 fallback turn", len(turns))
	}
	if len(turns[0].ToolDetails) != 1 || turns[0].ToolDetails[0].Name != "dimension:security" {
		t.Fatalf("tool details=%+v", turns[0].ToolDetails)
	}
}

func TestRenderTokenSummaryOmitsUnrecordedSide(t *testing.T) {
	events := []ReviewEvent{
		{
			Sequence:  1,
			AttemptID: "attempt_tokens",
			EventType: "provider_call.completed",
			Payload:   map[string]any{"provider": "openai", "call_ordinal": 1.0, "input_tokens": 100.0},
		},
	}
	rendered := eventsToRenderEvents(events)
	var stdout string
	for i := range rendered {
		if rendered[i].Type == "ToolResult" {
			stdout = rendered[i].Stdout
		}
	}
	if !strings.Contains(stdout, "in=100") {
		t.Fatalf("stdout=%q missing recorded input", stdout)
	}
	if strings.Contains(stdout, "out=0") {
		t.Fatalf("stdout=%q renders unrecorded output as zero", stdout)
	}
}

func TestMismatchedAttemptIDDiscarded(t *testing.T) {
	dir := t.TempDir()
	attemptID := "attempt_dir_name"
	adaptertest.MustWrite(t, MetadataPath(dir, attemptID),
		`{"schema":"worktree-review.session-metadata/v1","attempt_id":"attempt_OTHER","heartbeat_at":"2026-09-09T12:00:00.000Z","last_persisted_sequence":1}`+"\n")
	adaptertest.MustWrite(t, EventsPath(dir, attemptID),
		`{"schema":"worktree-review.event/v1","sequence":1,"occurred_at":"2026-09-09T12:00:00.000Z","attempt_id":"`+attemptID+`","surface":"web","event_type":"attempt.created","payload":{"repository_display":"acme/x"},"reconstructed":false}`+"\n")

	r := New(dir)
	detail, err := r.GetSession(attemptID)
	if err != nil {
		t.Fatal(err)
	}
	// The mismatched metadata was discarded: UpdatedAt falls back to the
	// event timestamp, and provenance records the mismatch.
	if !detail.UpdatedAt.Equal(detail.CreatedAt) {
		t.Fatalf("metadata should be discarded: created=%v updated=%v", detail.CreatedAt, detail.UpdatedAt)
	}
	found := false
	for _, warning := range detail.Provenance.Warnings {
		if warning.Code == "attempt_id_mismatch" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing attempt_id_mismatch warning: %+v", detail.Provenance.Warnings)
	}
}
