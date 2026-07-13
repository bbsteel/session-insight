//go:build sqlite_fts5

package db

import (
	"strings"
	"testing"
)

func TestTurnTexts_InsertDeleteSync(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer database.Close()

	turns := []TurnText{
		{TurnIndex: 0, Role: "user", Content: "hello world search test"},
	}
	if err := database.UpsertTurns("test", "sync-session", turns, 100); err != nil {
		t.Fatalf("UpsertTurns: %v", err)
	}

	results, err := database.SearchTurns("search", 10)
	if err != nil {
		t.Fatalf("SearchTurns: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 search result, got %d", len(results))
	}
	if results[0].SessionID != "sync-session" {
		t.Fatalf("expected session 'sync-session', got %q", results[0].SessionID)
	}

	// Delete via empty list
	if err := database.UpsertTurns("test", "sync-session", nil, 200); err != nil {
		t.Fatalf("UpsertTurns delete: %v", err)
	}

	results, err = database.SearchTurns("search", 10)
	if err != nil {
		t.Fatalf("SearchTurns after delete: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results after delete, got %d", len(results))
	}
}

func TestTurnTexts_RebuildConsistency(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer database.Close()

	turns := []TurnText{
		{TurnIndex: 0, Role: "user", Content: "first turn content"},
		{TurnIndex: 1, Role: "assistant", Content: "second turn reply"},
		{TurnIndex: 2, Role: "user", Content: "third turn query"},
	}
	if err := database.UpsertTurns("test", "rebuild-session", turns, 100); err != nil {
		t.Fatalf("UpsertTurns: %v", err)
	}

	if err := database.RebuildFTS(); err != nil {
		t.Fatalf("RebuildFTS: %v", err)
	}

	var ttCount int
	if err := database.conn.QueryRow(
		`SELECT COUNT(*) FROM turn_texts`,
	).Scan(&ttCount); err != nil {
		t.Fatalf("count turn_texts: %v", err)
	}

	var ftsCount int
	if err := database.conn.QueryRow(
		`SELECT COUNT(*) FROM turn_texts_fts`,
	).Scan(&ftsCount); err != nil {
		t.Fatalf("count turn_texts_fts: %v", err)
	}

	if ttCount != ftsCount {
		t.Fatalf("count mismatch: turn_texts=%d turn_texts_fts=%d", ttCount, ftsCount)
	}
}

func TestMigrate_FTS5Available(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatalf("first Open() failed: %v", err)
	}

	// Second Open on same dir must be idempotent.
	database.Close()
	database, err = Open(dir)
	if err != nil {
		t.Fatalf("second Open() failed: %v", err)
	}
	defer database.Close()

	var name string
	err = database.conn.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='turn_texts_fts'`,
	).Scan(&name)
	if err != nil {
		t.Fatalf("turn_texts_fts not found: %v", err)
	}
	if name != "turn_texts_fts" {
		t.Fatalf("expected 'turn_texts_fts', got %q", name)
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatalf("first Open() failed: %v", err)
	}
	database.Close()

	database, err = Open(dir)
	if err != nil {
		t.Fatalf("second Open() failed: %v", err)
	}
	defer database.Close()
}

func TestUpsertTurns_EmptyListCleansUp(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer database.Close()

	turns := []TurnText{
		{TurnIndex: 0, Role: "user", Content: "some content"},
	}
	if err := database.UpsertTurns("test", "cleanup-session", turns, 100); err != nil {
		t.Fatalf("UpsertTurns insert: %v", err)
	}

	// Verify content exists
	var count int
	if err := database.conn.QueryRow(
		`SELECT COUNT(*) FROM turn_texts WHERE agent_type = 'test' AND session_id = 'cleanup-session'`,
	).Scan(&count); err != nil {
		t.Fatalf("count after insert: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row after insert, got %d", count)
	}

	// Empty list upsert
	if err := database.UpsertTurns("test", "cleanup-session", nil, 200); err != nil {
		t.Fatalf("UpsertTurns empty: %v", err)
	}

	// Verify turn_texts is empty
	if err := database.conn.QueryRow(
		`SELECT COUNT(*) FROM turn_texts WHERE agent_type = 'test' AND session_id = 'cleanup-session'`,
	).Scan(&count); err != nil {
		t.Fatalf("count after empty upsert: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows after empty upsert, got %d", count)
	}

	// Verify watermark revision was updated
	rev, exists, err := database.GetWatermark("test", "cleanup-session")
	if err != nil {
		t.Fatalf("GetWatermark: %v", err)
	}
	if !exists {
		t.Fatal("watermark should exist after empty upsert")
	}
	if rev != 200 {
		t.Fatalf("expected revision 200, got %d", rev)
	}
}

func TestGetWatermark(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer database.Close()

	turns := []TurnText{
		{TurnIndex: 0, Role: "user", Content: "watermark test"},
	}
	if err := database.UpsertTurns("test", "wm-session", turns, 300); err != nil {
		t.Fatalf("UpsertTurns: %v", err)
	}

	rev, exists, err := database.GetWatermark("test", "wm-session")
	if err != nil {
		t.Fatalf("GetWatermark: %v", err)
	}
	if !exists {
		t.Fatal("watermark should exist")
	}
	if rev != 300 {
		t.Fatalf("expected revision 300, got %d", rev)
	}

	_, exists, err = database.GetWatermark("test", "nonexistent")
	if err != nil {
		t.Fatalf("GetWatermark nonexistent: %v", err)
	}
	if exists {
		t.Fatal("watermark should not exist for nonexistent session")
	}
}

func TestDeleteOrphansByAgent(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer database.Close()

	insert := func(sessionID string) {
		turns := []TurnText{
			{TurnIndex: 0, Role: "user", Content: "orphan test " + sessionID},
		}
		if err := database.UpsertTurns("test", sessionID, turns, 100); err != nil {
			t.Fatalf("UpsertTurns %s: %v", sessionID, err)
		}
	}
	insert("session-A")
	insert("session-B")
	insert("session-C")

	removed, err := database.DeleteOrphansByAgent("test", []string{"session-A", "session-B"})
	if err != nil {
		t.Fatalf("DeleteOrphansByAgent: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 orphan removed, got %d", removed)
	}

	// A and B remain
	for _, id := range []string{"session-A", "session-B"} {
		var count int
		if err := database.conn.QueryRow(
			`SELECT COUNT(*) FROM turn_texts WHERE agent_type = 'test' AND session_id = ?`,
			id,
		).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", id, err)
		}
		if count == 0 {
			t.Fatalf("%s should still exist", id)
		}
		_, exists, err := database.GetWatermark("test", id)
		if err != nil {
			t.Fatalf("GetWatermark %s: %v", id, err)
		}
		if !exists {
			t.Fatalf("watermark for %s should still exist", id)
		}
	}

	// C is deleted
	var count int
	if err := database.conn.QueryRow(
		`SELECT COUNT(*) FROM turn_texts WHERE agent_type = 'test' AND session_id = 'session-C'`,
	).Scan(&count); err != nil {
		t.Fatalf("count session-C: %v", err)
	}
	if count != 0 {
		t.Fatal("session-C should be deleted")
	}
	_, exists, err := database.GetWatermark("test", "session-C")
	if err != nil {
		t.Fatalf("GetWatermark session-C: %v", err)
	}
	if exists {
		t.Fatal("watermark for session-C should be deleted")
	}
}

func TestDeleteOrphansByAgent_EmptyKnown(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer database.Close()

	turns := []TurnText{
		{TurnIndex: 0, Role: "user", Content: "all orphans test"},
	}
	if err := database.UpsertTurns("test", "session-A", turns, 100); err != nil {
		t.Fatalf("UpsertTurns A: %v", err)
	}
	if err := database.UpsertTurns("test", "session-B", turns, 100); err != nil {
		t.Fatalf("UpsertTurns B: %v", err)
	}

	removed, err := database.DeleteOrphansByAgent("test", nil)
	if err != nil {
		t.Fatalf("DeleteOrphansByAgent empty: %v", err)
	}
	if removed != 2 {
		t.Fatalf("expected 2 orphans removed, got %d", removed)
	}

	// All turns deleted
	var count int
	if err := database.conn.QueryRow(
		`SELECT COUNT(*) FROM turn_texts WHERE agent_type = 'test'`,
	).Scan(&count); err != nil {
		t.Fatalf("count all: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 turns, got %d", count)
	}

	// All watermarks deleted
	if err := database.conn.QueryRow(
		`SELECT COUNT(*) FROM index_watermarks WHERE agent_type = 'test'`,
	).Scan(&count); err != nil {
		t.Fatalf("count watermarks: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 watermarks, got %d", count)
	}
}

func TestSnippetAround(t *testing.T) {
	const radius = 60

	t.Run("found in middle", func(t *testing.T) {
		content := "0123456789helloworld9876543210"
		got := snippetAround(content, "hello", 5)
		if len(got) == 0 {
			t.Fatal("expected non-empty snippet")
		}
		if !strings.HasPrefix(got, "…") {
			t.Fatal("expected leading ellipsis")
		}
		if !strings.HasSuffix(got, "…") {
			t.Fatal("expected trailing ellipsis")
		}
	})

	t.Run("not found", func(t *testing.T) {
		content := "abcdefghijklmnopqrstuvwxyz"
		got := snippetAround(content, "zzz", radius)
		if len(got) == 0 {
			t.Fatal("expected non-empty snippet")
		}
		if strings.HasPrefix(got, "…") {
			t.Fatal("unexpected leading ellipsis for not-found")
		}
	})

	t.Run("at start", func(t *testing.T) {
		content := "hello world rest of the text that continues on and on"
		got := snippetAround(content, "hello", 5)
		if len(got) == 0 {
			t.Fatal("expected non-empty snippet")
		}
		if strings.HasPrefix(got, "…") {
			t.Fatal("unexpected leading ellipsis at start")
		}
	})

	t.Run("at end", func(t *testing.T) {
		content := "a lot of text before the hello"
		got := snippetAround(content, "hello", 5)
		if len(got) == 0 {
			t.Fatal("expected non-empty snippet")
		}
		if strings.HasSuffix(got, "…") {
			t.Fatal("unexpected trailing ellipsis at end")
		}
	})

	t.Run("chinese characters", func(t *testing.T) {
		content := "前缀内容你好世界后缀内容更多文字"
		got := snippetAround(content, "你好世界", radius)
		if len(got) == 0 {
			t.Fatal("expected non-empty snippet for Chinese")
		}
	})

	t.Run("no html tags", func(t *testing.T) {
		content := "some plain text around the query term here"
		got := snippetAround(content, "query", radius)
		if strings.Contains(got, "<b>") {
			t.Fatal("snippet must not contain <b> HTML tags")
		}
	})
}
