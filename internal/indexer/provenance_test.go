//go:build sqlite_fts5

package indexer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
)

type fakeReader struct {
	agent    string
	sessions []model.Session
	listErr  error
	// incomplete when true reports Complete=false (no omission tombstones).
	incomplete bool
	details    map[string]*model.SessionDetail
	getErr     map[string]error
}

func (f *fakeReader) AgentType() string   { return f.agent }
func (f *fakeReader) DisplayName() string { return f.agent }
func (f *fakeReader) ListSessions() ([]model.Session, error) {
	sessions, _, err := f.ListSessionsDetailed()
	return sessions, err
}
func (f *fakeReader) ListSessionsDetailed() ([]model.Session, bool, error) {
	if f.listErr != nil {
		return nil, false, f.listErr
	}
	return f.sessions, !f.incomplete, nil
}
func (f *fakeReader) GetSession(id string) (*model.SessionDetail, error) {
	if err, ok := f.getErr[id]; ok {
		return nil, err
	}
	if d, ok := f.details[id]; ok {
		return d, nil
	}
	return nil, errors.New("not found")
}
func (f *fakeReader) RenderANSI(id string, cols int) (string, error) {
	return "", errors.New("unsupported")
}
func (f *fakeReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	return nil, nil
}

func openIndexerDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestIndexerTombstoneOnSuccessfulDiscovery(t *testing.T) {
	database := openIndexerDB(t)
	now := time.Now().UTC()
	sess := model.Session{ID: "s1", AgentType: "fake", Name: "n", CreatedAt: now, UpdatedAt: now, TurnCount: 1}
	prov := model.SessionProvenance{
		State: model.RecordComplete, CapturedAt: now, AdapterRevision: 1,
		Sources: []model.SessionSourceFile{{Role: "primary_transcript", Path: "/x", State: model.SourcePresent}},
	}
	detail := &model.SessionDetail{
		Session:    sess,
		Turns:      []model.TurnVM{{TurnIndex: 0, UserMessage: "hi", AssistantMessage: "yo"}},
		Provenance: &prov,
	}
	fr := &fakeReader{
		agent:    "fake",
		sessions: []model.Session{sess},
		details:  map[string]*model.SessionDetail{"s1": detail},
		getErr:   map[string]error{},
	}
	ix := New(database, []reader.BaseSessionReader{fr})
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Second cycle: session gone from discovery → tombstone, not hard delete.
	fr.sessions = nil
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, ok, err := database.GetProvenance("fake", "s1")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.State != model.RecordSourceMissing {
		t.Fatalf("state=%s", got.State)
	}
	row, exists, err := database.GetSessionRow("fake", "s1")
	if err != nil || !exists {
		t.Fatal("session meta must remain")
	}
	if row.ID != "s1" {
		t.Fatalf("row=%+v", row)
	}
}

func TestIndexerScanFailureDoesNotMassMissing(t *testing.T) {
	database := openIndexerDB(t)
	now := time.Now().UTC()
	sess := model.Session{ID: "s1", AgentType: "fake", CreatedAt: now, UpdatedAt: now}
	prov := model.SessionProvenance{State: model.RecordComplete, CapturedAt: now, AdapterRevision: 1}
	detail := &model.SessionDetail{Session: sess, Turns: []model.TurnVM{{TurnIndex: 0, UserMessage: "x"}}, Provenance: &prov}
	fr := &fakeReader{
		agent: "fake", sessions: []model.Session{sess},
		details: map[string]*model.SessionDetail{"s1": detail},
	}
	ix := New(database, []reader.BaseSessionReader{fr})
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Discovery fails — keep previous complete state.
	fr.listErr = errors.New("permission denied")
	err := ix.RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected cycle error from list failure")
	}
	got, ok, _ := database.GetProvenance("fake", "s1")
	if !ok || got.State != model.RecordComplete {
		t.Fatalf("scan failure must not mark missing: ok=%v state=%v", ok, got)
	}
}

func TestHandleReadFailureUsesCatalogAdapterRevision(t *testing.T) {
	// claude catalog declares AdapterRevision >= 2 after provenance wiring.
	database := openIndexerDB(t)
	now := time.Now().UTC()
	sess := model.Session{ID: "s-fail", AgentType: "claude", CreatedAt: now, UpdatedAt: now}
	fr := &fakeReader{
		agent:    "claude",
		sessions: []model.Session{sess},
		getErr: map[string]error{
			"s-fail": reader.NewSessionReadError(reader.ReadFormatUnsupported, model.WarnUnsupportedSchema,
				errors.New("schema too new")),
		},
	}
	// Attach sources on the typed error so Build inventory is non-empty.
	if sre, ok := reader.AsSessionReadError(fr.getErr["s-fail"]); ok {
		sre.Sources = []model.SessionSourceFile{{
			Role: model.SourceRolePrimaryTranscript, Path: "/tmp/x.jsonl", State: model.SourceUnsupported,
		}}
		sre.Warnings = []model.ParseWarning{{
			Code: model.WarnUnsupportedSchema, Severity: model.WarningSeverityError,
			AffectsCompleteness: true, Impacts: []string{model.ImpactReplay}, Count: 1,
		}}
	}
	ix := New(database, []reader.BaseSessionReader{fr})
	if err := ix.RunOnce(context.Background()); err != nil {
		// completed_with_errors is fine; we care about persisted revision
		t.Logf("cycle err (expected ok): %v", err)
	}
	got, ok, err := database.GetProvenance("claude", "s-fail")
	if err != nil || !ok {
		t.Fatalf("provenance: ok=%v err=%v", ok, err)
	}
	if got.State != model.RecordParserUnsupported {
		t.Fatalf("state=%s", got.State)
	}
	def, defOK := reader.AgentDefinition("claude")
	if !defOK {
		t.Fatal("claude catalog missing")
	}
	if got.AdapterRevision != def.AdapterRevision {
		t.Fatalf("adapter_revision=%d want catalog %d", got.AdapterRevision, def.AdapterRevision)
	}
	if got.WarningSummary.Total < 1 {
		t.Fatalf("warning_summary not aggregated: %+v", got.WarningSummary)
	}
}

func TestIndexerCancelDoesNotTombstone(t *testing.T) {
	database := openIndexerDB(t)
	now := time.Now().UTC()
	sess := model.Session{ID: "s1", AgentType: "fake", CreatedAt: now, UpdatedAt: now}
	prov := model.SessionProvenance{State: model.RecordComplete, CapturedAt: now, AdapterRevision: 1}
	detail := &model.SessionDetail{Session: sess, Turns: []model.TurnVM{{TurnIndex: 0, UserMessage: "x"}}, Provenance: &prov}
	fr := &fakeReader{
		agent: "fake", sessions: []model.Session{sess},
		details: map[string]*model.SessionDetail{"s1": detail},
	}
	ix := New(database, []reader.BaseSessionReader{fr})
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Cancelled context before indexing: indexOnce returns early without tombstone of others.
	// With empty agent list after cancel at start of list loop:
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = ix.RunOnce(ctx)
	got, ok, _ := database.GetProvenance("fake", "s1")
	if !ok || got.State != model.RecordComplete {
		t.Fatalf("cancel must not mark missing: %+v", got)
	}
}
