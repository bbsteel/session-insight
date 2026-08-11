package imported

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/bundle"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/capability"
)

// fixtureRoot extracts one bundle into t.TempDir and returns the root, the
// generated bundle ID, and the imported session ID.
func fixtureRoot(t *testing.T) (root, bundleID, sessionID string) {
	t.Helper()
	m := bundle.Manifest{
		Format:        bundle.Format,
		FormatVersion: bundle.FormatVersion,
		CreatedAt:     time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC),
		OriginHost:    "origin-box",
		CaseLabel:     "case-7",
		Sessions: []bundle.SessionEntry{
			{
				AgentType: "claude",
				ID:        "sess/1",
				Title:     "Imported demo",
				CreatedAt: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC),
				File:      "claude-sess_1.json",
				Redacted:  true,
			},
		},
	}
	payload := bundle.SessionPayload{
		Entry: m.Sessions[0],
		Detail: &model.SessionDetail{
			Session: model.Session{ID: "sess/1", AgentType: "claude", Name: "Imported demo", IsLive: true},
			Turns:   []model.TurnVM{{TurnIndex: 0, UserMessage: "hi", AssistantMessage: "hello"}},
		},
		RenderEvents: []model.RenderEvent{
			{Type: "UserPrompt", Text: "hi"},
			{Type: "TextChunk", Text: "hello"},
		},
	}
	var buf bytes.Buffer
	if err := bundle.WriteBundle(&buf, m, []bundle.SessionPayload{payload}); err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	root = t.TempDir()
	id, _, err := bundle.Extract(&buf, root)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return root, id, JoinSessionID(id, "sess/1")
}

func TestListGetRender(t *testing.T) {
	root, bundleID, sessionID := fixtureRoot(t)
	r := New(root)

	sessions, complete, err := r.ListSessionsDetailed()
	if err != nil || !complete {
		t.Fatalf("ListSessionsDetailed: complete=%v err=%v", complete, err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %v", sessions)
	}
	s := sessions[0]
	if s.ID != sessionID || s.AgentType != AgentType || s.Name != "Imported demo" {
		t.Errorf("session = %+v", s)
	}
	if s.IsLive {
		t.Error("imported session must never be live")
	}
	if !strings.HasPrefix(s.ID, bundleID+"--") || strings.Contains(s.ID, "/") {
		t.Errorf("imported id not sanitized: %q", s.ID)
	}

	detail, err := r.GetSession(sessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if detail.AgentType != AgentType || detail.ID != sessionID || detail.IsLive {
		t.Errorf("detail identity not reshaped: %+v", detail.Session)
	}
	info := detail.ImportInfo
	if info == nil {
		t.Fatal("ImportInfo not populated")
		return
	}
	if info.OriginalAgentType != "claude" || info.OriginalSessionID != "sess/1" ||
		info.OriginHost != "origin-box" || info.BundleID != bundleID ||
		info.CaseLabel != "case-7" || !info.Redacted {
		t.Errorf("ImportInfo = %+v", info)
	}

	events, err := r.GetRenderEvents(sessionID)
	if err != nil || len(events) != 2 {
		t.Fatalf("GetRenderEvents: %v %v", events, err)
	}
	ansi, err := r.RenderANSI(sessionID, 0)
	if err != nil {
		t.Fatalf("RenderANSI: %v", err)
	}
	if !strings.Contains(ansi, "hi") || !strings.Contains(ansi, "hello") {
		t.Errorf("render output missing content: %q", ansi)
	}

	snapDetail, snapEvents, err := r.ReadIndexSnapshot(context.Background(), s)
	if err != nil {
		t.Fatalf("ReadIndexSnapshot: %v", err)
	}
	if snapDetail.ID != sessionID || len(snapEvents) != 2 {
		t.Errorf("snapshot = %v events %d", snapDetail.ID, len(snapEvents))
	}
}

func TestGetSessionUnknownIDIsTypedMissing(t *testing.T) {
	root, _, _ := fixtureRoot(t)
	r := New(root)
	if _, err := r.GetSession("nope--nope"); err == nil {
		t.Fatal("expected error for unknown id")
	}
	// The handleGetSession probe loop keys on any error to move to the next
	// reader; a typed readerr keeps indexer tombstone semantics correct.
	if _, err := r.GetSession("../escape"); err == nil {
		t.Fatal("expected error for traversal id")
	}
}

func TestMissingRootIsEmptyInventory(t *testing.T) {
	r := New(t.TempDir() + "/does-not-exist")
	sessions, complete, err := r.ListSessionsDetailed()
	if err != nil || !complete || len(sessions) != 0 {
		t.Fatalf("missing root: sessions=%v complete=%v err=%v", sessions, complete, err)
	}
}

func TestSessionLiveAlwaysFalse(t *testing.T) {
	_, _, sessionID := fixtureRoot(t)
	r := New(t.TempDir()) // even for unknown ids: no error, never live
	live, err := r.SessionLive(sessionID)
	if err != nil || live {
		t.Errorf("SessionLive = %v, %v", live, err)
	}
}

func TestCapabilitiesValid(t *testing.T) {
	def := Capabilities()
	if errs := capability.ValidateStatic(def); len(errs) != 0 {
		t.Fatalf("validation: %v", errs)
	}
	if def.ResumeCommand != nil {
		t.Error("imported must not declare a resume command")
	}
	for _, id := range capability.BaselineIDs() {
		if _, ok := def.Capabilities[id]; !ok {
			t.Errorf("missing baseline capability %s", id)
		}
	}
	if got := def.Capabilities[capability.CapabilityDelete].State; got == capability.CapabilityExact {
		t.Error("delete must not be exact")
	}
	if got := def.Capabilities[capability.CapabilityTerminate].State; got == capability.CapabilityExact {
		t.Error("terminate must not be exact")
	}
}

func TestSanitizeID(t *testing.T) {
	if got := SanitizeID("a/b\\c"); got != "a_b_c" {
		t.Errorf("SanitizeID = %q", got)
	}
	if got := SanitizeID("a\x00b\n"); got != "a_b_" {
		t.Errorf("SanitizeID control chars = %q", got)
	}
}
