package db

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestProvenanceMigrationAndUnavailable(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()
	if err := db.UpsertSessionMeta("claude", "s1", "/cwd", "", "", "p", "n", "", "", 1, 1, now, now); err != nil {
		t.Fatal(err)
	}
	p, ok, err := db.GetProvenance("claude", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if ok || p != nil {
		t.Fatal("legacy session must report provenance unavailable, not complete")
	}
}

func TestReplaceSessionSnapshotAtomic(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()
	mt := now.Add(-time.Minute)
	sz := int64(42)
	prov := model.SessionProvenance{
		State:           model.RecordComplete,
		CapturedAt:      now,
		AdapterRevision: 2,
		Sources: []model.SessionSourceFile{{
			Role: model.SourceRolePrimaryTranscript, Path: "/tmp/a.jsonl",
			State: model.SourcePresent, UpdatedAt: &mt, SizeBytes: &sz,
		}},
		Warnings:       []model.ParseWarning{},
		WarningSummary: model.WarningSummary{},
	}
	sess := model.Session{
		ID: "s1", AgentType: "claude", Name: "n", CWD: "/c",
		CreatedAt: now, UpdatedAt: now, TurnCount: 1, MessageCount: 2,
	}
	if err := db.ReplaceSessionSnapshot(SessionSnapshotWrite{
		AgentType: "claude", Session: sess,
		TurnCount: 1, MessageCount: 2,
		Turns:      []TurnText{{TurnIndex: 0, Role: "user", Content: "hello"}},
		Provenance: &prov, Revision: now.UnixNano(),
	}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.GetProvenance("claude", "s1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.State != model.RecordComplete || got.AdapterRevision != 2 {
		t.Fatalf("got %+v", got)
	}
	if got.LastSuccessfulAt == nil {
		t.Fatal("expected last_successful_at on complete write")
	}
	if got.MissingSince != nil {
		t.Fatal("missing_since must be cleared on restore")
	}
	// turns + watermark present
	rev, exists, err := db.GetWatermark("claude", "s1")
	if err != nil || !exists || rev == 0 {
		t.Fatalf("watermark: exists=%v rev=%d err=%v", exists, rev, err)
	}
}

func TestSourceMissingTombstoneChainAndRestore(t *testing.T) {
	db := openTestDB(t)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	prov := model.SessionProvenance{
		State: model.RecordComplete, CapturedAt: now, AdapterRevision: 1,
		Sources: []model.SessionSourceFile{{
			Role: model.SourceRolePrimaryTranscript, Path: "/p.jsonl", State: model.SourcePresent,
		}},
	}
	sess := model.Session{ID: "s1", AgentType: "claude", Name: "n", CreatedAt: now, UpdatedAt: now}
	if err := db.ReplaceSessionSnapshot(SessionSnapshotWrite{
		AgentType: "claude", Session: sess, TurnCount: 1, MessageCount: 1,
		Turns:      []TurnText{{TurnIndex: 0, Role: "user", Content: "keep-me"}},
		Provenance: &prov, Revision: 1,
	}); err != nil {
		t.Fatal(err)
	}

	// complete → source_missing
	n, err := db.MarkSessionsSourceMissing("claude", []string{"s1"}, now.Add(time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("tombstone n=%d err=%v", n, err)
	}
	got, ok, err := db.GetProvenance("claude", "s1")
	if err != nil || !ok {
		t.Fatal(err)
	}
	if got.State != model.RecordSourceMissing || got.MissingSince == nil {
		t.Fatalf("tombstone: %+v", got)
	}
	// FTS/turn content retained
	results, err := db.SearchTurns("keep-me", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("tombstone must retain FTS content")
	}

	// re-mark keeps original missing_since
	firstMissing := *got.MissingSince
	if _, err := db.MarkSessionsSourceMissing("claude", []string{"s1"}, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got2, _, _ := db.GetProvenance("claude", "s1")
	if got2.MissingSince == nil || !got2.MissingSince.Equal(firstMissing) {
		t.Fatalf("missing_since changed: %v vs %v", got2.MissingSince, firstMissing)
	}

	// restore → complete clears missing_since
	prov2 := prov
	prov2.CapturedAt = now.Add(3 * time.Hour)
	if err := db.ReplaceSessionSnapshot(SessionSnapshotWrite{
		AgentType: "claude", Session: sess, TurnCount: 1, MessageCount: 1,
		Turns:      []TurnText{{TurnIndex: 0, Role: "user", Content: "keep-me"}},
		Provenance: &prov2, Revision: 2,
	}); err != nil {
		t.Fatal(err)
	}
	restored, _, _ := db.GetProvenance("claude", "s1")
	if restored.State != model.RecordComplete || restored.MissingSince != nil {
		t.Fatalf("restore: %+v", restored)
	}
}

func TestExplicitDeleteRemovesProvenanceNoTombstone(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()
	sess := model.Session{ID: "s1", AgentType: "claude", CreatedAt: now, UpdatedAt: now}
	prov := model.SessionProvenance{State: model.RecordComplete, CapturedAt: now, AdapterRevision: 1}
	_ = db.ReplaceSessionSnapshot(SessionSnapshotWrite{
		AgentType: "claude", Session: sess, Turns: nil, Provenance: &prov, Revision: 1,
	})
	if err := db.DeleteSessionData("claude", "s1"); err != nil {
		t.Fatal(err)
	}
	_, ok, err := db.GetProvenance("claude", "s1")
	if err != nil || ok {
		t.Fatalf("expected full delete, ok=%v err=%v", ok, err)
	}
	_, sessOK, err := db.GetSessionRow("claude", "s1")
	if err != nil || sessOK {
		t.Fatalf("session row should be gone")
	}
}

func TestCorruptProvenanceJSONSafe(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()
	if err := db.UpsertSessionMeta("claude", "s1", "", "", "", "", "", "", "", 0, 0, now, now); err != nil {
		t.Fatal(err)
	}
	_, err := db.conn.Exec(`
		INSERT INTO session_provenance(
			agent_type, session_id, state, reason_code, captured_at, adapter_revision,
			sources_json, warnings_json, warning_summary_json, revision
		) VALUES ('claude','s1','complete','',?,1,'NOT_JSON','{{','nope',0)`,
		model.FormatTime(now),
	)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.GetProvenance("claude", "s1")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.State != model.RecordComplete {
		t.Fatalf("state=%s", got.State)
	}
	// Empty slices on corrupt JSON — no panic.
	if got.Sources == nil || got.Warnings == nil {
		t.Fatal("expected non-nil empty slices")
	}
	// Compact list status also safe
	m, err := db.ListProvenanceStatusByAgent("claude")
	if err != nil {
		t.Fatal(err)
	}
	if m["claude\x00s1"] == nil {
		t.Fatal("missing compact status")
	}
}

func TestListProvenanceStatusNoPaths(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()
	prov := model.SessionProvenance{
		State: model.RecordDegraded, CapturedAt: now, AdapterRevision: 1,
		WarningSummary: model.WarningSummary{Total: 3},
		Sources: []model.SessionSourceFile{{
			Role: "primary_transcript", Path: "/secret/path", State: model.SourcePresent,
		}},
		Warnings: []model.ParseWarning{{
			Code: "malformed_record_skipped", Severity: "warning", Count: 3, AffectsCompleteness: true,
		}},
	}
	sess := model.Session{ID: "s1", AgentType: "claude", CreatedAt: now, UpdatedAt: now}
	_ = db.ReplaceSessionSnapshot(SessionSnapshotWrite{
		AgentType: "claude", Session: sess, Provenance: &prov, Revision: 1,
	})
	m, err := db.ListProvenanceStatusByAgent("claude")
	if err != nil {
		t.Fatal(err)
	}
	st := m["claude\x00s1"]
	if st == nil || st.State != model.RecordDegraded || st.WarningCount != 3 {
		t.Fatalf("compact: %+v", st)
	}
	// Ensure the compact type itself has no path when marshaled.
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if containsSubstring(string(b), "/secret") {
		t.Fatalf("compact status leaked path: %s", b)
	}
}

func containsSubstring(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub)))
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
