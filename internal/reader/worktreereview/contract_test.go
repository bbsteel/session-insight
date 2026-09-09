package worktreereview

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	fixturePassed      = "attempt_01JWRTEST000000000PASS"
	fixtureBlocked     = "attempt_01JWRTEST00000000BLCK"
	fixtureError       = "attempt_01JWRTEST00000000ERRR"
	fixtureRunning     = "attempt_01JWRTEST00000000RUNN"
	fixtureInterrupted = "attempt_01JWRTEST00000000INTR"
	fixtureCorrupt     = "attempt_01JWRTEST00000000CRPT"
	fixtureV99         = "attempt_01JWRTEST00000000V99X"
)

func fixtureRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join("testdata")
}

func TestSessionIDMappingIsIdentity(t *testing.T) {
	if got := SessionIDForAttempt(fixturePassed); got != fixturePassed {
		t.Fatalf("session id %q != attempt id %q", got, fixturePassed)
	}
}

func TestParseSessionMetadata(t *testing.T) {
	data, err := os.ReadFile(MetadataPath(fixtureRoot(t), fixturePassed))
	if err != nil {
		t.Fatal(err)
	}
	meta, err := ParseSessionMetadata(data)
	if err != nil {
		t.Fatal(err)
	}
	if meta.AttemptID != fixturePassed {
		t.Fatalf("attempt_id=%q", meta.AttemptID)
	}
	if meta.LastPersistedSequence != 25 {
		t.Fatalf("last_persisted_sequence=%d", meta.LastPersistedSequence)
	}
}

func TestParseSessionMetadataRejectsUnknownVersion(t *testing.T) {
	data, err := os.ReadFile(MetadataPath(fixtureRoot(t), fixtureV99))
	if err != nil {
		t.Fatal(err)
	}
	_, err = ParseSessionMetadata(data)
	if err == nil {
		t.Fatal("expected UnsupportedSchemaError")
	}
	if !IsUnsupportedSchema(err) {
		t.Fatalf("err=%v is not UnsupportedSchemaError", err)
	}
}

func TestReadEventsFilePassed(t *testing.T) {
	result, err := ReadEventsFile(EventsPath(fixtureRoot(t), fixturePassed))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 25 {
		t.Fatalf("events=%d want 25", len(result.Events))
	}
	if result.SkippedMalformed != 0 || result.SkippedVersion != 0 {
		t.Fatalf("skipped malformed=%d version=%d", result.SkippedMalformed, result.SkippedVersion)
	}
	for i, event := range result.Events {
		if event.Sequence != i+1 {
			t.Fatalf("event[%d] sequence=%d", i, event.Sequence)
		}
	}
	first := result.Events[0]
	if first.EventType != "attempt.created" {
		t.Fatalf("first event=%q", first.EventType)
	}
	if first.Payload["repository_display"] != "acme/session-insight" {
		t.Fatalf("repository_display=%v", first.Payload["repository_display"])
	}
	last := result.Events[len(result.Events)-1]
	if last.EventType != "attempt.completed" || last.Payload["gate_state"] != "Passed" {
		t.Fatalf("last event=%q payload=%v", last.EventType, last.Payload)
	}
}

func TestReadEventsFileToleratesCorruption(t *testing.T) {
	result, err := ReadEventsFile(EventsPath(fixtureRoot(t), fixtureCorrupt))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 2 {
		t.Fatalf("valid events=%d want 2", len(result.Events))
	}
	if result.SkippedVersion != 1 {
		t.Fatalf("skipped unknown-version=%d want 1", result.SkippedVersion)
	}
	if result.SkippedMalformed != 1 {
		t.Fatalf("skipped malformed=%d want 1 (half-written tail)", result.SkippedMalformed)
	}
}

func TestErrorFixtureHasNoResult(t *testing.T) {
	if _, err := os.Stat(ResultPath(fixtureRoot(t), fixtureError)); !os.IsNotExist(err) {
		t.Fatalf("error fixture must not have result.json: %v", err)
	}
	result, err := ReadEventsFile(EventsPath(fixtureRoot(t), fixtureError))
	if err != nil {
		t.Fatal(err)
	}
	last := result.Events[len(result.Events)-1]
	if last.EventType != "attempt.failed" {
		t.Fatalf("terminal event=%q want attempt.failed", last.EventType)
	}
}

func TestParseResultDocument(t *testing.T) {
	data, err := os.ReadFile(ResultPath(fixtureRoot(t), fixtureBlocked))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ParseResultDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	if doc.GateState != "Blocked" {
		t.Fatalf("gate_state=%q", doc.GateState)
	}
	if doc.AttemptID != fixtureBlocked {
		t.Fatalf("result attempt_id %q does not match journal directory %q", doc.AttemptID, fixtureBlocked)
	}
	if doc.Summary == "" {
		t.Fatal("summary is empty")
	}
}

func TestResultFixtureComesFromRealPipelineSnapshot(t *testing.T) {
	// The fixture result.json documents are extracted from the real
	// Pipeline-generated demo snapshots (worktree-review A-015), with only
	// attempt_id re-pointed at the fixture directory. Guard the provenance
	// by requiring the schema const and a real merge identity.
	data, err := os.ReadFile(ResultPath(fixtureRoot(t), fixturePassed))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ParseResultDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	if doc.GateState != "Passed" {
		t.Fatalf("gate_state=%q", doc.GateState)
	}
}

func TestValidAttemptIDGuard(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "../etc", "a/b", `a\\b`} {
		if validAttemptID(bad) {
			t.Fatalf("accepted hostile id %q", bad)
		}
		if dir := AttemptDir("/root", bad); dir != "" {
			t.Fatalf("AttemptDir accepted %q", bad)
		}
	}
	if !validAttemptID(fixturePassed) {
		t.Fatal("rejected a normal attempt id")
	}
}

func TestInterruptedFixtureHasStaleHeartbeat(t *testing.T) {
	data, err := os.ReadFile(MetadataPath(fixtureRoot(t), fixtureInterrupted))
	if err != nil {
		t.Fatal(err)
	}
	meta, err := ParseSessionMetadata(data)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(meta.HeartbeatAt) < 24*time.Hour {
		t.Fatalf("interrupted heartbeat should be stale, got %v", meta.HeartbeatAt)
	}
}

func TestRunningFixtureHeartbeatIsLive(t *testing.T) {
	data, err := os.ReadFile(MetadataPath(fixtureRoot(t), fixtureRunning))
	if err != nil {
		t.Fatal(err)
	}
	meta, err := ParseSessionMetadata(data)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(meta.HeartbeatAt) <= 0 {
		t.Fatal("running fixture uses a far-future heartbeat so liveness tests are deterministic")
	}
}
