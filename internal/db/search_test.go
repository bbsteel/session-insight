//go:build sqlite_fts5

package db

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func setupSearchDB(t *testing.T) *DB {
	t.Helper()
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}

	// sess-1
	if err := database.UpsertTurns("test", "sess-1", []TurnText{
		{TurnIndex: 0, Role: "user", Content: "hello world search test"},
		{TurnIndex: -1, Role: "meta", Content: "My Project repo"},
	}, 100); err != nil {
		database.Close()
		t.Fatalf("UpsertTurns sess-1: %v", err)
	}

	// sess-2
	if err := database.UpsertTurns("test", "sess-2", []TurnText{
		{TurnIndex: 0, Role: "user", Content: "fix bug in /foo/bar.go"},
		{TurnIndex: 1, Role: "user", Content: "another attempt"},
		{TurnIndex: -1, Role: "meta", Content: "Bug Bash"},
	}, 200); err != nil {
		database.Close()
		t.Fatalf("UpsertTurns sess-2: %v", err)
	}

	// sess-3
	if err := database.UpsertTurns("test", "sess-3", []TurnText{
		{TurnIndex: 0, Role: "user", Content: "你好世界 中文测试"},
		{TurnIndex: -1, Role: "meta", Content: "中文项目"},
	}, 300); err != nil {
		database.Close()
		t.Fatalf("UpsertTurns sess-3: %v", err)
	}

	// metric-only
	if err := database.UpsertTurns("test", "metric-only", []TurnText{
		{TurnIndex: -1, Role: "meta", Content: "Metrics Dashboard"},
	}, 400); err != nil {
		database.Close()
		t.Fatalf("UpsertTurns metric-only: %v", err)
	}

	return database
}

func containsSession(results []TurnSearchResult, want string) bool {
	for _, r := range results {
		if r.SessionID == want {
			return true
		}
	}
	return false
}

func TestSearch_Chinese(t *testing.T) {
	database := setupSearchDB(t)
	defer database.Close()

	// 4-rune query: should return sess-3
	results, err := database.SearchTurns("中文测试", 30)
	if err != nil {
		t.Fatalf("SearchTurns('中文测试'): %v", err)
	}
	if !containsSession(results, "sess-3") {
		t.Fatalf("expected sess-3 in results for '中文测试', got %v", results)
	}

	// 2-rune query: now uses LIKE fallback, should find sess-3
	results, err = database.SearchTurns("你好", 30)
	if err != nil {
		t.Fatalf("SearchTurns('你好'): %v", err)
	}
	if !containsSession(results, "sess-3") {
		t.Fatalf("expected sess-3 in results for '你好' (LIKE fallback), got %v", results)
	}
}

func TestSearch_EnglishCaseInsensitive(t *testing.T) {
	database := setupSearchDB(t)
	defer database.Close()

	results, err := database.SearchTurns("Hello", 30)
	if err != nil {
		t.Fatalf("SearchTurns('Hello'): %v", err)
	}
	if !containsSession(results, "sess-1") {
		t.Fatalf("expected sess-1 in results for 'Hello', got %v", results)
	}
}

func TestSearch_FilePath(t *testing.T) {
	database := setupSearchDB(t)
	defer database.Close()

	results, err := database.SearchTurns("/foo/bar.go", 30)
	if err != nil {
		t.Fatalf("SearchTurns('/foo/bar.go'): %v", err)
	}
	if !containsSession(results, "sess-2") {
		t.Fatalf("expected sess-2 in results for '/foo/bar.go', got %v", results)
	}
}

func TestSearch_SpecialChars(t *testing.T) {
	database := setupSearchDB(t)
	defer database.Close()

	// 1-rune queries (never reach FTS, test early rejection)
	singleRune := []string{`"`, `*`, `(`, `)`, `%`, `_`, `'`}
	for _, q := range singleRune {
		results, err := database.SearchTurns(q, 30)
		if err != nil {
			t.Fatalf("SearchTurns(%q): unexpected error: %v", q, err)
		}
		_ = results
	}

	// 3+ rune queries that look like FTS operators (should be treated as literals)
	ftsLike := []string{
		`foo OR bar`,     // FTS OR operator — must not be parsed as boolean
		`hello AND bye`,  // FTS AND operator
		`search NOT bug`, // FTS NOT operator
		`"hello"`,        // nested double quotes — prepareFTSQuery escapes inner
		`he*llo`,         // FTS prefix operator
	}
	for _, q := range ftsLike {
		results, err := database.SearchTurns(q, 30)
		if err != nil {
			t.Fatalf("SearchTurns(%q): unexpected error: %v", q, err)
		}
		_ = results
	}
}

func TestSearch_ShortQuery(t *testing.T) {
	database := setupSearchDB(t)
	defer database.Close()

	// 1-rune query: now uses LIKE fallback, should find sessions with 'a'
	results, err := database.SearchTurns("a", 30)
	if err != nil {
		t.Fatalf("SearchTurns('a'): unexpected error: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected results for single-rune LIKE query 'a', got empty")
	}
	// sess-2 has "another" and "bar.go", metric-only has "Dashboard"
	if !containsSession(results, "sess-2") {
		t.Fatalf("expected sess-2 in results for 'a', got %v", results)
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	database := setupSearchDB(t)
	defer database.Close()

	results, err := database.SearchTurns("", 30)
	if err != nil {
		t.Fatalf("SearchTurns(''): unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected empty results for empty query, got %v", results)
	}
}

func TestSearch_OneResultPerSession(t *testing.T) {
	database := setupSearchDB(t)
	defer database.Close()

	// Insert more turns into sess-1 that also match "search"
	if err := database.UpsertTurns("test", "sess-1", []TurnText{
		{TurnIndex: 0, Role: "user", Content: "hello world search test"},
		{TurnIndex: 1, Role: "user", Content: "another search in same session"},
		{TurnIndex: 2, Role: "user", Content: "yet another search hit"},
		{TurnIndex: -1, Role: "meta", Content: "My Project repo"},
	}, 500); err != nil {
		t.Fatalf("UpsertTurns sess-1 update: %v", err)
	}

	results, err := database.SearchTurns("search", 30)
	if err != nil {
		t.Fatalf("SearchTurns('search'): %v", err)
	}

	count := 0
	for _, r := range results {
		if r.SessionID == "sess-1" {
			count++
		}
	}
	if count > 1 {
		t.Fatalf("sess-1 appears %d times in results, expected at most 1", count)
	}
}

func TestSearch_MetaFallback(t *testing.T) {
	database := setupSearchDB(t)
	defer database.Close()

	results, err := database.SearchTurns("Metrics", 30)
	if err != nil {
		t.Fatalf("SearchTurns('Metrics'): %v", err)
	}
	if !containsSession(results, "metric-only") {
		t.Fatalf("expected metric-only in results for 'Metrics', got %v", results)
	}

	results, err = database.SearchTurns("Dashboard", 30)
	if err != nil {
		t.Fatalf("SearchTurns('Dashboard'): %v", err)
	}
	if !containsSession(results, "metric-only") {
		t.Fatalf("expected metric-only in results for 'Dashboard', got %v", results)
	}
}

func TestSearch_SnippetNoHTML(t *testing.T) {
	database := setupSearchDB(t)
	defer database.Close()

	results, err := database.SearchTurns("hello", 30)
	if err != nil {
		t.Fatalf("SearchTurns('hello'): %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result for 'hello'")
	}
	for _, r := range results {
		if strings.Contains(r.Match, `<b>`) || strings.Contains(r.Match, `&lt;b&gt;`) {
			t.Fatalf("Match contains HTML: %q", r.Match)
		}
	}
}

func TestSearch_Limit(t *testing.T) {
	database := setupSearchDB(t)
	defer database.Close()

	results, err := database.SearchTurns("search", 200)
	if err != nil {
		t.Fatalf("SearchTurns('search', 200): %v", err)
	}
	// limit 200 > 100 should be capped to 30 (default)
	if len(results) > 30 {
		t.Fatalf("expected at most 30 results with capped limit, got %d", len(results))
	}
}

func TestSearch_NULFiltered(t *testing.T) {
	database := setupSearchDB(t)
	defer database.Close()

	results, err := database.SearchTurns("hel\x00lo", 30)
	if err != nil {
		t.Fatalf("SearchTurns with NUL byte: unexpected error: %v", err)
	}
	_ = results
}

func TestPrepareFTSQuery(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", `"hello"`},
		{`he"llo`, `"he""llo"`},
		{"", `""`},
		{"test\x00data", `"testdata"`},
	}

	for _, tc := range tests {
		got := prepareFTSQuery(tc.input)
		if got != tc.expected {
			t.Fatalf("prepareFTSQuery(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestPrepareFTSQuery_UTF8Truncation(t *testing.T) {
	// Build a query that exceeds maxQueryBytes, ending in the middle of a multi-byte CJK char.
	// Each CJK char is 3 bytes. 4096 is not divisible by 3.
	// After truncation, the result must be valid UTF-8 and not end mid-character.
	prefix := strings.Repeat("x", maxQueryBytes-3)
	cjk := prefix + "你好世界" // "你好世界" = 12 bytes, so this exceeds 4096

	q := prepareFTSQuery(cjk)
	// Must be valid UTF-8
	if !utf8.ValidString(q) {
		t.Fatalf("prepareFTSQuery produced invalid UTF-8: %q", q)
	}
	// Should start with " and end with "
	if len(q) < 2 || q[0] != '"' || q[len(q)-1] != '"' {
		t.Fatalf("prepareFTSQuery not properly quoted: %q", q)
	}
}
