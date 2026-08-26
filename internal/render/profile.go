package render

import "github.com/bbsteel/session-insight/internal/model"

// profileFor resolves the layout profile from the event stream's agent type
// (first non-empty AgentType wins; spliced sub-agent events inherit the
// parent's agent type in every reader, so the whole stream is homogeneous).
//
// This remains the production selector while reader.UsesDeclarationResolver
// is false. Slice F flips that gate per Agent after evidence lands; until
// then Claude, Chrys, and Grok keep these legacy profiles.
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
