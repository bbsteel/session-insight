package grok

import (
	"testing"

	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
	"github.com/bbsteel/session-insight/internal/reader/readerr"
)

func TestGrokConformance(t *testing.T) {
	root := t.TempDir()
	sessionID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	writeSession(t, root, "%2Ftmp%2Fdemo", sessionID, summaryFile{}, sampleUpdatesClosed(), sampleEventsClosed())

	adaptertest.Run(t, adaptertest.Config{
		Capabilities: Capabilities(),
		NewReader: func(t *testing.T) adaptertest.Reader {
			return New(root)
		},
		Expect: adaptertest.Expectations{
			SessionCount: 1,
			SessionIDs:   []string{sessionID},
		},
	})
}

func TestGrokProvenanceComplete(t *testing.T) {
	root := t.TempDir()
	sessionID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	writeSession(t, root, "%2Ftmp%2Fdemo", sessionID, summaryFile{}, sampleUpdatesClosed(), sampleEventsClosed())
	detail, err := New(root).GetSession(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	adaptertest.AssertProvenanceComplete(t, detail, Capabilities())
}

func TestGrokProvenanceSourceMissing(t *testing.T) {
	root := t.TempDir()
	_, err := New(root).GetSession("no-such-session")
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

func TestGrokProvenanceMetadataOnly(t *testing.T) {
	// Summary present, empty updates/events → no replayable turns.
	root := t.TempDir()
	sessionID := "bbbbbbbb-bbbb-cccc-dddd-eeeeeeeeeeee"
	writeSession(t, root, "%2Ftmp%2Fdemo", sessionID, summaryFile{}, "", "")
	detail, err := New(root).GetSession(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	adaptertest.AssertProvenanceDegradedOrUnsupported(t, detail, Capabilities())
	if detail.Provenance.State != "metadata_only" {
		t.Fatalf("state=%s turns=%d", detail.Provenance.State, len(detail.Turns))
	}
	if len(detail.Provenance.Sources) == 0 {
		t.Fatal("expected source inventory")
	}
}
