package db

import "testing"

func TestBookmarkStorePersistsAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := database.AddBookmark("claude", "sess-1"); err != nil {
		t.Fatalf("AddBookmark: %v", err)
	}
	if err := database.AddBookmark("claude", "sess-1"); err != nil {
		t.Fatalf("AddBookmark duplicate: %v", err)
	}

	ok, err := database.IsBookmarked("claude", "sess-1")
	if err != nil {
		t.Fatalf("IsBookmarked: %v", err)
	}
	if !ok {
		t.Fatal("expected sess-1 to be bookmarked")
	}

	database.Close()
	database, err = Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer database.Close()

	ok, err = database.IsBookmarked("claude", "sess-1")
	if err != nil {
		t.Fatalf("IsBookmarked after reopen: %v", err)
	}
	if !ok {
		t.Fatal("expected bookmark to persist after reopen")
	}

	if err := database.RemoveBookmark("claude", "sess-1"); err != nil {
		t.Fatalf("RemoveBookmark: %v", err)
	}
	if err := database.RemoveBookmark("claude", "sess-1"); err != nil {
		t.Fatalf("RemoveBookmark duplicate: %v", err)
	}
	ok, err = database.IsBookmarked("claude", "sess-1")
	if err != nil {
		t.Fatalf("IsBookmarked after remove: %v", err)
	}
	if ok {
		t.Fatal("expected bookmark to be removed")
	}
}

func TestListBookmarksOrdersNewestFirst(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	if err := database.AddBookmark("claude", "older"); err != nil {
		t.Fatalf("AddBookmark older: %v", err)
	}
	if _, err := database.conn.Exec(
		`UPDATE bookmarked_sessions SET created_at = datetime('now', '-1 hour') WHERE agent_type = 'claude' AND session_id = 'older'`,
	); err != nil {
		t.Fatalf("age older bookmark: %v", err)
	}
	if err := database.AddBookmark("codex", "newer"); err != nil {
		t.Fatalf("AddBookmark newer: %v", err)
	}
	if err := database.UpdateBookmarkNote("codex", "newer", "Important handoff context"); err != nil {
		t.Fatalf("UpdateBookmarkNote newer: %v", err)
	}

	bookmarks, err := database.ListBookmarks()
	if err != nil {
		t.Fatalf("ListBookmarks: %v", err)
	}
	if len(bookmarks) != 2 {
		t.Fatalf("expected 2 bookmarks, got %d", len(bookmarks))
	}
	if bookmarks[0].AgentType != "codex" || bookmarks[0].SessionID != "newer" {
		t.Fatalf("expected newest bookmark first, got %+v", bookmarks[0])
	}
	if bookmarks[0].Note != "Important handoff context" {
		t.Fatalf("expected bookmark note, got %q", bookmarks[0].Note)
	}
}
