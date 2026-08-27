package render

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/presentation"
)

func TestNeutralCompileMatchesDefaultProfile(t *testing.T) {
	resolved := presentation.Resolve(presentation.NeutralDeclaration())
	got := CompilePresentation(resolved)
	if got.Name != defaultProfile.Name {
		t.Fatalf("Name=%q want %q", got.Name, defaultProfile.Name)
	}
	if got.Box != defaultProfile.Box {
		t.Fatalf("Box=%+v want %+v", got.Box, defaultProfile.Box)
	}
	if got.UserHeader != "" || got.AssistantHeader || got.GroupToolRuns || got.ToolBullet || got.ResultBox {
		t.Fatalf("neutral compile enabled custom style flags: %+v", got.Style)
	}
	if got.Preprocess != nil {
		t.Fatal("neutral compile must not attach a preprocess callback")
	}
}

func TestNeutralCompileMatchesDefaultANSIAndPositions(t *testing.T) {
	events := defaultCompilerEvents()
	wantANSI, wantPos, wantLines := formatEvents(events, 100, Options{})
	resolved := presentation.Resolve(presentation.NeutralDeclaration())
	gotANSI, gotPos, gotLines := formatEventsWithProfile(events, 100, Options{}, CompilePresentation(resolved))
	if gotANSI != wantANSI {
		t.Fatalf("ANSI mismatch\nwant:\n%s\ngot:\n%s", wantANSI, gotANSI)
	}
	if gotLines != wantLines {
		t.Fatalf("lines=%d want %d", gotLines, wantLines)
	}
	wantJSON, err := json.Marshal(wantPos)
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, err := json.Marshal(gotPos)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("positions mismatch\nwant %s\ngot %s", wantJSON, gotJSON)
	}
}

func TestLegacyAgentsKeepProductionProfiles(t *testing.T) {
	ts := time.Now()
	for _, agent := range []string{"claude", "chrys", "grok"} {
		events := []model.RenderEvent{
			{EventID: "b0", Type: "TurnBoundary", TurnIndex: 0, Timestamp: ts, AgentType: agent},
			{EventID: "u0", Type: "UserPrompt", TurnIndex: 0, Timestamp: ts, AgentType: agent, Text: "hi"},
			{EventID: "i0", Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, AgentType: agent,
				ToolName: "Bash", ToolCallID: "c1", ToolInput: map[string]any{"command": "ls"}},
			{EventID: "r0", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: agent, ToolCallID: "c1", Stdout: "ok"},
		}
		legacy := profileFor(events)
		if legacy.Name != agent {
			t.Fatalf("%s profileFor Name=%q", agent, legacy.Name)
		}
		compiled := CompilePresentation(presentation.Resolve(presentation.NativeNeutralDeclaration(agent)))
		if compiled.Name == agent {
			t.Fatalf("%s: compiling the Slice B declaration must not impersonate the legacy profile", agent)
		}
		ansi := FormatEvents(events, 100)
		switch agent {
		case "claude":
			if !strings.Contains(ansi, "▼ Tools") || !strings.Contains(ansi, "╔") {
				t.Fatalf("claude production ANSI lost legacy layout:\n%s", ansi)
			}
		case "chrys":
			if !strings.Contains(ansi, "❯ You") || !strings.Contains(ansi, "╭") {
				t.Fatalf("chrys production ANSI lost legacy layout:\n%s", ansi)
			}
		case "grok":
			if !strings.Contains(ansi, "╭") {
				t.Fatalf("grok production ANSI lost legacy layout:\n%s", ansi)
			}
		}
	}
}

func defaultCompilerEvents() []model.RenderEvent {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return []model.RenderEvent{
		{EventID: "b0", Type: "TurnBoundary", TurnIndex: 0, Timestamp: ts, AgentType: "codex"},
		{EventID: "u0", Type: "UserPrompt", TurnIndex: 0, Timestamp: ts, AgentType: "codex", Text: "hello"},
		{EventID: "x0", Type: "TextChunk", TurnIndex: 0, Timestamp: ts, AgentType: "codex", Text: "world"},
		{EventID: "i0", Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, AgentType: "codex",
			ToolName: "Bash", ToolCallID: "c1", ToolInput: map[string]any{"command": "ls"}},
		{EventID: "r0", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: "codex",
			ToolCallID: "c1", Stdout: "a.txt"},
	}
}
