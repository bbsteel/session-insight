package hermes

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
	"github.com/bbsteel/session-insight/internal/reader/capability"
)

func TestHermesCapabilityEvidence(t *testing.T) {
	basicPath := fixtureDB(t, "basic.sql")
	adaptertest.RunFull(t, adaptertest.FullConfig{
		Config: adaptertest.Config{
			Capabilities: Capabilities(),
			NewReader: func(t *testing.T) adaptertest.Reader {
				r, err := New(basicPath)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = r.db.Close() })
				return r
			},
			Expect: adaptertest.Expectations{
				SessionCount: 1,
				SessionIDs:   []string{"hermes-basic-001"},
			},
		},
		Evidence: hermesEvidenceCases(),
	})
}

func TestHermesCapabilityEvidenceMatrix(t *testing.T) {
	rows := adaptertest.RunCapabilityEvidence(t, Capabilities(), hermesEvidenceCases(), adaptertest.CoverageOptions{
		BasicSatisfied: adaptertest.DefaultBasicSatisfied(),
	})
	if len(rows) < len(capability.BaselineIDs()) {
		t.Fatalf("evidence rows=%d", len(rows))
	}
}

func hermesEvidenceCases() []adaptertest.EvidenceCase {
	return []adaptertest.EvidenceCase{
		{
			Scenario: "tokens-tools-diff-subtasks-resume", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{
				capability.CapabilityTokens, capability.CapabilityToolResults, capability.CapabilityDiff,
				capability.CapabilitySubtasks, capability.CapabilityResume,
			},
			NewReader: func(t *testing.T) adaptertest.Reader {
				r := fixtureReader(t, "rich.sql")
				return r
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				id := "hermes-rich-001"
				adaptertest.AssertTokens(t, r, adaptertest.TokenExpect{
					SessionID:             id,
					RequireNonNilBilling:  true,
					RequireExactPrecision: true,
					ExactPrompt:           adaptertest.Int64(100),
					ExactCompletion:       adaptertest.Int64(50),
					ExactCacheRead:        adaptertest.Int64(11),
					ExactCacheWrite:       adaptertest.Int64(3),
					ExactReasoning:        adaptertest.Int64(7),
					PresentInput:          model.PresenceExact,
					PresentOutput:         model.PresenceExact,
					PresentCacheRead:      model.PresenceExact,
					PresentCacheWrite:     model.PresenceExact,
					PresentReasoning:      model.PresenceExact,
				})
				adaptertest.AssertToolResults(t, r, adaptertest.ToolResultsExpect{
					SessionID: id, MinPairs: 2, RequireSuccess: true, RequireFailure: true,
				})
				adaptertest.AssertDiff(t, r, adaptertest.DiffExpect{
					SessionID: id, FilePathSub: "a.go", OldSub: "old", NewSub: "new",
				})
				adaptertest.AssertSubtasks(t, r, adaptertest.SubtaskExpect{
					SessionID: id, MinSubagents: 1, RequireChildIDs: true,
				})
				adaptertest.AssertResume(t, r, adaptertest.ResumeExpect{SessionID: id, ExactID: id})
			},
		},
		{
			Scenario: "realtime-mutation", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{capability.CapabilityRealtime},
			NewReader: func(t *testing.T) adaptertest.Reader {
				return fixtureReader(t, "basic.sql")
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				hr := r.(*HermesReader)
				marker := "hermes-realtime-marker"
				adaptertest.AssertRealtimeStableThenMutate(t, r, "hermes-basic-001", func(t *testing.T) {
					db := writableFixtureDB(t, hr.dbPath)
					_, err := db.Exec(`INSERT INTO messages (session_id, role, content, timestamp, finish_reason) VALUES (?, 'user', ?, ?, NULL)`,
						"hermes-basic-001", marker, time.Now().Unix())
					if err != nil {
						t.Fatal(err)
					}
				}, adaptertest.RealtimeExpect{ContentMarker: marker})
			},
		},
		{
			Scenario: "delete-sandbox", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{capability.CapabilityDelete},
			NewReader: func(t *testing.T) adaptertest.Reader {
				path := fixtureDB(t, "rich.sql")
				db := writableFixtureDB(t, path)
				_, err := db.Exec(`INSERT INTO sessions (id, source, model, parent_session_id, model_config, started_at, ended_at, title) VALUES ('hermes-keep-001', 'cli', 'test', NULL, '{}', 1767225600, 1767225601, 'Keep')`)
				if err != nil {
					t.Fatal(err)
				}
				_, err = db.Exec(`INSERT INTO sessions (id, source, model, parent_session_id, model_config, started_at, ended_at, title) VALUES ('hermes-compression-child', 'cli', 'test', 'hermes-rich-001', '{"compression":true}', 1767225600, 1767225601, 'Compression child')`)
				if err != nil {
					t.Fatal(err)
				}
				r, err := New(path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = r.db.Close() })
				return r
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				adaptertest.AssertDeleteSandbox(t, r, "hermes-rich-001", "hermes-keep-001")
				list, err := r.ListSessions()
				if err != nil {
					t.Fatal(err)
				}
				for _, session := range list {
					if session.ID == "hermes-compression-child" {
						if session.ParentSessionID != "" {
							t.Fatalf("compression child parent=%q, want orphaned", session.ParentSessionID)
						}
						return
					}
				}
				t.Fatal("compression child was deleted instead of orphaned")
			},
		},
	}
}

func TestHermesReadsLegacySchema(t *testing.T) {
	r := fixtureReader(t, "legacy.sql")
	list, err := r.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "hermes-legacy-001" {
		t.Fatalf("legacy list=%+v", list)
	}
	detail, err := r.GetSession("hermes-legacy-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Turns) != 1 || detail.Turns[0].AssistantMessage != "legacy replay works" {
		t.Fatalf("legacy detail turns=%+v", detail.Turns)
	}
	if detail.Billing == nil || detail.Billing.Precision != model.PrecisionExact {
		t.Fatalf("legacy billing=%+v", detail.Billing)
	}
}

func TestHermesInterruptedSessionAndEmptyReplay(t *testing.T) {
	r := fixtureReader(t, "interrupted.sql")
	live, err := r.SessionLive("hermes-interrupted-001")
	if err != nil {
		t.Fatal(err)
	}
	if !live {
		t.Fatal("fresh unfinalized fixture should be live")
	}
	events, err := r.GetRenderEvents("hermes-interrupted-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[len(events)-1].Subtype != "in_progress" {
		t.Fatalf("interrupted events=%+v", events)
	}
	staleAt := time.Now().Add(-(model.LiveWindow + time.Second))
	for _, storePath := range []string{r.dbPath, r.dbPath + "-wal", r.dbPath + "-shm"} {
		if _, err := os.Stat(storePath); err == nil {
			if err := os.Chtimes(storePath, staleAt, staleAt); err != nil {
				t.Fatalf("age Hermes store %q: %v", storePath, err)
			}
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat Hermes store %q: %v", storePath, err)
		}
	}
	live, err = r.SessionLive("hermes-interrupted-001")
	if err != nil {
		t.Fatal(err)
	}
	if live {
		t.Fatal("stale unfinalized fixture should not be live")
	}

	path := filepath.Join(t.TempDir(), "empty.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, started_at REAL, ended_at REAL); CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT, role TEXT, content TEXT, timestamp REAL, finish_reason TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	empty, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = empty.db.Close() })
	list, err := empty.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if list == nil || len(list) != 0 {
		t.Fatalf("empty list=%v", list)
	}
	if events, err := empty.GetRenderEvents("missing"); err == nil || events != nil {
		t.Fatalf("missing render events=%v err=%v", events, err)
	}
}

func TestHermesNativeIntegerBounds(t *testing.T) {
	const maxInt64Value = int64(1<<63 - 1)
	const minInt64Value = -1 << 63

	if _, ok := asNativeInt(maxInt64Value); ok != (strconv.IntSize == 64) {
		t.Fatalf("max int64 conversion ok=%v on %d-bit host", ok, strconv.IntSize)
	}
	if _, ok := asNativeInt(minInt64Value); ok != (strconv.IntSize == 64) {
		t.Fatalf("min int64 conversion ok=%v on %d-bit host", ok, strconv.IntSize)
	}
	if got := firstInt(map[string]any{"exit_code": maxInt64Value}, "exit_code"); got == 0 {
		t.Fatal("out-of-range exit code must remain a failure signal")
	}
	if got := firstInt(map[string]any{"exit_code": minInt64Value}, "exit_code"); got == 0 {
		t.Fatal("negative out-of-range exit code must remain a failure signal")
	}
}

func TestResolveDBPathHonorsHermesHome(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "state.db")
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMES_HOME", home)
	t.Setenv("HERMES_STATE_DB", "")
	t.Setenv("HERMES_DB", "")
	got, ok := ResolveDBPath()
	if !ok || got != path {
		t.Fatalf("ResolveDBPath=(%q,%v), want (%q,true)", got, ok, path)
	}
}
