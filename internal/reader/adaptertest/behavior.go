package adaptertest

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

// RunBasicBehavior runs Layer 2 common reader checks against the fixture
// already bound into r. It never reads home directories.
func RunBasicBehavior(t *testing.T, r Reader, exp Expectations) {
	t.Helper()

	unknownID := exp.UnknownSessionID
	if unknownID == "" {
		unknownID = "__adaptertest_unknown_session_id__"
	}

	t.Run("behavior/list_stable", func(t *testing.T) {
		list1, err := r.ListSessions()
		if err != nil {
			t.Fatalf("ListSessions #1: %v", err)
		}
		list2, err := r.ListSessions()
		if err != nil {
			t.Fatalf("ListSessions #2: %v", err)
		}
		if len(list1) != exp.SessionCount {
			t.Fatalf("ListSessions count = %d, want %d", len(list1), exp.SessionCount)
		}
		if len(list2) != len(list1) {
			t.Fatalf("ListSessions unstable count: %d then %d", len(list1), len(list2))
		}
		ids1 := sessionIDs(list1)
		ids2 := sessionIDs(list2)
		if !reflect.DeepEqual(ids1, ids2) {
			t.Fatalf("ListSessions order/ids unstable:\n  first=%v\n  second=%v", ids1, ids2)
		}
		if err := checkUniqueNonEmpty(ids1); err != nil {
			t.Fatal(err)
		}
		if len(exp.SessionIDs) > 0 {
			if err := sameIDSet(ids1, exp.SessionIDs); err != nil {
				t.Fatal(err)
			}
		}
	})

	if exp.SessionCount == 0 {
		t.Run("behavior/empty_fixture_unknown_id", func(t *testing.T) {
			assertUnknownSession(t, r, unknownID)
		})
		return
	}

	list, err := r.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}

	t.Run("behavior/list_detail_agree", func(t *testing.T) {
		for _, s := range list {
			detail, err := r.GetSession(s.ID)
			if err != nil {
				t.Fatalf("GetSession(%q): %v", s.ID, err)
			}
			if detail == nil {
				t.Fatalf("GetSession(%q) returned nil detail without error", s.ID)
			}
			if detail.AgentType != s.AgentType {
				t.Errorf("%s: detail AgentType %q != list %q", s.ID, detail.AgentType, s.AgentType)
			}
			if detail.AgentType != r.AgentType() {
				t.Errorf("%s: detail AgentType %q != reader %q", s.ID, detail.AgentType, r.AgentType())
			}
			if detail.ID != s.ID {
				t.Errorf("detail ID %q != list ID %q", detail.ID, s.ID)
			}
			// CreatedAt should agree when both sides report a value (same second).
			if !s.CreatedAt.IsZero() && !detail.CreatedAt.IsZero() && s.CreatedAt.Unix() != detail.CreatedAt.Unix() {
				t.Errorf("%s: CreatedAt list=%v detail=%v", s.ID, s.CreatedAt, detail.CreatedAt)
			}
			// UpdatedAt may differ between list and detail when readers use
			// different sources (e.g. workspace metadata vs content mtime).
			// Require only that neither side reports CreatedAt after UpdatedAt.
			if !detail.CreatedAt.IsZero() && !detail.UpdatedAt.IsZero() && detail.CreatedAt.After(detail.UpdatedAt) {
				t.Errorf("%s: CreatedAt %v after UpdatedAt %v", s.ID, detail.CreatedAt, detail.UpdatedAt)
			}
			if !s.CreatedAt.IsZero() && !s.UpdatedAt.IsZero() && s.CreatedAt.After(s.UpdatedAt) {
				t.Errorf("%s list: CreatedAt %v after UpdatedAt %v", s.ID, s.CreatedAt, s.UpdatedAt)
			}
			// UpdatedAt must not be zero-time epoch noise when CreatedAt is set.
			if !s.CreatedAt.IsZero() && s.UpdatedAt.IsZero() {
				t.Errorf("%s list: UpdatedAt is zero while CreatedAt is set", s.ID)
			}
		}
	})

	t.Run("behavior/turns_stable_reread", func(t *testing.T) {
		for _, s := range list {
			d1, err := r.GetSession(s.ID)
			if err != nil {
				t.Fatalf("GetSession #1 %q: %v", s.ID, err)
			}
			d2, err := r.GetSession(s.ID)
			if err != nil {
				t.Fatalf("GetSession #2 %q: %v", s.ID, err)
			}
			if len(d1.Turns) != len(d2.Turns) {
				t.Fatalf("%s: turn count %d then %d (duplication?)", s.ID, len(d1.Turns), len(d2.Turns))
			}
			for i := range d1.Turns {
				if d1.Turns[i].TurnIndex != d2.Turns[i].TurnIndex {
					t.Errorf("%s: turn[%d] index %d vs %d", s.ID, i, d1.Turns[i].TurnIndex, d2.Turns[i].TurnIndex)
				}
				if d1.Turns[i].UserMessage != d2.Turns[i].UserMessage {
					t.Errorf("%s: turn[%d] user message unstable", s.ID, i)
				}
			}
			// Valid zero: empty turns is allowed; do not treat as failure.
		}
	})

	t.Run("behavior/render_events_stable_reread", func(t *testing.T) {
		for _, s := range list {
			e1, err1 := r.GetRenderEvents(s.ID)
			e2, err2 := r.GetRenderEvents(s.ID)
			// Both success or both same error class (unsupported vs not found).
			if (err1 == nil) != (err2 == nil) {
				t.Fatalf("%s: GetRenderEvents err %v then %v", s.ID, err1, err2)
			}
			if err1 != nil {
				continue
			}
			if len(e1) != len(e2) {
				t.Fatalf("%s: render event count %d then %d", s.ID, len(e1), len(e2))
			}
			for i := range e1 {
				if e1[i].Type != e2[i].Type || e1[i].TurnIndex != e2[i].TurnIndex {
					t.Errorf("%s: event[%d] unstable type/turn", s.ID, i)
					break
				}
			}
		}
	})

	t.Run("behavior/unknown_session", func(t *testing.T) {
		// Ensure unknown ID does not collide with fixture IDs.
		for _, s := range list {
			if s.ID == unknownID {
				t.Fatalf("UnknownSessionID %q collides with fixture session", unknownID)
			}
		}
		assertUnknownSession(t, r, unknownID)
	})

	t.Run("behavior/render_no_panic", func(t *testing.T) {
		for _, s := range list {
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						t.Fatalf("RenderANSI(%q) panicked: %v", s.ID, rec)
					}
				}()
				out, err := r.RenderANSI(s.ID, 80)
				// Either bytes or a non-nil error is fine; empty+nil is also allowed
				// for intentionally empty sessions. Panic is not.
				_ = out
				_ = err
			}()
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						t.Fatalf("GetRenderEvents(%q) panicked: %v", s.ID, rec)
					}
				}()
				_, _ = r.GetRenderEvents(s.ID)
			}()
		}
		// Unknown ID must not panic either.
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("RenderANSI(unknown) panicked: %v", rec)
				}
			}()
			_, _ = r.RenderANSI(unknownID, 80)
		}()
	})

	t.Run("behavior/missing_vs_empty_distinguishable", func(t *testing.T) {
		// Unknown session: GetSession must error.
		_, errMiss := r.GetSession(unknownID)
		if errMiss == nil {
			t.Fatal("GetSession(unknown) must return an error")
		}
		// Known sessions: GetSession succeeds (empty turns still succeed).
		for _, s := range list {
			d, err := r.GetSession(s.ID)
			if err != nil {
				t.Fatalf("GetSession(%q) should succeed for listed session: %v", s.ID, err)
			}
			if d == nil {
				t.Fatalf("GetSession(%q) nil detail", s.ID)
			}
			// Empty turns are a valid zero, not an error.
			_ = d.Turns
		}
	})
}

func assertUnknownSession(t *testing.T, r Reader, id string) {
	t.Helper()
	detail, err := r.GetSession(id)
	if err == nil {
		t.Fatalf("GetSession(%q) expected error, got detail=%v", id, detail)
	}
	// Error text should be actionable (mention not found / unknown / missing).
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "not found") &&
		!strings.Contains(msg, "unknown") &&
		!strings.Contains(msg, "no such") &&
		!strings.Contains(msg, "missing") &&
		!strings.Contains(msg, "invalid") {
		// Still fail if totally silent codes; allow agent-specific wording
		// that includes the id.
		if !strings.Contains(msg, strings.ToLower(id)) {
			t.Fatalf("GetSession(%q) error not actionable: %v", id, err)
		}
	}
}

func sessionIDs(list []model.Session) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = s.ID
	}
	return out
}

func checkUniqueNonEmpty(ids []string) error {
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" {
			return fmt.Errorf("empty session ID in list")
		}
		if seen[id] {
			return fmt.Errorf("duplicate session ID %q", id)
		}
		seen[id] = true
	}
	return nil
}

func sameIDSet(got, want []string) error {
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	if !reflect.DeepEqual(g, w) {
		return fmt.Errorf("session ID set got %v want %v", got, want)
	}
	return nil
}
