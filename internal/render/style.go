package render

import (
	"github.com/bbsteel/session-insight/internal/model"
)

// BoxSet describes the box-drawing characters used by a style.
type BoxSet struct {
	TL, TR, BL, BR string
	H, V           string
}

// Palette maps semantic color names to ANSI color slots. The backend emits
// indexed colors (\x1b[38;5;Nm / \x1b[48;5;Nm) so the actual RGB is resolved by
// the frontend xterm theme. A Palette lets different themes or agents use
// different slot assignments without touching the formatter.
type Palette struct {
	Bg            Color
	Error         Color
	Success       Color
	Warning       Color
	Tool          Color
	Skill         Color
	Subagent      Color
	Fg            Color
	Muted         Color
	User          Color
	SuccessBright Color
	ErrorBright   Color
	Banner        Color
	DiffDel       Color
	DiffAdd       Color
}

// DefaultDark is the default dark-mode palette; it mirrors the slot assignment
// in theme.go. For now DefaultLight uses the same indexed slots because the
// frontend remaps the same slots to a light RGB scheme. When per-agent or
// per-variant palettes are introduced, these can diverge.
var DefaultDark = Palette{
	Bg:            ColBg,
	Error:         ColError,
	Success:       ColSuccess,
	Warning:       ColWarning,
	Tool:          ColTool,
	Skill:         ColSkill,
	Subagent:      ColSubagent,
	Fg:            ColFg,
	Muted:         ColMuted,
	User:          ColUser,
	SuccessBright: ColSuccessBright,
	ErrorBright:   ColErrorBright,
	Banner:        ColBanner,
	DiffDel:       ColDiffDel,
	DiffAdd:       ColDiffAdd,
}

// DefaultLight currently aliases DefaultDark. Future light-specific palettes can
// assign different slots when the backend needs to output different colors for
// light mode.
var DefaultLight = DefaultDark

// DefaultPalette is the active palette used by profiles that do not yet
// participate in theme resolution.
var DefaultPalette = DefaultDark

// Theme is a named collection of palettes, indexed by an optional variant key
// (e.g. "dark", "light", "high-contrast"). A theme may have only a Default
// palette, or any number of named variants.
type Theme struct {
	Name     string
	Default  Palette
	Variants map[string]Palette
}

// Palette selects the palette for the given variant, falling back to Default.
func (t Theme) Palette(variant string) Palette {
	if p, ok := t.Variants[variant]; ok {
		return p
	}
	return t.Default
}

// DefaultTheme is the global fallback theme. All profiles currently resolve to
// DefaultTheme; per-agent themes can be added to the theme registry later.
var DefaultTheme = Theme{
	Name:    "default",
	Default: DefaultPalette,
	Variants: map[string]Palette{
		"dark":  DefaultDark,
		"light": DefaultLight,
	},
}

// Style is a complete terminal rendering style for a single agent / mode. It is
// intentionally a flat composition of small strategies and primitive values so
// that new agents can be assembled without modifying the formatter.
type Style struct {
	Box     BoxSet
	Palette Palette

	// Static switches from the original Profile abstraction.
	UserHeader       string
	AssistantHeader  bool
	GroupToolRuns    bool
	GroupHeaderStats bool
	ToolBullet       bool
	ResultBox        bool
	SubagentBadge    bool

	// ResultIndent is extra indentation applied to depth-0 tool results when
	// the style uses per-tool bullets (e.g. grok indents results to line up
	// under the bullet).
	ResultIndent string

	// Behavioral strategies; nil means the default/legacy behavior.
	Bullet     BulletStrategy
	ToolBox    ToolBoxStrategy
	Thought    ThoughtStrategy
	ColorRule  ColorRule
	Preprocess func([]model.RenderEvent)
}

// Profile is a named Style plus the factory metadata needed to resolve it from
// an event stream. It remains the public type returned by profileFor.
type Profile struct {
	Name string
	Style
}

// BulletStrategy renders the compact bullet/header line above a tool invocation.
// When ToolBullet is enabled, each tool is introduced by a foldable header line
// (WriteFoldHeader) or an inline header line (WriteInlineHeader) and edit tools
// may receive an extra edit-specific header (WriteEditHeader).
type BulletStrategy interface {
	Char() string
	ColorForTool(p *Profile, toolName string, failed bool) Color

	// WriteFoldHeader writes the visible part of the bullet line that stays
	// visible when the tool body is collapsed. It returns the UTF-16 length of
	// the bullet+name run so the formatter can compute badge_offset for the
	// frontend fold badge.
	WriteFoldHeader(p *Profile, tb *trackingBuilder, toolName string, failed bool,
		purpose string, input map[string]any) int

	// WriteInlineHeader writes the bullet line for tools that are not part of
	// a per-tool fold (e.g. nested or edit tools).
	WriteInlineHeader(p *Profile, tb *trackingBuilder, toolName string, failed bool,
		purpose string, input map[string]any)

	// WriteEditHeader writes an optional header line for an edit tool (e.g.
	// grok's "◆ SearchReplace"). It should end with a newline if it writes
	// anything.
	WriteEditHeader(p *Profile, tb *trackingBuilder, prefix, ts, toolName string)
}

// ToolBoxStrategy decides the prefix/indent and top-border header of the tool
// input box. Different profiles vary in how much metadata they promote into the
// box header and how the box is indented under the bullet line.
type ToolBoxStrategy interface {
	BoxPrefix(p *Profile, prefix string) string
	BuildHeader(p *Profile, input map[string]any, toolName, promotedPurpose string,
		durationMs int64, ts string) (header string, suppress bool)
}

// ThoughtFold records the line extent of a thought fold, analogous to the tool
// fold state used by the formatter.
type ThoughtFold struct {
	TurnIndex     int
	Key           string
	HeaderDisplay int
	HeaderLogical int
	BodyDisplay   int
	BodyLogical   int
}

// ThoughtStrategy renders the agent's reasoning/thinking block. The default
// behavior (nil strategy) writes the raw thinking text; grok uses a compact
// foldable header with a sidebar.
type ThoughtStrategy interface {
	Start(p *Profile, tb *trackingBuilder, evt model.RenderEvent, prefix, ts string) *ThoughtFold
	Chunk(p *Profile, tb *trackingBuilder, evt model.RenderEvent, prefix string)
	End(p *Profile, tb *trackingBuilder, fold *ThoughtFold) *RenderPosition
}

// ColorRule allows a style to override the border color of a tool input box
// based on the tool name and outcome. It is consulted after the base category
// color (tool/subagent/skill) has been computed.
type ColorRule interface {
	ColorFor(p *Profile, toolName string, failed bool) (Color, bool)
}

// categoryColor returns the base border color for a tool based on its category.
func categoryColor(p *Profile, toolName string) Color {
	if toolName == "Agent" || toolName == "Task" {
		return p.Palette.Subagent
	}
	if toolName == "Skill" {
		return p.Palette.Skill
	}
	return p.Palette.Tool
}
