package render

// defaultProfile preserves the historical ANSI layout for agents without a
// dedicated style (e.g. codex, copilot, opencode).
var defaultProfile = Profile{
	Name: "default",
	Style: Style{
		Box:     BoxSet{TL: "╔", TR: "╗", BL: "╚", BR: "╝", H: "═", V: "║"},
		Palette: DefaultPalette,
		ToolBox: standardToolBox{},
	},
}

// chrysProfile matches the Chrys Code TUI: rounded boxes, "❯ You" user header,
// "◇" assistant header, grouped tools with per-tool bullets, and result boxes.
var chrysProfile = Profile{
	Name: "chrys",
	Style: Style{
		Box:             BoxSet{TL: "╭", TR: "╮", BL: "╰", BR: "╯", H: "─", V: "│"},
		Palette:         DefaultPalette,
		UserHeader:      "❯ You",
		AssistantHeader: true,
		GroupToolRuns:   true,
		ToolBullet:      true,
		ResultBox:       true,
		SubagentBadge:   true,
		Bullet:          standardBullet{char: "•"},
		ToolBox:         chrysToolBox{},
	},
}

// claudeProfile keeps the default box layout but adds a grouped tool-run header
// with category stats, mirroring Claude Code's own collapsed-tools summary.
// Folded tools move the "Tool: …" header out of the box top onto a dedicated
// "▼ Tool: …" fold line (ToolFoldHeader) so each tool collapses independently.
var claudeProfile = Profile{
	Name: "claude",
	Style: Style{
		Box:              BoxSet{TL: "╔", TR: "╗", BL: "╚", BR: "╝", H: "═", V: "║"},
		Palette:          DefaultPalette,
		GroupToolRuns:    true,
		GroupHeaderStats: true,
		ToolFoldHeader:   true,
		ToolBox:          standardToolBox{},
	},
}

// grokProfile matches Grok Build's native terminal look: rounded boxes, "◆"
// bullets, per-status Run colors, Skill accent, compact thought folds, and a
// two-space indent under the bullet.
var grokProfile = Profile{
	Name: "grok",
	Style: Style{
		Box:          BoxSet{TL: "╭", TR: "╮", BL: "╰", BR: "╯", H: "─", V: "│"},
		Palette:      DefaultPalette,
		ToolBullet:   true,
		ResultBox:    true,
		ResultIndent: "  ",
		Bullet:       grokBullet{},
		ToolBox:      grokToolBox{},
		Thought:      grokThought{},
		ColorRule:    grokColorRule{},
		Preprocess:   computeGrokThoughtSummaries,
	},
}

// profiles maps known agent types to their style. The default profile is used
// for any agent not listed here.
var profiles = map[string]*Profile{
	"chrys":  &chrysProfile,
	"claude": &claudeProfile,
	"grok":   &grokProfile,
}
