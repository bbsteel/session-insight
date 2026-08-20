package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	return newStore(root, func(agent string) bool { return agent == "claude" })
}

// pngBytes renders a small valid PNG whose content depends on the seed color.
func pngBytes(t *testing.T, seed uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{seed, uint8(x), uint8(y), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func importPNG(t *testing.T, s *Store, agent, name string, seed uint8) *CaptureRecord {
	t.Helper()
	rec, err := s.Import(agent, name, bytes.NewReader(pngBytes(t, seed)), name+".png")
	if err != nil {
		t.Fatalf("Import(%s): %v", name, err)
	}
	return rec
}

func acceptCapture(t *testing.T, s *Store, agent, name string) {
	t.Helper()
	err := s.catalogs.update(agent, func(cat *AgentCatalog) error {
		st := cat.item(name)
		st.AcceptedHash = st.Current.Hash
		st.AcceptedExt = st.Current.Ext
		return nil
	})
	if err != nil {
		t.Fatalf("accept %s: %v", name, err)
	}
}

func statusOf(t *testing.T, s *Store, agent, name string) ItemStatus {
	t.Helper()
	cat, err := s.catalogs.load(agent)
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	return resolveStatus(cat.Items[name], false, s.blobExists(agent, currentCapture(cat.Items[name])))
}

func TestImportLifecycle(t *testing.T) {
	s := testStore(t)

	rec := importPNG(t, s, "claude", "04-thinking", 1)
	if got := statusOf(t, s, "claude", "04-thinking").Status; got != StatusCaptured {
		t.Fatalf("after first import status = %s, want captured", got)
	}
	if _, err := os.Stat(s.blobPath("claude", rec.Hash, rec.Ext)); err != nil {
		t.Fatalf("blob missing: %v", err)
	}

	// Same content again: unchanged, still captured.
	rec2 := importPNG(t, s, "claude", "04-thinking", 1)
	if rec2.Hash != rec.Hash {
		t.Fatalf("same content must keep the same hash")
	}
	if got := statusOf(t, s, "claude", "04-thinking").Status; got != StatusCaptured {
		t.Fatalf("re-import of same content changed status to %s", got)
	}

	acceptCapture(t, s, "claude", "04-thinking")
	if got := statusOf(t, s, "claude", "04-thinking").Status; got != StatusUsed {
		t.Fatalf("after accept status = %s, want used", got)
	}

	// New content for the same logical file: update_available, old accepted
	// content stays in the store and remains servable.
	importPNG(t, s, "claude", "04-thinking", 2)
	if got := statusOf(t, s, "claude", "04-thinking").Status; got != StatusUpdateAvailable {
		t.Fatalf("after update status = %s, want update_available", got)
	}
	if _, _, err := s.lookupBlob("claude", rec.Hash); err != nil {
		t.Fatalf("accepted blob must remain available after an update: %v", err)
	}
}

func TestImportRejectsMasquerade(t *testing.T) {
	s := testStore(t)
	importPNG(t, s, "claude", "04-thinking", 1)
	_, err := s.Import("claude", "04-thinking-toggled", bytes.NewReader(pngBytes(t, 1)), "copy.png")
	if err == nil || !strings.Contains(err.Error(), "identical image") {
		t.Fatalf("same image in a second slot must be rejected, got %v", err)
	}
}

func TestImportValidation(t *testing.T) {
	s := testStore(t)
	if _, err := s.Import("claude", "99-nope", bytes.NewReader(pngBytes(t, 1)), "x.png"); err == nil {
		t.Fatal("unknown logical name must be rejected")
	}
	if _, err := s.Import("nosuch", "04-thinking", bytes.NewReader(pngBytes(t, 1)), "x.png"); err == nil {
		t.Fatal("unknown agent must be rejected")
	}
	if _, err := s.Import("claude", "04-thinking", strings.NewReader("not an image"), "x.png"); err == nil {
		t.Fatal("non-image content must be rejected")
	}
	if _, err := s.Import("claude", "04-thinking", strings.NewReader(""), "x.png"); err == nil {
		t.Fatal("empty upload must be rejected")
	}
}

func TestScanDropIns(t *testing.T) {
	s := testStore(t)
	dir := s.agentDir("claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "04-thinking.png"), pngBytes(t, 3), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vacation.png"), pngBytes(t, 4), 0o600); err != nil {
		t.Fatal(err)
	}
	imported, skipped, err := s.ScanDropIns("claude")
	if err != nil {
		t.Fatal(err)
	}
	if len(imported) != 1 || imported[0] != "04-thinking" {
		t.Fatalf("imported = %v, want [04-thinking]", imported)
	}
	if len(skipped) != 1 || skipped[0] != "vacation.png" {
		t.Fatalf("skipped = %v, want [vacation.png]", skipped)
	}
	// The drop-in file moved into the content-addressed store; unknown files stay.
	if _, err := os.Stat(filepath.Join(dir, "04-thinking.png")); !os.IsNotExist(err) {
		t.Fatal("imported drop-in must be removed from the agent directory")
	}
	if _, err := os.Stat(filepath.Join(dir, "vacation.png")); err != nil {
		t.Fatal("unknown file must be left untouched")
	}
	if got := statusOf(t, s, "claude", "04-thinking").Status; got != StatusCaptured {
		t.Fatalf("status = %s, want captured", got)
	}
}

func TestLookupBlobGating(t *testing.T) {
	s := testStore(t)
	rec := importPNG(t, s, "claude", "04-thinking", 1)
	path, _, err := s.lookupBlob("claude", rec.Hash)
	if err != nil {
		t.Fatalf("known hash must resolve: %v", err)
	}
	if !strings.Contains(path, "blobs") {
		t.Fatalf("blob must come from the content-addressed dir: %s", path)
	}
	unknown := strings.Repeat("a", 64)
	if _, _, err := s.lookupBlob("claude", unknown); err == nil {
		t.Fatal("unknown hash must not be served")
	}
	if _, _, err := s.lookupBlob("claude", "../../etc/passwd"); err == nil {
		t.Fatal("path traversal must be rejected")
	}
}

func TestLocalUnavailableKeepsAcceptedValid(t *testing.T) {
	s := testStore(t)
	rec := importPNG(t, s, "claude", "04-thinking", 1)
	acceptCapture(t, s, "claude", "04-thinking")
	if err := os.Remove(s.blobPath("claude", rec.Hash, rec.Ext)); err != nil {
		t.Fatal(err)
	}
	st := statusOf(t, s, "claude", "04-thinking")
	if st.Status != StatusUsed || !st.LocalUnavailable {
		t.Fatalf("missing local blob: status = %+v, want used + local_unavailable", st)
	}
}
