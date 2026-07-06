package model

import "strings"

// IsEditTool reports whether a tool name represents a file-edit operation
// across all supported agents.
func IsEditTool(name string) bool {
	switch name {
	case "Edit", "str_replace_editor", // Claude
		"edit",        // OpenCode
		"edit_file",   // Chrys
		"apply_patch": // Codex, Copilot
		return true
	}
	return false
}

// ExtractEditCalls converts a ToolInvocation RenderEvent into one or more
// EditCall structs, normalising across all agents.
// Claude / OpenCode use snake_case file_path/old_string/new_string.
// Codex / Copilot use a raw patch string that must be parsed.
func ExtractEditCalls(evt RenderEvent) []EditCall {
	if !IsEditTool(evt.ToolName) {
		return nil
	}
	if evt.ToolName == "apply_patch" {
		return parseApplyPatch(evt.TurnIndex, evt.ToolInput)
	}
	// Claude Edit / str_replace_editor / OpenCode edit
	call := EditCall{TurnIndex: evt.TurnIndex}
	if v, ok := evt.ToolInput["file_path"].(string); ok {
		call.FilePath = v
	}
	if v, ok := evt.ToolInput["old_string"].(string); ok {
		call.OldString = v
	}
	if v, ok := evt.ToolInput["new_string"].(string); ok {
		call.NewString = v
	}
	if v, ok := evt.ToolInput["replace_all"].(bool); ok {
		call.ReplaceAll = v
	}
	if call.FilePath == "" && call.OldString == "" && call.NewString == "" {
		return nil
	}
	return []EditCall{call}
}

// parseApplyPatch parses a Codex / Copilot apply_patch tool input.
//
// Patch format:
//
//	*** Begin Patch
//	*** Update File: path/to/file
//	@@ <range>
//	-removed line
//	+added line
//	 context line
//	*** End Patch
//
// Multiple Update File sections produce multiple EditCalls.
func parseApplyPatch(turnIndex int, input map[string]any) []EditCall {
	var patchStr string
	for _, key := range []string{"args", "input", "patch"} {
		if v, ok := input[key].(string); ok && v != "" {
			patchStr = v
			break
		}
	}
	if patchStr == "" {
		return nil
	}

	type hunk struct {
		old []string
		new []string
	}

	var calls []EditCall
	var curFile string
	var hunks []hunk
	var cur *hunk
	inPatch := false

	flush := func() {
		if curFile == "" || len(hunks) == 0 {
			return
		}
		var oldParts, newParts []string
		for _, h := range hunks {
			oldParts = append(oldParts, h.old...)
			newParts = append(newParts, h.new...)
		}
		calls = append(calls, EditCall{
			TurnIndex: turnIndex,
			FilePath:  curFile,
			OldString: strings.Join(oldParts, "\n"),
			NewString: strings.Join(newParts, "\n"),
		})
		curFile = ""
		hunks = nil
		cur = nil
	}

	for _, line := range strings.Split(patchStr, "\n") {
		switch {
		case line == "*** Begin Patch":
			inPatch = true
		case line == "*** End Patch":
			flush()
			inPatch = false
		case !inPatch:
			// skip
		case strings.HasPrefix(line, "*** Update File: "):
			flush()
			curFile = strings.TrimPrefix(line, "*** Update File: ")
			// Update File sections use @@ hunk markers; don't pre-init.
		case strings.HasPrefix(line, "*** Add File: "):
			flush()
			curFile = strings.TrimPrefix(line, "*** Add File: ")
			// Add File sections emit bare +lines with no @@ header.
			h := hunk{}
			hunks = append(hunks, h)
			cur = &hunks[len(hunks)-1]
		case strings.HasPrefix(line, "*** Delete File: "):
			flush()
			curFile = strings.TrimPrefix(line, "*** Delete File: ")
			// Delete File sections emit bare context/-lines with no @@ header.
			h := hunk{}
			hunks = append(hunks, h)
			cur = &hunks[len(hunks)-1]
		case strings.HasPrefix(line, "*** Move to: "):
			curFile = strings.TrimPrefix(line, "*** Move to: ")
		case strings.HasPrefix(line, "@@"):
			h := hunk{}
			hunks = append(hunks, h)
			cur = &hunks[len(hunks)-1]
		case cur == nil:
			// before first @@, ignore
		case strings.HasPrefix(line, "-"):
			cur.old = append(cur.old, line[1:])
		case strings.HasPrefix(line, "+"):
			cur.new = append(cur.new, line[1:])
		default:
			// context line (space prefix or bare)
			ctx := strings.TrimPrefix(line, " ")
			cur.old = append(cur.old, ctx)
			cur.new = append(cur.new, ctx)
		}
	}
	flush()
	return calls
}
