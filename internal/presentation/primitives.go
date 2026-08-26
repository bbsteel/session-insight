package presentation

// PrimitiveID names a registered layout behavior. Compiler lookup is by this
// ID, never by Agent type.
type PrimitiveID string

const (
	PrimitiveNeutralBlock     PrimitiveID = "neutral_block"
	PrimitivePromptPrefixed   PrimitiveID = "prompt_prefixed"
	PrimitiveAssistantLabeled PrimitiveID = "assistant_labeled"
	PrimitiveToolBoxed        PrimitiveID = "tool_boxed"
	PrimitiveToolBulleted     PrimitiveID = "tool_bulleted"
	PrimitiveToolGroupSummary PrimitiveID = "tool_group_summary"
	PrimitiveResultBoxed      PrimitiveID = "result_boxed"
	PrimitiveThinkingSidebar  PrimitiveID = "thinking_sidebar"
	PrimitiveDiffGutter       PrimitiveID = "diff_gutter"
	PrimitiveSubagentBranch   PrimitiveID = "subagent_branch"
	PrimitiveBoundaryDivider  PrimitiveID = "boundary_divider"
)

// KnownPrimitiveIDs returns the registered primitive catalog in stable order.
func KnownPrimitiveIDs() []PrimitiveID {
	return []PrimitiveID{
		PrimitiveNeutralBlock,
		PrimitivePromptPrefixed,
		PrimitiveAssistantLabeled,
		PrimitiveToolBoxed,
		PrimitiveToolBulleted,
		PrimitiveToolGroupSummary,
		PrimitiveResultBoxed,
		PrimitiveThinkingSidebar,
		PrimitiveDiffGutter,
		PrimitiveSubagentBranch,
		PrimitiveBoundaryDivider,
	}
}

// IsKnownPrimitive reports whether id is a registered primitive.
func IsKnownPrimitive(id PrimitiveID) bool {
	if id == "" {
		return false
	}
	for _, known := range KnownPrimitiveIDs() {
		if known == id {
			return true
		}
	}
	return false
}
