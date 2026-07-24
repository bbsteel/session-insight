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
