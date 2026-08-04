package grok

import (
	"testing"

	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
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
	// Typed SessionReadError — verified by non-nil error (kind checked in unit tests)
}
