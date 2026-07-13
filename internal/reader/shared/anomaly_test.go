package shared

import (
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestIsContinuationNudge(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"继续", true},
		{"继续。", true},
		{"继续吧~", true},
		{"好的继续", true},
		{"OK", true},
		{"ok!", true},
		{"go on", true},
		{"Continue", true},
		{"继续 刚才的任务", true},
		{"好的", true},
		{"", false},
		{"。。。", false},
		{"帮我把测试跑一遍", false},
		{"继续深入分析这个模块的性能瓶颈和内存占用", false}, // >25 runes 不算,即使以推进词开头
		{"okay so here is the plan", false},
	}
	for _, c := range cases {
		if got := isContinuationNudge(c.msg); got != c.want {
			t.Errorf("isContinuationNudge(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

func TestRunAnomalyDetectionNudges(t *testing.T) {
	turns := []model.TurnVM{
		{TurnIndex: 0, UserMessage: "继续"}, // 首 turn 不判定
		{TurnIndex: 1, UserMessage: "帮我实现功能 X", ErrorCount: 1},
		{TurnIndex: 2, UserMessage: "继续"},
		{TurnIndex: 3, UserMessage: "ok"},
	}
	summary := RunAnomalyDetection(turns)

	if summary.NudgeCount != 2 {
		t.Errorf("NudgeCount = %d, want 2", summary.NudgeCount)
	}
	if summary.TotalAnomalies != 3 { // 1 tool_failure + 2 nudges
		t.Errorf("TotalAnomalies = %d, want 3", summary.TotalAnomalies)
	}
	if len(turns[0].Anomalies) != 0 {
		t.Errorf("first turn must not be marked as nudge, got %v", turns[0].Anomalies)
	}
	for _, i := range []int{2, 3} {
		found := false
		for _, a := range turns[i].Anomalies {
			if a == "continuation_nudge" {
				found = true
			}
		}
		if !found {
			t.Errorf("turn %d missing continuation_nudge anomaly: %v", i, turns[i].Anomalies)
		}
	}
}
