package packops

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/bundle"
	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/reader/imported"
)

// fakeReader is a minimal live reader with one exportable session ("sess-1").
type fakeReader struct{}

func (fakeReader) AgentType() string   { return "claude" }
func (fakeReader) DisplayName() string { return "Claude" }
func (fakeReader) ListSessions() ([]model.Session, error) {
	return []model.Session{fixtureSession()}, nil
}
func (fakeReader) GetSession(id string) (*model.SessionDetail, error) {
	if id != "sess-1" {
		return nil, nil
	}
	return &model.SessionDetail{
		Session: fixtureSession(),
		Turns:   []model.TurnVM{{TurnIndex: 0, UserMessage: "hi", AssistantMessage: "hello"}},
	}, nil
}
func (fakeReader) RenderANSI(id string, cols int) (string, error) { return "", nil }
func (fakeReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	if id != "sess-1" {
		return nil, nil
	}
	return []model.RenderEvent{
		{Type: "UserPrompt", Text: "hi"},
		{Type: "TextChunk", Text: "hello"},
	}, nil
}

func fixtureSession() model.Session {
	return model.Session{
		ID:        "sess-1",
		AgentType: "claude",
		Name:      "Export me",
		CreatedAt: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC),
	}
}

func TestBuildExportSelections(t *testing.T) {
	res, err := BuildExport(
		[]reader.BaseSessionReader{fakeReader{}}, nil,
		[]Selection{
			{AgentType: "claude", ID: "sess-1"},
			{AgentType: "claude", ID: "missing"},
			{AgentType: "nosuchagent", ID: "x"},
		},
		ExportOptions{CaseLabel: "  case-7  ", SIVersion: "v0.0.0-test"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Payloads) != 1 {
		t.Fatalf("payloads = %d, want 1", len(res.Payloads))
	}
	if len(res.Skipped) != 2 {
		t.Fatalf("skipped = %+v, want 2 entries", res.Skipped)
	}
	if res.Skipped[0] != (Selection{AgentType: "claude", ID: "missing"}) ||
		res.Skipped[1] != (Selection{AgentType: "nosuchagent", ID: "x"}) {
		t.Errorf("skipped = %+v", res.Skipped)
	}

	m := res.Manifest
	if m.Format != bundle.Format || m.FormatVersion != bundle.FormatVersion {
		t.Errorf("manifest format = %q v%d", m.Format, m.FormatVersion)
	}
	if m.SIVersion != "v0.0.0-test" {
		t.Errorf("si_version = %q", m.SIVersion)
	}
	if m.CaseLabel != "case-7" {
		t.Errorf("case label not trimmed: %q", m.CaseLabel)
	}
	if m.CreatedAt.IsZero() || m.CreatedAt.Location() != time.UTC {
		t.Errorf("created_at = %v", m.CreatedAt)
	}
	if len(m.Sessions) != 1 || m.Sessions[0].File != "claude-sess-1.json" {
		t.Errorf("manifest sessions = %+v", m.Sessions)
	}
	if len(m.RelatedSessionIDs) != 1 || m.RelatedSessionIDs[0] != "sess-1" {
		t.Errorf("related ids = %v", m.RelatedSessionIDs)
	}
	if p := res.Payloads[0]; p.Detail == nil || len(p.RenderEvents) != 2 {
		t.Errorf("payload = %+v", p)
	}
}

func TestBuildExportRedact(t *testing.T) {
	readers := []reader.BaseSessionReader{fakeReader{}}
	sels := []Selection{{AgentType: "claude", ID: "sess-1"}}

	res, err := BuildExport(readers, nil, sels, ExportOptions{Redact: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Manifest.Options.Redacted {
		t.Error("manifest options.redacted not set")
	}
	if !res.Payloads[0].Entry.Redacted {
		t.Error("session entry not marked redacted")
	}
	// Redaction works on a deep copy: a fresh read must be unaffected.
	fresh, _ := fakeReader{}.GetSession("sess-1")
	if fresh.Turns[0].UserMessage != "hi" {
		t.Errorf("redaction leaked into reader state: %+v", fresh.Turns[0])
	}

	res, err = BuildExport(readers, nil, sels, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Manifest.Options.Redacted || res.Payloads[0].Entry.Redacted {
		t.Error("redact flag propagated without Redact option")
	}
}

func TestImportBundleRoundTrip(t *testing.T) {
	res, err := BuildExport(
		[]reader.BaseSessionReader{fakeReader{}}, nil,
		[]Selection{{AgentType: "claude", ID: "sess-1"}},
		ExportOptions{CaseLabel: "case-9", SIVersion: "v0.0.0-test"},
	)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := bundle.WriteBundle(&buf, res.Manifest, res.Payloads); err != nil {
		t.Fatal(err)
	}

	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	importRoot := filepath.Join(t.TempDir(), "imports")

	bundleID, manifest, err := ImportBundle(database, importRoot, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if bundleID == "" {
		t.Fatal("empty bundle id")
	}
	if manifest.CaseLabel != "case-9" || len(manifest.Sessions) != 1 {
		t.Errorf("manifest = %+v", manifest)
	}
	if _, err := os.Stat(filepath.Join(importRoot, bundleID, "manifest.json")); err != nil {
		t.Errorf("extracted manifest missing: %v", err)
	}

	summaries, err := database.ImportSummaries()
	if err != nil {
		t.Fatal(err)
	}
	importedID := imported.JoinSessionID(bundleID, "sess-1")
	rec, ok := summaries[imported.AgentType+"\x00"+importedID]
	if !ok {
		t.Fatalf("import record missing: %v", summaries)
	}
	if rec.BundleID != bundleID || rec.OriginalAgentType != "claude" ||
		rec.OriginalSessionID != "sess-1" || rec.CaseLabel != "case-9" {
		t.Errorf("import record = %+v", rec)
	}
}
