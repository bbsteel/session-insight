package llm

import (
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

func makeDetail(turns int, userMsgLen int) *model.SessionDetail {
	d := &model.SessionDetail{}
	d.AgentType = "claude"
	d.Project = "demo"
	d.Repository = "github.com/x/demo"
	d.Branch = "main"
	for i := 0; i < turns; i++ {
		d.Turns = append(d.Turns, model.TurnVM{
			TurnIndex:        i,
			UserMessage:      strings.Repeat("用", userMsgLen),
			AssistantMessage: strings.Repeat("答", userMsgLen),
			ToolNames:        []string{"Bash"},
		})
	}
	return d
}

func TestBuildSessionContextSmallSessionKeepsEverything(t *testing.T) {
	ctx := BuildSessionContext(makeDetail(3, 100))
	if strings.Contains(ctx, "已省略") {
		t.Fatalf("small session should not be elided:\n%s", ctx)
	}
	for _, want := range []string{"Turn 0", "Turn 2", "github.com/x/demo @main"} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("context missing %q", want)
		}
	}
}

func TestBuildSessionContextElidesMiddleWithinBudget(t *testing.T) {
	ctx := BuildSessionContext(makeDetail(200, 1900))
	if got := len([]rune(ctx)); got > contextBudgetRunes {
		t.Fatalf("context %d runes exceeds budget %d", got, contextBudgetRunes)
	}
	if !strings.Contains(ctx, "已省略") {
		t.Fatal("oversized session should carry an elision marker")
	}
	// Head and tail turns must survive.
	for _, want := range []string{"Turn 0", "Turn 199"} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("context missing %q", want)
		}
	}
}

func TestSanitizeTitle(t *testing.T) {
	cases := map[string]string{
		"「修复登录超时」":               "修复登录超时",
		"\"Fix login bug.\"":     "Fix login bug",
		"修复登录超时。\n多余的解释":         "修复登录超时",
		"  修复登录超时!  ":            "修复登录超时",
		strings.Repeat("长", 100): strings.Repeat("长", 40),
	}
	for in, want := range cases {
		if got := SanitizeTitle(in); got != want {
			t.Errorf("SanitizeTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
