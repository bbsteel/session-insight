package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func sampleManifest() Manifest {
	return Manifest{
		Format:        Format,
		FormatVersion: FormatVersion,
		CreatedAt:     time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC),
		OriginHost:    "origin-box",
		CaseLabel:     "case-7",
		Options:       Options{IncludeRaw: true},
		Sessions: []SessionEntry{
			{
				AgentType: "claude",
				ID:        "sess-1",
				Title:     "Demo session",
				CreatedAt: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC),
				File:      "claude-sess-1.json",
				RawDir:    "claude-sess-1",
			},
		},
	}
}

func samplePayload() SessionPayload {
	return SessionPayload{
		Entry: sampleManifest().Sessions[0],
		Detail: &model.SessionDetail{
			Session: model.Session{
				ID:        "sess-1",
				AgentType: "claude",
				Name:      "Demo session",
			},
			Turns: []model.TurnVM{{TurnIndex: 0, UserMessage: "hi", AssistantMessage: "hello"}},
		},
		RenderEvents: []model.RenderEvent{
			{Type: "UserMessage", Text: "hi"},
			{Type: "AssistantMessage", Text: "hello"},
		},
		RawFiles: map[string][]byte{"transcript.jsonl": []byte(`{"line":1}`)},
	}
}

func TestWriteExtractRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	m := sampleManifest()
	if err := WriteBundle(&buf, m, []SessionPayload{samplePayload()}); err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}

	dest := t.TempDir()
	bundleID, got, err := Extract(&buf, dest)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !strings.Contains(bundleID, "-") || len(bundleID) != len("20060102-150405-")+6 {
		t.Errorf("unexpected bundle id shape %q", bundleID)
	}
	if got.Format != Format || got.FormatVersion != FormatVersion {
		t.Errorf("manifest format fields = %q/%d", got.Format, got.FormatVersion)
	}
	if got.OriginHost != "origin-box" || got.CaseLabel != "case-7" {
		t.Errorf("manifest metadata lost: %+v", got)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].ID != "sess-1" {
		t.Fatalf("manifest sessions = %+v", got.Sessions)
	}

	dir := filepath.Join(dest, bundleID)
	sessionJSON, err := os.ReadFile(filepath.Join(dir, "sessions", "claude-sess-1.json"))
	if err != nil {
		t.Fatalf("read extracted session: %v", err)
	}
	if !strings.Contains(string(sessionJSON), `"render_events"`) {
		t.Error("session payload missing render_events")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "raw", "claude-sess-1", "transcript.jsonl"))
	if err != nil {
		t.Fatalf("read extracted raw file: %v", err)
	}
	if string(raw) != `{"line":1}` {
		t.Errorf("raw content = %q", raw)
	}
	// No temp extraction dirs left behind.
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".extract-") {
			t.Errorf("temp dir left behind: %s", e.Name())
		}
	}
}

func TestExtractRejectsNewerFormatVersion(t *testing.T) {
	m := sampleManifest()
	m.FormatVersion = FormatVersion + 1
	var buf bytes.Buffer
	// WriteBundle validates too, so build the archive by hand.
	writeRawArchive(t, &buf, m, true)

	_, _, err := Extract(&buf, t.TempDir())
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("err = %v, want ErrUnsupportedVersion", err)
	}
}

func TestExtractRejectsPathTraversal(t *testing.T) {
	// Entries that must never be written under (or outside) dest.
	// Absolute and ".." forms are Zip Slip; outside-layout is also rejected.
	badNames := []string{
		"../escape.txt",
		"sessions/../../escape.txt",
		"/etc/passwd",
		"raw/../../etc/passwd",
		"not-in-layout.txt",
		`sessions\..\..\escape.txt`,
	}
	for _, name := range badNames {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			tw := tar.NewWriter(gz)
			body := []byte("pwned")
			if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write(body); err != nil {
				t.Fatal(err)
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}

			dest := t.TempDir()
			if _, _, err := Extract(&buf, dest); !errors.Is(err, ErrInvalidBundle) {
				t.Fatalf("err = %v, want ErrInvalidBundle", err)
			}
			// Nothing outside dest, and no leftover extract dirs with the payload.
			if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "escape.txt")); err == nil {
				t.Error("traversal file written outside dest")
			}
		})
	}
}

func TestNormalizeArchiveRel(t *testing.T) {
	ok, err := normalizeArchiveRel("sessions/claude-sess-1.json")
	if err != nil || ok != "sessions/claude-sess-1.json" {
		t.Fatalf("safe path: got %q, %v", ok, err)
	}
	for _, bad := range []string{"../x", "foo/../../x", "/abs", "other/file.json", "", "sessions/../../etc/passwd"} {
		if _, err := normalizeArchiveRel(bad); err == nil {
			t.Errorf("normalizeArchiveRel(%q) accepted", bad)
		}
	}
}

func TestExtractRejectsMissingSessionFile(t *testing.T) {
	m := sampleManifest()
	var buf bytes.Buffer
	writeRawArchive(t, &buf, m, false)
	if _, _, err := Extract(&buf, t.TempDir()); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("err = %v, want ErrInvalidBundle", err)
	}
}

// writeRawArchive builds a minimal valid-shaped archive with the given
// manifest, optionally including the declared session payload file.
func writeRawArchive(t *testing.T, buf *bytes.Buffer, m Manifest, withSession bool) {
	t.Helper()
	gz := gzip.NewWriter(buf)
	tw := tar.NewWriter(gz)
	add := func(name string, body []byte) {
		t.Helper()
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	manifestJSON, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	add("manifest.json", manifestJSON)
	if withSession {
		add("sessions/"+m.Sessions[0].File, []byte(`{"detail":{},"render_events":[]}`))
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsBadManifests(t *testing.T) {
	cases := map[string]func(*Manifest){
		"wrong format": func(m *Manifest) { m.Format = "other" },
		"zero version": func(m *Manifest) { m.FormatVersion = 0 },
		"no sessions":  func(m *Manifest) { m.Sessions = nil },
		"abs file":     func(m *Manifest) { m.Sessions[0].File = "/etc/passwd" },
		"dotdot file":  func(m *Manifest) { m.Sessions[0].File = "../x.json" },
		"empty file":   func(m *Manifest) { m.Sessions[0].File = "" },
		"abs raw dir":  func(m *Manifest) { m.Sessions[0].RawDir = "/tmp/raw" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := sampleManifest()
			mutate(&m)
			if err := Validate(&m); !errors.Is(err, ErrInvalidBundle) {
				t.Errorf("err = %v, want ErrInvalidBundle", err)
			}
		})
	}
}

func TestRedactSession(t *testing.T) {
	detail := &model.SessionDetail{
		Session: model.Session{ID: "s1", CWD: "/home/deck/projects/app"},
		Turns: []model.TurnVM{
			{
				TurnIndex:        0,
				UserMessage:      "use OPENAI_API_KEY=sk-abcdef1234567890 and token: ghp_0123456789abcdefghij",
				AssistantMessage: "see /Users/deck/notes.txt for details",
				Events: []model.EventVM{
					{Type: "x", Data: map[string]any{"cmd": "AWS_SECRET_ACCESS_KEY=AKIAIOSFODNN7EXAMPLE"}},
				},
			},
		},
	}
	events := []model.RenderEvent{
		{Type: "ToolResult", Stdout: "authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk"},
		{Type: "UserMessage", Text: "nothing sensitive here"},
	}

	n := RedactSession(detail, events, "/home/deck")
	if n == 0 {
		t.Fatal("expected redactions")
	}
	u := detail.Turns[0]
	if strings.Contains(u.UserMessage, "sk-abcdef") || strings.Contains(u.UserMessage, "ghp_0123") {
		t.Errorf("tokens not redacted: %q", u.UserMessage)
	}
	if !strings.Contains(u.UserMessage, redactedPlaceholder) {
		t.Errorf("placeholder missing: %q", u.UserMessage)
	}
	if got := u.Events[0].Data["cmd"]; strings.Contains(got.(string), "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("map value not redacted: %v", got)
	}
	if !strings.Contains(u.AssistantMessage, "~/notes.txt") {
		t.Errorf("mac home path not redacted: %q", u.AssistantMessage)
	}
	if !strings.Contains(detail.CWD, "~") {
		t.Errorf("homeDir not redacted: %q", detail.CWD)
	}
	if strings.Contains(events[0].Stdout, "eyJhbGci") {
		t.Errorf("JWT not redacted: %q", events[0].Stdout)
	}
	if events[1].Text != "nothing sensitive here" {
		t.Errorf("clean text mutated: %q", events[1].Text)
	}
}

func TestDeepCopyIsolation(t *testing.T) {
	detail := &model.SessionDetail{
		Session: model.Session{ID: "s1"},
		Turns:   []model.TurnVM{{UserMessage: "api_key=supersecretvalue"}},
	}
	dup := DeepCopyDetail(detail)
	RedactSession(dup, nil, "")
	if strings.Contains(detail.Turns[0].UserMessage, redactedPlaceholder) {
		t.Error("redaction leaked into original detail")
	}
}
