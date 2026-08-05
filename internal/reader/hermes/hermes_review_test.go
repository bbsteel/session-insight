package hermes

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	_ "github.com/mattn/go-sqlite3"
)

func TestHermesRejectsEmptySessionIDs(t *testing.T) {
	r := fixtureReader(t, "basic.sql")
	if _, err := r.readSessionRow(""); err == nil {
		t.Fatal("readSessionRow(\"\") should fail before querying")
	}
	if err := r.DeleteSession(""); err == nil {
		t.Fatal("DeleteSession(\"\") should fail before querying")
	}
}

func TestHermesSQLiteDSNEscapesSpecialPathAndFallsBackToStarted(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "dir?query#fragment", "state.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", sqliteDSN(path, "_busy_timeout=5000&_foreign_keys=on"))
	if err != nil {
		t.Fatal(err)
	}
	started := time.Unix(1767225600, 0)
	_, err = db.Exec(`
CREATE TABLE sessions (id TEXT PRIMARY KEY, started_at REAL, ended_at REAL);
CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT, role TEXT, content TEXT, timestamp REAL);
INSERT INTO sessions (id, started_at, ended_at) VALUES (?, ?, NULL);`, "hermes-no-messages", started.Unix())
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if dsn := sqliteDSN(path, "mode=ro"); strings.Contains(dsn, "?query#fragment") {
		t.Fatalf("sqlite DSN did not escape path: %q", dsn)
	}
	r, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Close() })
	list, err := r.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !list[0].UpdatedAt.Equal(started) {
		t.Fatalf("sessions=%+v, want one session updated at %v", list, started)
	}
}

func TestHermesListSessionsUsesActiveMessageSummary(t *testing.T) {
	r := fixtureReader(t, "rich.sql")
	db := writableFixtureDB(t, r.dbPath)
	_, err := db.Exec(`INSERT INTO messages (session_id, role, content, timestamp, active) VALUES (?, 'user', ?, ?, 0)`,
		"hermes-rich-001", "inactive prompt", 1767225900)
	if err != nil {
		t.Fatal(err)
	}
	list, err := r.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range list {
		if session.ID != "hermes-rich-001" {
			continue
		}
		if session.MessageCount != 8 || session.TurnCount != 2 {
			t.Fatalf("active summary=%+v, want message_count=8 turn_count=2", session)
		}
		if session.PreviewText != "update the sample file" {
			t.Fatalf("preview=%q, want first active user message", session.PreviewText)
		}
		return
	}
	t.Fatal("rich Hermes session not found")
}

func TestHermesContentAndToolCallFallbacks(t *testing.T) {
	for raw, want := range map[string]string{
		`{"unexpected":42}`: `{"unexpected":42}`,
		`42`:                `42`,
	} {
		if got := contentText(raw); got != want {
			t.Errorf("contentText(%q)=%q, want %q", raw, got, want)
		}
	}

	calls := parseToolInvocations(hermesMessage{
		ID:        "message-1",
		ToolCalls: `[{"name":"first"},{"name":"second"}]`,
	})
	if len(calls) != 2 || calls[0].ID == calls[1].ID {
		t.Fatalf("tool call IDs=%+v, want unique fallback IDs", calls)
	}
	turns := buildTurns([]hermesMessage{
		{Role: "user", Content: "prompt"},
		{Role: "assistant", Content: "answer", Reasoning: "thinking"},
	})
	if len(turns) != 1 || turns[0].AssistantMessage != "answer\nthinking" {
		t.Fatalf("turns=%+v, want locale-neutral reasoning separator", turns)
	}
	if strings.Contains(turns[0].AssistantMessage, "[思考]") {
		t.Fatalf("assistant message contains hard-coded locale marker: %q", turns[0].AssistantMessage)
	}
}

func TestHermesDelegationAndRequestDumpPredicates(t *testing.T) {
	if !isDelegateConfig("cli", `{"parent_session_id":"parent"}`) {
		t.Fatal("parent_session_id should identify delegated sessions")
	}
	if !isDelegateConfig("tool", "not-json") {
		t.Fatal("tool source should identify delegated sessions")
	}
	if isDelegateConfig("cli", `{}`) {
		t.Fatal("empty delegation metadata should not identify a delegated session")
	}

	cases := []struct {
		name string
		id   string
		want bool
	}{
		{"exact short id", "a", true},
		{"short id does not prefix longer id", "a", false},
		{"exact underscored id", "a_b", true},
		{"not a request dump", "a", false},
	}
	for _, tc := range cases {
		name := tc.name
		switch name {
		case "exact short id":
			name = "request_dump_a_1.json"
		case "short id does not prefix longer id":
			name = "request_dump_a_b_1.json"
		case "exact underscored id":
			name = "request_dump_a_b_1.json"
		default:
			name = "session_a_1.json"
		}
		if got := requestDumpMatchesSession(name, tc.id); got != tc.want {
			t.Errorf("requestDumpMatchesSession(%q,%q)=%v, want %v", name, tc.id, got, tc.want)
		}
	}
	canonical := "request_dump_20260804_102620_8cdc5c_20260804_114351_947119.json"
	if !requestDumpMatchesSession(canonical, "20260804_102620_8cdc5c") {
		t.Fatalf("canonical request dump did not match its session ID")
	}
	if requestDumpMatchesSession(canonical, "20260804_102620_8cdc5c_20260804") {
		t.Fatalf("canonical request dump matched a session-ID prefix")
	}
}

func TestHermesLegacyCleanupDoesNotDeleteIDPrefix(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"request_dump_a_1.json", "request_dump_a_b_1.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeLegacyFiles(dir, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "request_dump_a_1.json")); !os.IsNotExist(err) {
		t.Fatalf("target request dump remains or stat failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "request_dump_a_b_1.json")); err != nil {
		t.Fatalf("prefixed request dump was removed: %v", err)
	}
}

func TestHermesBillingPrecisionAndUsageErrors(t *testing.T) {
	r := fixtureReader(t, "rich.sql")
	db := writableFixtureDB(t, r.dbPath)
	if _, err := db.Exec(`UPDATE sessions SET estimated_cost_usd = 1.25, actual_cost_usd = NULL, cost_status = 'estimated' WHERE id = 'hermes-rich-001'`); err != nil {
		t.Fatal(err)
	}
	detail, err := r.GetSession("hermes-rich-001")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Billing == nil || detail.Billing.Precision != model.PrecisionEstimated {
		t.Fatalf("estimated billing=%+v", detail.Billing)
	}
	if _, err := db.Exec(`UPDATE sessions SET estimated_cost_usd = 1.25, actual_cost_usd = 2.5, cost_status = 'exact' WHERE id = 'hermes-rich-001'`); err != nil {
		t.Fatal(err)
	}
	detail, err = r.GetSession("hermes-rich-001")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Billing == nil || detail.Billing.Precision != model.PrecisionExact {
		t.Fatalf("exact billing=%+v", detail.Billing)
	}
	if _, err := db.Exec(`DROP TABLE session_model_usage`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetSession("hermes-rich-001"); err == nil || !strings.Contains(err.Error(), "hermes query usage") {
		t.Fatalf("usage query error=%v, want propagated query failure", err)
	}
}
