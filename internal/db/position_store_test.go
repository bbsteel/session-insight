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
