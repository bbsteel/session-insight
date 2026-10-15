package pi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
	"github.com/bbsteel/session-insight/internal/reader/readerr"
)

const basicID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

func TestPiConformance(t *testing.T) {
	root := installTestdata(t, basicID, "basic.jsonl")
	adaptertest.Run(t, adaptertest.Config{
		Capabilities: Capabilities(),
		NewReader: func(t *testing.T) adaptertest.Reader {
			return New(root)
		},
		Expect: adaptertest.Expectations{
			SessionCount: 1,
			SessionIDs:   []string{basicID},
		},
	})
}

func TestPiProvenanceComplete(t *testing.T) {
	root := installTestdata(t, basicID, "basic.jsonl")
	detail, err := New(root).GetSession(basicID)
	if err != nil {
		t.Fatal(err)
	}
	adaptertest.AssertProvenanceComplete(t, detail, Capabilities())
}

func TestPiProvenanceSourceMissing(t *testing.T) {
	_, err := New(t.TempDir()).GetSession("no-such-session")
	if err == nil {
		t.Fatal("expected error")
	}
	sre, ok := readerr.As(err)
	if !ok {
		t.Fatalf("expected typed readerr, got %T %v", err, err)
	}
	if sre.Kind != readerr.SourceMissing {
		t.Fatalf("kind=%s", sre.Kind)
	}
}

func TestPiProvenanceMetadataOnly(t *testing.T) {
	id := "bbbbbbbb-bbbb-cccc-dddd-eeeeeeeeeeee"
	root := installTestdata(t, id, "header-only.jsonl")
	detail, err := New(root).GetSession(id)
	if err != nil {
		t.Fatal(err)
	}
	adaptertest.AssertProvenanceDegradedOrUnsupported(t, detail, Capabilities())
	if detail.Provenance.State != "metadata_only" {
		t.Fatalf("state=%s turns=%d", detail.Provenance.State, len(detail.Turns))
	}
	if len(detail.Turns) != 0 {
		t.Fatalf("metadata_only must have zero turns, got %d", len(detail.Turns))
	}
}

func TestPiProvenanceDegradedMalformedLine(t *testing.T) {
	root := t.TempDir()
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	body, err := os.ReadFile(filepath.Join("testdata", "basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	writeSessionFile(t, root, id, "not-json\n"+string(body))
	detail, err := New(root).GetSession(id)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Provenance.State != "degraded" || len(detail.Provenance.Warnings) != 1 || detail.Provenance.Warnings[0].Code != "malformed_record_skipped" {
		t.Fatalf("provenance=%+v", detail.Provenance)
	}
}

func TestPiFirstMissingRetainsDiscoveredSourcePath(t *testing.T) {
	root := installTestdata(t, basicID, "basic.jsonl")
	r := New(root)
	sessions, complete, err := r.ListSessionsDetailed()
	if err != nil || !complete || len(sessions) != 1 {
		t.Fatalf("list complete=%v sessions=%d err=%v", complete, len(sessions), err)
	}
	knownPath := r.knownSessionFile(basicID)
	if knownPath == "" {
		t.Fatal("missing discovered path")
	}
	if err := os.Remove(knownPath); err != nil {
		t.Fatal(err)
	}
	_, err = r.GetSession(basicID)
	if err == nil {
		t.Fatal("expected source missing error")
	}
	sre, ok := readerr.As(err)
	if !ok || len(sre.Sources) != 1 || sre.Sources[0].Path != knownPath || sre.Sources[0].State != "missing" {
		t.Fatalf("read error facts=%+v", sre)
	}
}

func installTestdata(t *testing.T, id, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeSessionFile(t, root, id, string(body))
	return root
}

func writeSessionFile(t *testing.T, root, id, body string) string {
	t.Helper()
	dir := filepath.Join(root, "--tmp-demo--")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "2026-01-01T00-00-00-000Z_"+id+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
