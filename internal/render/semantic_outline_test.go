package render

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// buildOutline runs the real formatter over events to produce core positions,
// then classifies the outline exactly the way the positions cache build does.
func buildOutline(t *testing.T, events []model.RenderEvent, detail *model.SessionDetail) []RenderPosition {
	t.Helper()
	fr := FormatEventsResultOpts(events, 100, Options{})
	outline, _ := BuildSemanticOutline(SemanticOutlineInput{
		Events:        events,
		Detail:        detail,
		CorePositions: fr.Positions,
	})
	return outline
}

func outlineCodes(positions []RenderPosition) []string {
	var codes []string
	for _, p := range positions {
		code, _ := p.Payload["code"].(string)
		codes = append(codes, code)
	}
	return codes
}

func inv(turn int, callID, tool string, input map[string]any) model.RenderEvent {
	return model.RenderEvent{
		Type: "ToolInvocation", TurnIndex: turn, Depth: 0, Timestamp: time.Now(),
		EventID: "ev-" + callID, ToolName: tool, ToolCallID: callID, ToolInput: input,
	}
}

func okResult(turn int, callID string) model.RenderEvent {
	return model.RenderEvent{
		Type: "ToolResult", TurnIndex: turn, Depth: 0, Timestamp: time.Now(),
		ToolName: "Bash", ToolCallID: callID, Stdout: "done",
	}
}

func TestOutlineRejectedTimeoutFailed(t *testing.T) {
	cases := []struct {
		name   string
		result model.RenderEvent
		code   string
	}{
		{"rejected", model.RenderEvent{Type: "ToolResult", TurnIndex: 0, ToolCallID: "c1", Rejected: true, ErrorKind: "permission"}, OutlineCodeToolRejected},
		{"timeout", model.RenderEvent{Type: "ToolResult", TurnIndex: 0, ToolCallID: "c1", TimedOut: true, TimeoutSeconds: 30}, OutlineCodeToolTimeout},
		{"nonzero exit", model.RenderEvent{Type: "ToolResult", TurnIndex: 0, ToolCallID: "c1", ExitCode: 1}, OutlineCodeToolFailed},
		{"stderr", model.RenderEvent{Type: "ToolResult", TurnIndex: 0, ToolCallID: "c1", Stderr: "boom"}, OutlineCodeToolFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := []model.RenderEvent{
				{Type: "TurnBoundary", TurnIndex: 0},
				inv(0, "c1", "Bash", map[string]any{"command": "make deploy"}),
				tc.result,
			}
			outline := buildOutline(t, events, nil)
			codes := outlineCodes(outline)
			if len(codes) != 1 || codes[0] != tc.code {
				t.Fatalf("codes = %v, want exactly [%s]", codes, tc.code)
			}
			p := outline[0]
			if p.Kind != "outline" || p.Payload["category"] != OutlineCatAnomaly || p.Payload["precision"] != "exact" {
				t.Fatalf("bad outline position: %+v", p.Payload)
			}
			if !strings.HasPrefix(p.PositionKey, "outline:anomaly:"+tc.code+":0:c1:0") {
				t.Fatalf("unstable key: %s", p.PositionKey)
			}
		})
	}
}

func TestOutlineVerificationFailedNotDuplicated(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "TurnBoundary", TurnIndex: 0},
		inv(0, "c1", "Bash", map[string]any{"command": "go test ./..."}),
		{Type: "ToolResult", TurnIndex: 0, ToolCallID: "c1", ExitCode: 1, Stderr: "FAIL"},
	}
	outline := buildOutline(t, events, nil)
	codes := outlineCodes(outline)
	if len(codes) != 1 || codes[0] != OutlineCodeVerificationFailed {
		t.Fatalf("codes = %v, want exactly [verification_failed] (no tool_failed/key_result duplicate)", codes)
	}
}

func TestOutlineKeyResultAllowlist(t *testing.T) {
	positive := map[string]string{
		"go test ./...":                OutlineCodeTestsPassed,
		"go vet ./...":                 OutlineCodeLintPassed,
		"go build ./cmd/app":           OutlineCodeBuildSucceeded,
		"npm test":                     OutlineCodeTestsPassed,
		"npm run build":                OutlineCodeBuildSucceeded,
		"pnpm lint":                    OutlineCodeLintPassed,
		"yarn typecheck":               OutlineCodeTypecheckPassed,
		"bun run check":                OutlineCodeChecksPassed,
		"tsc --noEmit":                 OutlineCodeTypecheckPassed,
		"pytest -q":                    OutlineCodeTestsPassed,
		"python -m pytest tests":       OutlineCodeTestsPassed,
		"cargo test --all":             OutlineCodeTestsPassed,
		"cargo build --release":        OutlineCodeBuildSucceeded,
		"cargo clippy":                 OutlineCodeLintPassed,
		"cargo check":                  OutlineCodeChecksPassed,
		"dotnet test":                  OutlineCodeTestsPassed,
		"mvn verify":                   OutlineCodeChecksPassed,
		"./gradlew build":              OutlineCodeBuildSucceeded,
		"ctest --output-on-failure":    OutlineCodeTestsPassed,
		"swift test":                   OutlineCodeTestsPassed,
		"make test":                    OutlineCodeTestsPassed,
		"make -C pkg build":            OutlineCodeBuildSucceeded,
		"cd frontend && npm run build": OutlineCodeBuildSucceeded,
		"cd app; go test ./...":        OutlineCodeTestsPassed,
	}
	for cmd, want := range positive {
		t.Run("ok/"+cmd, func(t *testing.T) {
			events := []model.RenderEvent{
				{Type: "TurnBoundary", TurnIndex: 0},
				inv(0, "c1", "Bash", map[string]any{"command": cmd}),
				okResult(0, "c1"),
			}
			codes := outlineCodes(buildOutline(t, events, nil))
			if len(codes) != 1 || codes[0] != want {
				t.Fatalf("codes = %v, want [%s]", codes, want)
			}
		})
	}

	negative := []string{
		`echo "tests passed"`,
		"go testa ./...",  // not a real go subcommand
		"npm run testish", // arbitrary script containing "test"
		"git status",
		"ls -la",
		"rg foo",
		"cat package.json",
		"curl https://example.com",
		"make deploy",
		"tsc", // without --noEmit this emits output; not a check
	}
	for _, cmd := range negative {
		t.Run("noise/"+cmd, func(t *testing.T) {
			events := []model.RenderEvent{
				{Type: "TurnBoundary", TurnIndex: 0},
				inv(0, "c1", "Bash", map[string]any{"command": cmd}),
				okResult(0, "c1"),
			}
			if codes := outlineCodes(buildOutline(t, events, nil)); len(codes) != 0 {
				t.Fatalf("codes = %v, want none (noise must stay out of the outline)", codes)
			}
		})
	}

	t.Run("unknown status stays out", func(t *testing.T) {
		events := []model.RenderEvent{
			{Type: "TurnBoundary", TurnIndex: 0},
			inv(0, "c1", "Bash", map[string]any{"command": "go test ./..."}),
			// no result event at all
		}
		if codes := outlineCodes(buildOutline(t, events, nil)); len(codes) != 0 {
			t.Fatalf("codes = %v, want none for unknown status", codes)
		}
	})
}

func TestOutlineFileChanges(t *testing.T) {
	t.Run("modify/create/delete/rename", func(t *testing.T) {
		patch := "*** Begin Patch\n" +
			"*** Update File: a.go\n@@\n-old\n+new\n" +
			"*** Add File: b.go\n+hello\n" +
			"*** Delete File: c.go\n-gone\n" +
			"*** Update File: d.go\n*** Move to: e.go\n@@\n-x\n+y\n" +
			"*** End Patch"
		events := []model.RenderEvent{
			{Type: "TurnBoundary", TurnIndex: 0},
			inv(0, "c1", "apply_patch", map[string]any{"input": patch}),
			okResult(0, "c1"),
		}
		outline := buildOutline(t, events, nil)
		got := map[string]string{}
		for _, p := range outline {
			fp, _ := p.Payload["file_path"].(string)
			code, _ := p.Payload["code"].(string)
			got[fp] = code
		}
		want := map[string]string{
			"a.go": OutlineCodeFileModified,
			"b.go": OutlineCodeFileCreated,
			"c.go": OutlineCodeFileDeleted,
			"e.go": OutlineCodeFileRenamed,
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("file changes = %v, want %v", got, want)
		}
		for _, p := range outline {
			if p.Payload["file_path"] == "e.go" && p.Payload["previous_file_path"] != "d.go" {
				t.Fatalf("rename missing previous_file_path: %+v", p.Payload)
			}
		}
	})

	t.Run("claude edit", func(t *testing.T) {
		events := []model.RenderEvent{
			{Type: "TurnBoundary", TurnIndex: 0},
			inv(0, "c1", "Edit", map[string]any{"file_path": "x.go", "old_string": "a", "new_string": "b"}),
			okResult(0, "c1"),
		}
		codes := outlineCodes(buildOutline(t, events, nil))
		if len(codes) != 1 || codes[0] != OutlineCodeFileModified {
			t.Fatalf("codes = %v, want [file_modified]", codes)
		}
	})

	t.Run("failed edit yields anomaly only", func(t *testing.T) {
		events := []model.RenderEvent{
			{Type: "TurnBoundary", TurnIndex: 0},
			inv(0, "c1", "Edit", map[string]any{"file_path": "x.go", "old_string": "a", "new_string": "b"}),
			{Type: "ToolResult", TurnIndex: 0, ToolCallID: "c1", Stderr: "no such file"},
		}
		codes := outlineCodes(buildOutline(t, events, nil))
		if len(codes) != 1 || codes[0] != OutlineCodeToolFailed {
			t.Fatalf("codes = %v, want [tool_failed] only", codes)
		}
	})

	t.Run("missing result is unverified, never success", func(t *testing.T) {
		events := []model.RenderEvent{
			{Type: "TurnBoundary", TurnIndex: 0},
			inv(0, "c1", "Edit", map[string]any{"file_path": "x.go", "old_string": "a", "new_string": "b"}),
		}
		outline := buildOutline(t, events, nil)
		codes := outlineCodes(outline)
		if len(codes) != 1 || codes[0] != OutlineCodeFileChangeUnverifed {
			t.Fatalf("codes = %v, want [file_change_unverified]", codes)
		}
		if outline[0].Payload["precision"] != "estimated" || outline[0].Severity != "warning" {
			t.Fatalf("unverified change must be estimated+warning: %+v", outline[0].Payload)
		}
	})
}

func TestOutlineContextEvents(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "TurnBoundary", TurnIndex: 0},
		{Type: "UserPrompt", TurnIndex: 0, Text: "work"},
		{Type: "CompactionBoundary", TurnIndex: 0},
		{Type: "RollbackStart", TurnIndex: 1, Metadata: map[string]any{"count": 1, "resume_turn": 1}},
		{Type: "TurnBoundary", TurnIndex: 1, Metadata: map[string]any{"rolled_back": true, "original_turn_index": 1}},
		{Type: "UserPrompt", TurnIndex: 1, Text: "abandoned"},
		{Type: "RollbackEnd", TurnIndex: 1},
	}
	outline := buildOutline(t, events, nil)
	codes := outlineCodes(outline)
	if len(codes) != 2 {
		t.Fatalf("codes = %v, want exactly one compaction and one rollback", codes)
	}
	seen := map[string]int{}
	for _, c := range codes {
		seen[c]++
	}
	if seen[OutlineCodeCompaction] != 1 || seen[OutlineCodeRollback] != 1 {
		t.Fatalf("codes = %v, want compaction×1 + rollback×1", codes)
	}
	// Context items trace back to their core position.
	for _, p := range outline {
		src, _ := p.Payload["source_position_key"].(string)
		if !strings.HasPrefix(src, "compaction:") && !strings.HasPrefix(src, "rollback:") {
			t.Fatalf("context item %s lacks core source key: %q", p.Payload["code"], src)
		}
	}
}

func TestOutlineSessionFacts(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "TurnBoundary", TurnIndex: 0},
		{Type: "UserPrompt", TurnIndex: 0, Text: "slow work"},
		{Type: "TurnBoundary", TurnIndex: 1},
		{Type: "UserPrompt", TurnIndex: 1, Text: "more"},
	}
	detail := &model.SessionDetail{
		Turns: []model.TurnVM{
			{TurnIndex: 0, DurationMs: 120000, Anomalies: []string{"duration_spike"}},
			{TurnIndex: 1, Anomalies: []string{"continuation_nudge"}},
		},
		AnomalySummary: model.AnomalySummary{MissingShutdown: true},
	}
	outline := buildOutline(t, events, detail)
	var codes []string
	for _, p := range outline {
		code, _ := p.Payload["code"].(string)
		if p.Payload["precision"] != "estimated" {
			t.Fatalf("session fact %s must be estimated", code)
		}
		codes = append(codes, code)
	}
	joined := strings.Join(codes, ",")
	if !strings.Contains(joined, OutlineCodeDurationSpike) {
		t.Fatalf("missing duration_spike in %v", codes)
	}
	if !strings.Contains(joined, OutlineCodeMissingShutdown) {
		t.Fatalf("missing missing_shutdown in %v", codes)
	}
	if strings.Contains(joined, "continuation") {
		t.Fatalf("continuation nudge must not become a key event: %v", codes)
	}
}

func TestOutlineDeterministicKeysAndOrder(t *testing.T) {
	patch := "*** Begin Patch\n" +
		"*** Update File: z.go\n@@\n-a\n+b\n" +
		"*** Update File: m.go\n@@\n-c\n+d\n" +
		"*** Update File: a.go\n@@\n-e\n+f\n" +
		"*** End Patch"
	events := []model.RenderEvent{
		{Type: "TurnBoundary", TurnIndex: 0},
		inv(0, "c1", "apply_patch", map[string]any{"input": patch}),
		okResult(0, "c1"),
	}
	first := buildOutline(t, events, nil)
	for run := 0; run < 20; run++ {
		got := buildOutline(t, events, nil)
		if len(got) != len(first) {
			t.Fatalf("run %d: count changed", run)
		}
		for i := range got {
			if got[i].PositionKey != first[i].PositionKey {
				t.Fatalf("run %d: key %d changed %s → %s", run, i, first[i].PositionKey, got[i].PositionKey)
			}
		}
	}
	// Occurrence order follows sorted normalized paths, not patch order.
	var paths []string
	for _, p := range first {
		paths = append(paths, p.Payload["file_path"].(string))
	}
	if strings.Join(paths, ",") != "a.go,m.go,z.go" {
		t.Fatalf("path occurrence order = %v, want sorted", paths)
	}
}

func TestSortPositionsTieBreaker(t *testing.T) {
	end := func(v int) *int { return &v }
	positions := []RenderPosition{
		{PositionKey: "b", LineStart: 10, LineEnd: end(12)},
		{PositionKey: "a", LineStart: 10, LineEnd: end(12)},
		{PositionKey: "c", LineStart: 10},
		{PositionKey: "d", LineStart: 5},
	}
	SortPositions(positions)
	got := []string{positions[0].PositionKey, positions[1].PositionKey, positions[2].PositionKey, positions[3].PositionKey}
	// Absent line_end sorts as line_start (matches SQL NULL-first for the
	// shared line_start/line_end/position_key tie-breaker).
	want := []string{"d", "c", "a", "b"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestFormatResultExactTotalLines(t *testing.T) {
	// Long trailing output after the last marker must count toward total lines.
	longTail := strings.Repeat("line of trailing output\n", 50)
	events := []model.RenderEvent{
		{Type: "TurnBoundary", TurnIndex: 0},
		{Type: "UserPrompt", TurnIndex: 0, Text: "hi"},
		{Type: "TextChunk", TurnIndex: 0, Depth: 0, Text: longTail},
	}
	fr := FormatEventsResultOpts(events, 80, Options{})
	maxMarker := 0
	for _, p := range fr.Positions {
		if p.LineStart > maxMarker {
			maxMarker = p.LineStart
		}
	}
	if fr.TotalLines <= maxMarker+1 {
		t.Fatalf("TotalLines = %d, marker-inferred = %d; tail output must be counted", fr.TotalLines, maxMarker+1)
	}
	// Tracker math: logical lines of the ANSI output (no soft-wrap fixtures at
	// 80 cols with short lines would differ; trailing output ends with \n).
	logical := strings.Count(fr.ANSI, "\n")
	if fr.TotalLines != logical+1 {
		t.Fatalf("TotalLines = %d, ANSI logical lines = %d", fr.TotalLines, logical+1)
	}

	empty := FormatEventsResultOpts(nil, 80, Options{})
	if empty.TotalLines != 1 {
		t.Fatalf("empty session TotalLines = %d, want conventional minimum 1", empty.TotalLines)
	}
}
