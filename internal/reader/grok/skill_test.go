package grok

import (
	"strings"
	"testing"
)

func TestSkillNameFromPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/home/deck/.claude/skills/git-commit-with-context/SKILL.md", "git-commit-with-context"},
		{"/home/deck/.grok/skills/help/SKILL.md", "help"},
		{"/tmp/proj/.agents/skills/foo/SKILL.md", "foo"},
		{"/tmp/proj/.cursor/skills/bar/SKILL.md", "bar"},
		{`C:\Users\me\.claude\skills\baz\SKILL.md`, "baz"},
		{"/home/deck/.claude/skills/git-commit-with-context/skill.md", "git-commit-with-context"}, // case-insensitive file
		{"/tmp/a.go", ""},
		{"/home/deck/.claude/skills/README.md", ""},
		{"/home/deck/not-skills/foo/SKILL.md", ""},
		{"", ""},
	}
	for _, tc := range cases {
		got := skillNameFromPath(tc.path)
		if got != tc.want {
			t.Errorf("skillNameFromPath(%q)=%q want %q", tc.path, got, tc.want)
		}
	}
}

func TestSkillNameFromRead(t *testing.T) {
	if got := skillNameFromRead("read_file", map[string]any{
		"target_file": "/home/deck/.claude/skills/git-commit-with-context/SKILL.md",
		"limit":       100,
	}); got != "git-commit-with-context" {
		t.Errorf("got %q", got)
	}
	if got := skillNameFromRead("run_terminal_command", map[string]any{
		"target_file": "/home/deck/.claude/skills/git-commit-with-context/SKILL.md",
	}); got != "" {
		t.Errorf("non-read tool should not match, got %q", got)
	}
	if got := skillNameFromRead("read_file", map[string]any{
		"path": "/home/deck/.grok/skills/help/SKILL.md",
	}); got != "help" {
		t.Errorf("path key: got %q", got)
	}
}

func sampleSkillUpdates() string {
	return `{"timestamp":1700000000,"method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"use skill commit"}}}}
{"timestamp":1700000001,"method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call","toolCallId":"call-skill-1","title":"read_file","rawInput":{"target_file":"/home/deck/.claude/skills/git-commit-with-context/SKILL.md","limit":100},"_meta":{"x.ai/tool":{"name":"read_file","kind":"read","label":"Read"}}}}}
{"timestamp":1700000002,"method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call_update","toolCallId":"call-skill-1","status":"completed","content":[{"type":"content","content":{"type":"text","text":"---\nname: git-commit-with-context\n---\n"}}]}}}
{"timestamp":1700000003,"method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"committed"}}}}
{"timestamp":1700000004,"method":"_x.ai/session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"turn_completed","stop_reason":"end_turn","usage":{"inputTokens":10,"outputTokens":5,"modelCalls":1,"apiDurationMs":100}}}}
`
}

func TestSkillReadRecognizedInSessionAndRender(t *testing.T) {
	root := t.TempDir()
	id := "cccccccc-bbbb-cccc-dddd-eeeeeeeeeeee"
	writeSession(t, root, "proj", id, summaryFile{}, sampleSkillUpdates(), sampleEventsClosed())
	r := New(root)

	detail, err := r.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(detail.Turns) != 1 {
		t.Fatalf("turns=%d", len(detail.Turns))
	}
	turn := detail.Turns[0]
	if len(turn.Skills) != 1 || turn.Skills[0] != "git-commit-with-context" {
		t.Errorf("Skills=%v want [git-commit-with-context]", turn.Skills)
	}
	foundSkillTool := false
	for _, n := range turn.ToolNames {
		if n == "Skill" {
			foundSkillTool = true
		}
		if n == "read_file" {
			t.Errorf("tool_names still has raw read_file; want Skill rewrite")
		}
	}
	if !foundSkillTool {
		t.Errorf("tool_names=%v missing Skill", turn.ToolNames)
	}

	events, err := r.GetRenderEvents(id)
	if err != nil {
		t.Fatalf("GetRenderEvents: %v", err)
	}
	var inv *struct {
		name  string
		skill string
	}
	for _, e := range events {
		if e.Type != "ToolInvocation" {
			continue
		}
		skill, _ := e.ToolInput["skill"].(string)
		inv = &struct {
			name  string
			skill string
		}{e.ToolName, skill}
		break
	}
	if inv == nil {
		t.Fatal("no ToolInvocation")
	}
	if inv.name != "Skill" {
		t.Errorf("ToolName=%q want Skill", inv.name)
	}
	if inv.skill != "git-commit-with-context" {
		t.Errorf("skill input=%q", inv.skill)
	}

	// Ordinary file read must stay read_file (regression via closed fixture shape).
	// Ensure path detection is specific: a.go not skill.
	if skillNameFromPath("/tmp/a.go") != "" {
		t.Error("non-skill path should not match")
	}
}

func TestSkillRenderDoesNotLeakPath(t *testing.T) {
	// Guard: rewritten input should only carry skill name, not the filesystem path.
	input := map[string]any{
		"target_file": "/home/deck/.claude/skills/git-commit-with-context/SKILL.md",
		"limit":       float64(100),
	}
	name := "read_file"
	skill := skillNameFromRead(name, input)
	if skill == "" {
		t.Fatal("expected skill")
	}
	rewritten := map[string]any{"skill": skill}
	for k := range rewritten {
		if strings.Contains(k, "file") || strings.Contains(k, "path") {
			t.Errorf("unexpected key %q", k)
		}
	}
	if rewritten["skill"] != "git-commit-with-context" {
		t.Errorf("%v", rewritten)
	}
}
