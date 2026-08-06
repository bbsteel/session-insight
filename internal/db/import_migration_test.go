package db

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// makeV31DB creates a raw index.db pinned at schema version 31 without the
// import_records table, simulating a pre-migration database.
func makeV31DB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	conn, err := sql.Open("sqlite3", filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO schema_migrations(version) VALUES (31)`); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestV32FreshDBImportRecordsRoundTrip(t *testing.T) {
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	importedAt := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	rec := ImportRecord{
		AgentType:         "imported",
		SessionID:         "20260806-120000-abcdef--sess-1",
		BundleID:          "20260806-120000-abcdef",
		OriginHost:        "origin-box",
		OriginalAgentType: "claude",
		OriginalSessionID: "sess-1",
		CaseLabel:         "case-7",
		Redacted:          true,
		ImportedAt:        importedAt,
	}
	if err := d.UpsertImportRecord(rec); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	summaries, err := d.ImportSummaries()
	if err != nil {
		t.Fatalf("summaries: %v", err)
	}
	got, ok := summaries["imported\x00"+rec.SessionID]
	if !ok {
		t.Fatalf("record missing under composite key: %v", summaries)
	}
	if got.BundleID != rec.BundleID || got.OriginHost != "origin-box" || !got.Redacted || got.CaseLabel != "case-7" {
		t.Errorf("record fields not persisted: %+v", got)
	}
	if !got.ImportedAt.Equal(importedAt) {
		t.Errorf("imported_at = %v, want %v", got.ImportedAt, importedAt)
	}

	bundles, err := d.ListImportBundles()
	if err != nil {
		t.Fatalf("bundles: %v", err)
	}
	if len(bundles) != 1 || bundles[0].BundleID != rec.BundleID || bundles[0].SessionCount != 1 {
		t.Fatalf("bundle summary = %+v", bundles)
	}
	if bundles[0].OriginHost != "origin-box" || bundles[0].CaseLabel != "case-7" {
		t.Errorf("bundle summary metadata = %+v", bundles[0])
	}

	ids, err := d.DeleteImportRecordsByBundle(rec.BundleID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(ids) != 1 || ids[0] != rec.SessionID {
		t.Fatalf("deleted ids = %v", ids)
	}
	summaries, err = d.ImportSummaries()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 0 {
		t.Errorf("records remain after bundle delete: %v", summaries)
	}
}

func TestV32UpgradeFromV31(t *testing.T) {
	dir := makeV31DB(t)
	d, err := Open(dir)
	if err != nil {
		t.Fatalf("open/migrate v31 db: %v", err)
	}
	defer d.Close()

	// Table exists and is usable after migration from 31.
	if err := d.UpsertImportRecord(ImportRecord{AgentType: "imported", SessionID: "s", BundleID: "b"}); err != nil {
		t.Fatalf("upsert after migration: %v", err)
	}
	if bundles, err := d.ListImportBundles(); err != nil || len(bundles) != 1 {
		t.Fatalf("bundles after migration: %v %v", bundles, err)
	}
}

// TestV32SelfHealsWithoutVersionRow mirrors the v30 contract: a stray version
// row cannot prove the physical table exists, so the migration must recreate
// a dropped table even when schema_migrations already says 32.
func TestV32SelfHealsWithoutVersionRow(t *testing.T) {
	dir := makeV31DB(t)
	conn, err := sql.Open("sqlite3", filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO schema_migrations(version) VALUES (32)`); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	d, err := Open(dir)
	if err != nil {
		t.Fatalf("open db with version row but no table: %v", err)
	}
	defer d.Close()
	if err := d.UpsertImportRecord(ImportRecord{AgentType: "imported", SessionID: "s", BundleID: "b"}); err != nil {
		t.Fatalf("self-healed table rejects upsert: %v", err)
	}
}

// TestV32DeleteSessionDataClearsImportRecord ensures per-session deletion
// also removes the import provenance row.
func TestV32DeleteSessionDataClearsImportRecord(t *testing.T) {
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	rec := ImportRecord{AgentType: "imported", SessionID: "b--s", BundleID: "b"}
	if err := d.UpsertImportRecord(rec); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteSessionData("imported", "b--s"); err != nil {
		t.Fatalf("DeleteSessionData: %v", err)
	}
	summaries, err := d.ImportSummaries()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 0 {
		t.Errorf("import record survived DeleteSessionData: %v", summaries)
	}
}
