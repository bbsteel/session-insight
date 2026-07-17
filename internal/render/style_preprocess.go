package render

import (
	"fmt"

	"github.com/bbsteel/session-insight/internal/model"
)

// computeGrokThoughtSummaries post-processes the event list for grok to set a
// compact "Thought for Xs" summary on ThinkingStart events (duration from start
// to corresponding ThinkingEnd in same turn). This lets the terminal view show
// only the ◆ header line while the detailed thought chunks live inside the
// fold.
func computeGrokThoughtSummaries(events []model.RenderEvent) {
	for i := range events {
		e := &events[i]
		if e.Type != "ThinkingStart" || e.AgentType != "grok" {
			continue
		}
		startTs := e.Timestamp
		turn := e.TurnIndex
		for j := i + 1; j < len(events); j++ {
			if events[j].TurnIndex != turn {
				break
			}
			if events[j].Type == "ThinkingEnd" {
				if !events[j].Timestamp.IsZero() && !startTs.IsZero() {
					d := events[j].Timestamp.Sub(startTs).Seconds()
					if d < 0 {
						d = 0
					}
					e.Text = fmt.Sprintf("Thought for %.1fs", d)
				}
				break
			}
			if events[j].Type == "ThinkingStart" {
				break
			}
		}
		if e.Text == "" {
			e.Text = "Thought"
		}
	}
}
