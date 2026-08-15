//go:build sqlite_fts5

package db

import "testing"

func TestSavePositionCachePrunesStaleRevisionsAndWidths(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	positions := []PositionEntry{{PositionKey: "turn-0", Kind: "turn", TurnIndex: 0, LineStart: 0}}
	for _, cols := range []int{80, 100, 120, 140, 160} {
		if err := database.SavePositionCache("test", "s1", 100, cols, 1, positions); err != nil {
			t.Fatalf("SavePositionCache cols=%d: %v", cols, err)
		}
	}
	var count int
	if err := database.conn.QueryRow(`SELECT COUNT(*) FROM session_position_caches WHERE agent_type = 'test' AND session_id = 's1' AND revision = 100`).Scan(&count); err != nil {
		t.Fatalf("count widths: %v", err)
	}
	if count != maxPositionCacheWidths {
		t.Fatalf("width cache count=%d, want %d", count, maxPositionCacheWidths)
	}

	if err := database.SavePositionCache("test", "s1", 200, 90, 1, positions); err != nil {
		t.Fatalf("SavePositionCache new revision: %v", err)
	}
	if err := database.conn.QueryRow(`SELECT COUNT(*) FROM session_position_caches WHERE agent_type = 'test' AND session_id = 's1'`).Scan(&count); err != nil {
		t.Fatalf("count revisions: %v", err)
	}
	if count != 1 {
		t.Fatalf("cache count after revision change=%d, want 1", count)
	}
}

// TestOutlineKindRoundTrip proves the widened CHECK accepts 'outline'
// positions and that the read path returns them with payload and stable
// tie-breaker ordering intact.
func TestOutlineKindRoundTrip(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	end := 12
	positions := []PositionEntry{
		{PositionKey: "tool:0:9", Kind: "tool", TurnIndex: 0, LineStart: 9},
		{PositionKey: "outline:anomaly:tool_failed:0:c1:0", Kind: "outline", TurnIndex: 0, LineStart: 10,
			Label: "go test ./...", Severity: "error",
			Payload: map[string]any{"category": "anomaly", "code": "tool_failed", "precision": "exact"}},
		{PositionKey: "outline:file_change:file_modified:0:c2:0", Kind: "outline", TurnIndex: 0, LineStart: 10,
			LineEnd: &end, Label: "a.go",
			Payload: map[string]any{"category": "file_change", "code": "file_modified", "file_path": "a.go"}},
	}
	if err := database.SavePositionCache("test", "s1", 1, 100, 42, positions); err != nil {
		t.Fatalf("SavePositionCache with outline kind: %v", err)
	}
	cached, err := database.GetPositionCache("test", "s1", 1, 100)
	if err != nil || cached == nil {
		t.Fatalf("GetPositionCache: %v, nil=%v", err, cached == nil)
	}
	if cached.TotalLines != 42 {
		t.Fatalf("TotalLines = %d, want 42", cached.TotalLines)
	}
	if len(cached.Positions) != 3 {
		t.Fatalf("positions = %d, want 3", len(cached.Positions))
	}
	// Same line_start ordered by (line_end, position_key): the no-end anomaly
	// sorts before the end=12 file change.
	if cached.Positions[1].Payload["code"] != "tool_failed" || cached.Positions[2].Payload["code"] != "file_modified" {
		t.Fatalf("tie-breaker order wrong: %s then %s",
			cached.Positions[1].PositionKey, cached.Positions[2].PositionKey)
	}
	if cached.Positions[2].Payload["file_path"] != "a.go" {
		t.Fatalf("payload lost: %+v", cached.Positions[2].Payload)
	}
}
