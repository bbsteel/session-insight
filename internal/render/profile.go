package render

import "session-insight/internal/model"

// Profile is a per-agent terminal layout profile. The formatter has exactly
// one rendering code path; a profile only parameterizes it (box charset,
// headers, grouping), so a new agent's native look is a configuration entry,
// not a fork of the renderer. The default profile reproduces the historical
// layout byte-for-byte — agents without a profile are unaffected.
type Profile struct {
	Name string

	// Box-drawing charset for tool/diff boxes.
	BoxTL, BoxTR, BoxBL, BoxBR string
	BoxH, BoxV                 string

	// UserHeader, when non-empty, renders the user prompt as a standalone
	// header line (e.g. "❯ You") followed by the prompt text, instead of the
	// legacy inline "> " prefix.
	UserHeader string

	// AssistantHeader, when true, prefixes each contiguous assistant text
	// block with a "◇ <label>" line. The label comes from the turn's
	// TurnBoundary metadata key "agent_label", falling back to "Agent".
	AssistantHeader bool

	// GroupToolRuns, when true, prefixes each contiguous run of tool events
	// with a "▼ Tools (succeeded/total)" group header.
	GroupToolRuns bool

	// GroupHeaderStats, when true, appends a per-category tool count summary
	// to the group header ("· 4 search · 1 read · 5 shell"), mirroring Claude
	// Code's own collapsed-tools summary line (which is TUI-generated and not
	// present in the session data, so it is recomputed here).
	GroupHeaderStats bool

	// ToolBullet, when true, renders a "• <name>" line above each tool box
	// and promotes a reason/description/title argument into the box header.
	ToolBullet bool

	// ResultBox, when true, renders tool results as a bordered "Output" box
	// with a Completed/Failed footer instead of a bare ✓/✗ line.
	ResultBox bool

	// SubagentBadge, when true, renders subagent_started as a bold "◉ label"
	// badge instead of the legacy plain "@ label" line.
	SubagentBadge bool
}

var defaultProfile = Profile{
	Name:  "default",
	BoxTL: "╔", BoxTR: "╗", BoxBL: "╚", BoxBR: "╝",
	BoxH: "═", BoxV: "║",
}

var chrysProfile = Profile{
	Name:  "chrys",
	BoxTL: "╭", BoxTR: "╮", BoxBL: "╰", BoxBR: "╯",
	BoxH: "─", BoxV: "│",
	UserHeader:      "❯ You",
	AssistantHeader: true,
	GroupToolRuns:   true,
	ToolBullet:      true,
	ResultBox:       true,
	SubagentBadge:   true,
}

// Claude keeps the default box layout untouched; it only gains collapsible
// tool-run groups with a stats header (fold positions then flow to the
// frontend exactly like chrys).
var claudeProfile = Profile{
	Name:  "claude",
	BoxTL: "╔", BoxTR: "╗", BoxBL: "╚", BoxBR: "╝",
	BoxH: "═", BoxV: "║",
	GroupToolRuns:    true,
	GroupHeaderStats: true,
}

var profiles = map[string]*Profile{
	"chrys":  &chrysProfile,
	"claude": &claudeProfile,
}

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
