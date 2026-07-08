//go:build sqlite_fts5

package db

import (
	"testing"
	"time"
)

// TestRealWorld_Timing reports real-world SearchTurns timing against the
// production database for quick diagnosis. Not a pass/fail test — log output
// is the deliverable.
func TestRealWorld_SearchTiming(t *testing.T) {
	db, err := Open("/home/user/.session-insight")
	if err != nil {
		t.Skipf("production db not available: %v", err)
	}
	defer db.Close()

	queries := []struct {
		name string
		q    string
	}{
		{"short CJK '折叠'", "折叠"},
		{"short ASCII 'go'", "go"},
		{"long CJK '性能优化'", "性能优化"},
		{"long ASCII 'performance'", "performance"},
	}

	for _, tc := range queries {
		start := time.Now()
		results, err := db.SearchTurns(tc.q, 30)
		elapsed := time.Since(start)
		if err != nil {
			t.Errorf("%s: error: %v", tc.name, err)
			continue
		}
		t.Logf("%s: %v, %d results", tc.name, elapsed, len(results))
	}
}
