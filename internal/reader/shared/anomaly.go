package shared

import (
	"math"

	"session-insight/internal/model"
)

// RunAnomalyDetection performs mean+3σ anomaly detection on turns.
// First pass: marks tool_failure for turns with errors and collects durations.
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
		if t.DurationMs > 0 {
			durations = append(durations, t.DurationMs)
		}
	}

	if len(durations) < 2 {
		summary.TotalAnomalies = summary.ToolFailures
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
	summary.TotalAnomalies = summary.ToolFailures + summary.DurationSpikes
	return summary
}
