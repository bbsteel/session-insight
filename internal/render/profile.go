package render

import "github.com/bbsteel/session-insight/internal/model"

// profileFor resolves the layout profile from the event stream's agent type
// (first non-empty AgentType wins; spliced sub-agent events inherit the
// parent's agent type in every reader, so the whole stream is homogeneous).
func profileFor(events []model.RenderEvent) *Profile {
	for _, e := range events {
		if e.AgentType != "" {
			if p, ok := profiles[e.AgentType]; ok {
				return p
			}
			return &defaultProfile
		}
	}
	return &defaultProfile
}
