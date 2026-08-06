package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/bundle"
	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/indexer"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/reader/imported"
)

// exportSessionReader is a minimal live reader with one exportable session.
type exportSessionReader struct{}

func (exportSessionReader) AgentType() string   { return "claude" }
func (exportSessionReader) DisplayName() string { return "Claude" }
func (exportSessionReader) ListSessions() ([]model.Session, error) {
	return []model.Session{exportFixtureSession()}, nil
}
func (exportSessionReader) GetSession(id string) (*model.SessionDetail, error) {
	if id != "sess-1" {
		return nil, nil
	}
	return &model.SessionDetail{
		Session: exportFixtureSession(),
		Turns:   []model.TurnVM{{TurnIndex: 0, UserMessage: "hi", AssistantMessage: "hello"}},
	}, nil
}
func (exportSessionReader) RenderANSI(id string, cols int) (string, error) { return "", nil }
func (exportSessionReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	if id != "sess-1" {
		return nil, nil
	}
	return []model.RenderEvent{
		{Type: "UserPrompt", Text: "hi"},
		{Type: "TextChunk", Text: "hello"},
	}, nil
}

func exportFixtureSession() model.Session {
	return model.Session{
		ID:        "sess-1",
		AgentType: "claude",
		Name:      "Export me",
		CreatedAt: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC),
	}
}

// exportBundleFrom drives server A's export endpoint and returns the bundle bytes.
func exportBundleFrom(t *testing.T, srv *Server, body string) []byte {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/sessions/export-bundle", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("export status = %d, body %s", w.Code, w.Body.String())
	}
	if cd := w.Header().Get("Content-Disposition"); cd == "" {
		t.Error("export response missing Content-Disposition")
	}
	return w.Body.Bytes()
}

// importBundleInto uploads bundle bytes to server B's import endpoint.
func importBundleInto(t *testing.T, srv *Server, bundleBytes []byte) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "bundle.sibundle")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(bundleBytes); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/api/sessions/import-bundle", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	resp := map[string]any{}
	if w.Code == http.StatusOK {
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode import response: %v", err)
		}
	}
	return w.Code, resp
}

func TestExportImportRoundTrip(t *testing.T) {
	// Server A: owns the live session.
	dbA, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dbA.Close()
	srvA := New(dbA, []reader.BaseSessionReader{exportSessionReader{}})

	bundleBytes := exportBundleFrom(t, srvA,
		`{"sessions":[{"agent_type":"claude","id":"sess-1"}],"case_label":"case-7"}`)

	// Server B: imports the bundle.
	dbB, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dbB.Close()
	rootB := filepath.Join(t.TempDir(), "imports")
	importedReader := imported.New(rootB)
	srvB := New(dbB, []reader.BaseSessionReader{importedReader})
	srvB.SetImportRoot(rootB)
	kicked := ""
	srvB.SetIndexKicker(func(agentType string) { kicked = agentType })

	code, resp := importBundleInto(t, srvB, bundleBytes)
	if code != http.StatusOK {
		t.Fatalf("import status = %d", code)
	}
	bundleID, _ := resp["bundle_id"].(string)
	if bundleID == "" {
		t.Fatalf("import response missing bundle_id: %v", resp)
	}
	if resp["case_label"] != "case-7" {
		t.Errorf("case_label = %v", resp["case_label"])
	}
	if resp["imported"].(float64) != 1 {
		t.Errorf("imported = %v", resp["imported"])
	}
	if kicked != imported.AgentType {
		t.Errorf("kickIndex called with %q, want %q", kicked, imported.AgentType)
	}
	importedID := imported.JoinSessionID(bundleID, "sess-1")

	// Index the imported reader into B's DB.
	ix := indexer.New(dbB, []reader.BaseSessionReader{imported.New(rootB)})
	if err := ix.RunOnce(context.Background()); err != nil {
		t.Fatalf("indexer RunOnce: %v", err)
	}

	rows, err := dbB.ListRootSessionSummaries(imported.AgentType)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != importedID || rows[0].Name != "Export me" {
		t.Fatalf("indexed rows = %+v", rows)
	}
	if rows[0].IsLive {
		t.Error("imported session must not be live")
	}

	summaries, err := dbB.ImportSummaries()
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := summaries[imported.AgentType+"\x00"+importedID]
	if !ok {
		t.Fatalf("import record missing: %v", summaries)
	}
	if rec.OriginalAgentType != "claude" || rec.OriginalSessionID != "sess-1" || rec.CaseLabel != "case-7" {
		t.Errorf("import record = %+v", rec)
	}

	// GET detail exposes import_info.
	req := httptest.NewRequest("GET", "/api/sessions/"+importedID, nil)
	w := httptest.NewRecorder()
	srvB.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get imported session status = %d, body %s", w.Code, w.Body.String())
	}
	var detail struct {
		ID         string            `json:"id"`
		AgentType  string            `json:"agent_type"`
		IsLive     bool              `json:"is_live"`
		ImportInfo *model.ImportInfo `json:"import_info"`
	}
	if err := json.NewDecoder(w.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.AgentType != imported.AgentType || detail.ID != importedID {
		t.Errorf("detail identity = %s/%s", detail.AgentType, detail.ID)
	}
	if detail.IsLive {
		t.Error("imported detail must not be live")
	}
	if detail.ImportInfo == nil {
		t.Fatal("detail missing import_info")
	}
	if detail.ImportInfo.BundleID != bundleID || detail.ImportInfo.OriginalSessionID != "sess-1" ||
		detail.ImportInfo.CaseLabel != "case-7" {
		t.Errorf("import_info = %+v", detail.ImportInfo)
	}

	// List endpoint carries the import markers.
	req = httptest.NewRequest("GET", "/api/sessions", nil)
	w = httptest.NewRecorder()
	srvB.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}
	var list []SessionSummary
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !list[0].Imported || list[0].OriginalAgentType != "claude" || list[0].CaseLabel != "case-7" {
		t.Fatalf("list summary = %+v", list)
	}

	// GET /api/imports aggregates the bundle.
	req = httptest.NewRequest("GET", "/api/imports", nil)
	w = httptest.NewRecorder()
	srvB.Mux.ServeHTTP(w, req)
	var bundles []db.BundleSummary
	if err := json.NewDecoder(w.Body).Decode(&bundles); err != nil {
		t.Fatal(err)
	}
	if len(bundles) != 1 || bundles[0].BundleID != bundleID || bundles[0].SessionCount != 1 {
		t.Fatalf("bundles = %+v", bundles)
	}

	// DELETE removes records, index rows, and the on-disk bundle.
	req = httptest.NewRequest("DELETE", "/api/imports/"+bundleID, nil)
	w = httptest.NewRecorder()
	srvB.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(rootB, bundleID)); !os.IsNotExist(err) {
		t.Error("bundle dir still present after delete")
	}
	if summaries, _ := dbB.ImportSummaries(); len(summaries) != 0 {
		t.Errorf("import records remain: %v", summaries)
	}
	if rows, _ := dbB.ListRootSessionSummaries(imported.AgentType); len(rows) != 0 {
		t.Errorf("index rows remain: %v", rows)
	}
}

func TestImportRejectsNewerFormatVersion(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	root := filepath.Join(t.TempDir(), "imports")
	srv := New(database, []reader.BaseSessionReader{imported.New(root)})
	srv.SetImportRoot(root)

	// Hand-craft a bundle with a future format version.
	m := bundle.Manifest{
		Format:        bundle.Format,
		FormatVersion: bundle.FormatVersion + 1,
		CreatedAt:     time.Now().UTC(),
		Sessions:      []bundle.SessionEntry{{AgentType: "claude", ID: "s", File: "s.json"}},
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tarAdd := func(name string, body []byte) {
		t.Helper()
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	manifestJSON, _ := json.Marshal(m)
	tarAdd("manifest.json", manifestJSON)
	tarAdd("sessions/s.json", []byte(`{"detail":{},"render_events":[]}`))
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	code, _ := importBundleInto(t, srv, buf.Bytes())
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}

func TestDeleteImportBundleRejectsTraversal(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	root := filepath.Join(t.TempDir(), "imports")
	srv := New(database, nil)
	srv.SetImportRoot(root)

	// Encoded separators reach the handler and must be rejected outright.
	for _, id := range []string{"a%2Fb", "a%5Cb", "..%2F..", "%2Fetc"} {
		req := httptest.NewRequest("DELETE", "/api/imports/"+id, nil)
		w := httptest.NewRecorder()
		srv.Mux.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("DELETE /api/imports/%s: status = %d, want 400", id, w.Code)
		}
	}
	// Bare "." / ".." are cleaned by the mux into a redirect away from the
	// handler — also safe, assert they never succeed.
	for _, id := range []string{"..", "."} {
		req := httptest.NewRequest("DELETE", "/api/imports/"+id, nil)
		w := httptest.NewRecorder()
		srv.Mux.ServeHTTP(w, req)
		if w.Code == http.StatusOK {
			t.Errorf("DELETE /api/imports/%s: unexpectedly succeeded", id)
		}
	}
}
