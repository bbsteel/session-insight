package shared

import (
	"math"
	"strings"

	"github.com/bbsteel/session-insight/internal/model"
)

// continuationNudges are short user messages whose only content is "keep
// going" — the user pushing an agent that stopped at an intermediate result.
// Matched case-insensitively after stripping trailing punctuation, either
// exactly or as a "word + space" prefix (e.g. "继续 刚才的任务").
var continuationNudges = []string{
	"继续", "接着", "接着做", "继续做", "继续吧", "继续干", "好的继续", "让我们继续", "好", "好的", "ok好",
	"ok", "go on", "go ahead", "continue", "proceed", "keep going", "next", "done", "sure",
}

// isContinuationNudge reports whether a user message is a continuation nudge.
func isContinuationNudge(msg string) bool {
	trimmed := strings.TrimSpace(msg)
	if trimmed == "" || len([]rune(trimmed)) > 25 {
		return false
	}
	lower := strings.TrimRight(strings.ToLower(trimmed), "。！!.,~～ \t")
	if lower == "" {
		return false
	}
	for _, w := range continuationNudges {
		if lower == w || strings.HasPrefix(lower, w+" ") {
			return true
		}
	}
	return false
}

// RunAnomalyDetection performs mean+3σ anomaly detection on turns.
// First pass: marks tool_failure for turns with errors, continuation_nudge
// for turns the user had to push forward, and collects durations.
// Second pass: marks duration_spike for turns exceeding mean+3σ threshold (>30s floor).
func RunAnomalyDetection(turns []model.TurnVM) model.AnomalySummary {
	var durations []int64
	summary := model.AnomalySummary{}
	for i := range turns {
		t := &turns[i]
		if t.ErrorCount > 0 {
			t.Anomalies = append(t.Anomalies, "tool_failure")
			summary.ToolFailures++
		}
		// A nudge presupposes a previous assistant output to push on, so the
		// first turn never qualifies.
		if i > 0 && isContinuationNudge(t.UserMessage) {
			t.Anomalies = append(t.Anomalies, "continuation_nudge")
			summary.NudgeCount++
		}
		if t.DurationMs > 0 {
			durations = append(durations, t.DurationMs)
		}
	}

	if len(durations) < 2 {
		summary.TotalAnomalies = summary.ToolFailures + summary.NudgeCount
		return summary
	}

	var sum int64
	for _, d := range durations {
		sum += d
	}
	mean := float64(sum) / float64(len(durations))
	var variance float64
	for _, d := range durations {
		variance += (float64(d) - mean) * (float64(d) - mean)
	}
	stdDev := math.Sqrt(variance / float64(len(durations)))
	threshold := mean + 3*stdDev

	for i := range turns {
		if float64(turns[i].DurationMs) > threshold && turns[i].DurationMs > 30000 {
			turns[i].Anomalies = append(turns[i].Anomalies, "duration_spike")
			summary.DurationSpikes++
		}
	}
	summary.TotalAnomalies = summary.ToolFailures + summary.DurationSpikes + summary.NudgeCount
	return summary
}
