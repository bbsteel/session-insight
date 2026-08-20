package main

// This file declares the fixed terminal-reference screenshot checklist from
// the "Agent native terminal reference input" design. Every item is allowed to
// be missing: absence means "no visual evidence yet", never "unsupported".

// Slot is one logical screenshot file of a checklist item. Foldable scenes
// have extra slots for the toggled states; the tool owns the `-toggled`
// naming so humans never type the suffixes.
type Slot struct {
	// LogicalName is the canonical file base name, e.g. "04-thinking" or
	// "04-thinking-toggled". The stored file adds a real image extension.
	LogicalName string `json:"logical_name"`
	Label       string `json:"label"`
	Hint        string `json:"hint"`
}

// ChecklistItem is one row of the fixed screenshot checklist.
type ChecklistItem struct {
	ID          string `json:"id"` // e.g. "04-thinking"
	Title       string `json:"title"`
	Goal        string `json:"goal"`        // target picture
	Extractable string `json:"extractable"` // what later development may read from it
	Slots       []Slot `json:"slots"`
	// Features maps this item to presentation feature IDs from the v0.8.0
	// terminal polymorphism design. Empty means context-only evidence.
	Features []string `json:"features"`
	// CandidateNote describes the structured candidate condition. Empty means
	// the tool never proposes a machine candidate for this item.
	CandidateNote string `json:"candidate_note"`
}

func defaultSlot(id string) Slot {
	return Slot{LogicalName: id, Label: "Default state", Hint: "Capture the scene exactly as the native CLI shows it before touching any fold."}
}

func toggledSlot(id string) Slot {
	return Slot{LogicalName: id + "-toggled", Label: "After one toggle", Hint: "Toggle the same block once, then capture another complete full-screen shot."}
}

// checklist is the complete logical file list. Order is display order.
var checklist = []ChecklistItem{
	{
		ID:          "00-version",
		Title:       "Agent version",
		Goal:        "A native terminal view that confirms the Agent product version.",
		Extractable: "Version context observed with the capture. A version change alone never invalidates other images.",
		Slots:       []Slot{defaultSlot("00-version")},
	},
	{
		ID:          "01-session-overview",
		Title:       "Session overview",
		Goal:        "The representative full view after resuming a session: header, body, status or prompt areas.",
		Extractable: "Overall information hierarchy, density, margins, dominant visual language, default states.",
		Slots:       []Slot{defaultSlot("01-session-overview")},
		Features:    []string{"density", "turn_boundary"},
	},
	{
		ID:            "02-user-prompt",
		Title:         "User prompt",
		Goal:          "User input, preferably multi-line or naturally wrapped.",
		Extractable:   "Prompt marker, user badge, background, indentation, continuation and separators.",
		Slots:         []Slot{defaultSlot("02-user-prompt")},
		Features:      []string{"user_prompt"},
		CandidateNote: "UserPrompt event; multi-line text preferred.",
	},
	{
		ID:            "03-assistant-response",
		Title:         "Assistant response",
		Goal:          "A normal assistant reply, preferably with paragraphs, lists, code or links.",
		Extractable:   "Assistant identity, Markdown hierarchy, body density, code blocks and paragraph spacing.",
		Slots:         []Slot{defaultSlot("03-assistant-response")},
		Features:      []string{"assistant_text"},
		CandidateNote: "Assistant text chunk; fenced code preferred.",
	},
	{
		ID:            "04-thinking",
		Title:         "Thinking",
		Goal:          "The native default state of thinking / reasoning.",
		Extractable:   "Thought marker, summary, body, side line, colors and the default expanded state.",
		Slots:         []Slot{defaultSlot("04-thinking"), toggledSlot("04-thinking")},
		Features:      []string{"thinking"},
		CandidateNote: "ThinkingStart / ThinkingChunk event.",
	},
	{
		ID:            "05-tool-invocation",
		Title:         "Tool invocation",
		Goal:          "One representative complete tool call with parameters and result context.",
		Extractable:   "Tool name, parameter summary, frame, bullet, indentation and single-tool structure.",
		Slots:         []Slot{defaultSlot("05-tool-invocation"), toggledSlot("05-tool-invocation")},
		Features:      []string{"tool_invocation"},
		CandidateNote: "ToolInvocation event, preferably paired with its result.",
	},
	{
		ID:            "06-tool-running",
		Title:         "Tool running",
		Goal:          "The visible state while a tool is executing.",
		Extractable:   "Running marker, static spinner shape, status copy and colors; never used to simulate animation timing.",
		Slots:         []Slot{defaultSlot("06-tool-running")},
		Features:      []string{"tool_invocation"},
		CandidateNote: "Live session with an unpaired ToolInvocation.",
	},
	{
		ID:            "07-tool-success",
		Title:         "Tool success",
		Goal:          "A successful tool result.",
		Extractable:   "Success glyph, footer, duration, exit status and output boundaries.",
		Slots:         []Slot{defaultSlot("07-tool-success")},
		Features:      []string{"tool_result_success"},
		CandidateNote: "ToolResult with exact success status.",
	},
	{
		ID:            "08-tool-failure",
		Title:         "Tool failure",
		Goal:          "A failed tool, stderr output or a non-zero exit.",
		Extractable:   "Failure glyph, error color, body and failure footer.",
		Slots:         []Slot{defaultSlot("08-tool-failure")},
		Features:      []string{"tool_result_failure"},
		CandidateNote: "ToolResult with ExitCode != 0, ErrorKind set, or structured stderr.",
	},
	{
		ID:            "09-tool-timeout",
		Title:         "Tool timeout",
		Goal:          "An explicit tool timeout result.",
		Extractable:   "How timeout differs visually from a plain failure.",
		Slots:         []Slot{defaultSlot("09-tool-timeout")},
		Features:      []string{"tool_result_failure"},
		CandidateNote: "ToolResult.TimedOut == true.",
	},
	{
		ID:            "10-tool-rejected",
		Title:         "Tool rejected",
		Goal:          "A tool rejected by the user, a hook or a permission policy.",
		Extractable:   "How rejection differs visually from failure and timeout.",
		Slots:         []Slot{defaultSlot("10-tool-rejected")},
		Features:      []string{"tool_result_failure"},
		CandidateNote: "ToolResult.Rejected == true.",
	},
	{
		ID:            "11-file-change",
		Title:         "File change",
		Goal:          "The most representative file creation, modification, deletion or diff.",
		Extractable:   "Path, hunks, +/- markers, background colors, gutter and the change summary.",
		Slots:         []Slot{defaultSlot("11-file-change"), toggledSlot("11-file-change")},
		Features:      []string{"file_change"},
		CandidateNote: "ToolInvocation recognized as a file-edit call.",
	},
	{
		ID:            "12-subagent",
		Title:         "Subagent",
		Goal:          "A child agent / delegation run or result.",
		Extractable:   "Parent-child hierarchy, task summary, status, result and the collaboration structure.",
		Slots:         []Slot{defaultSlot("12-subagent"), toggledSlot("12-subagent")},
		Features:      []string{"subagent"},
		CandidateNote: "Collaboration invocation or an exact parent-child relation.",
	},
	{
		ID:            "13-context-boundary",
		Title:         "Context boundary",
		Goal:          "Compaction, rollback, rewind or another context boundary.",
		Extractable:   "Separator, history hint, boundary title and rollback/compaction treatment.",
		Slots:         []Slot{defaultSlot("13-context-boundary"), toggledSlot("13-context-boundary")},
		Features:      []string{"context_boundary"},
		CandidateNote: "Compaction / rollback / rewind normalized events.",
	},
	{
		ID:            "14-permission-request",
		Title:         "Permission request",
		Goal:          "A native CLI permission request.",
		Extractable:   "Options, focus, danger hints and the default selection. Undecided outcomes are never inferred from the image.",
		Slots:         []Slot{defaultSlot("14-permission-request")},
		CandidateNote: "Explicit permission-request event or subtype.",
	},
	{
		ID:            "15-long-output",
		Title:         "Long output",
		Goal:          "Long output, soft wrapping, native truncation or elision.",
		Extractable:   "Continuation lines, indentation, truncation summary, expand entry and output density.",
		Slots:         []Slot{defaultSlot("15-long-output"), toggledSlot("15-long-output")},
		Features:      []string{"long_output"},
		CandidateNote: "Output above a stable threshold or an exact truncation marker.",
	},
	{
		ID:            "16-live-status",
		Title:         "Live status",
		Goal:          "Session-level state while the Agent is thinking, waiting for a tool or waiting for the user.",
		Extractable:   "Session-level live hint; never confused with a single tool's running state.",
		Slots:         []Slot{defaultSlot("16-live-status")},
		Features:      []string{"live_status"},
		CandidateNote: "Exact session live state from the adapter.",
	},
	{
		ID:            "17-session-completed",
		Title:         "Session completed",
		Goal:          "A session or task that finished naturally.",
		Extractable:   "Completion marker, final prompt, elapsed time or statistics.",
		Slots:         []Slot{defaultSlot("17-session-completed")},
		CandidateNote: "Non-live finished session (low confidence suggestion).",
	},
	{
		ID:            "18-session-interrupted",
		Title:         "Session interrupted",
		Goal:          "A user interruption, abnormal termination or unfinished state.",
		Extractable:   "How interrupted differs visually from completed / failed.",
		Slots:         []Slot{defaultSlot("18-session-interrupted")},
		CandidateNote: "Explicit interrupted marker.",
	},
	{
		ID:          "19-agent-specific",
		Title:       "Agent-specific scene",
		Goal:        "The most recognizable scene of this Agent that does not fit the shared checklist.",
		Extractable: "Input for judging whether a new shared primitive is needed; never a direct Agent special-case.",
		Slots:       []Slot{defaultSlot("19-agent-specific")},
	},
	{
		ID:            "20-tool-group",
		Title:         "Tool group",
		Goal:          "The default state of a native group of consecutive tools.",
		Extractable:   "Group title, count, summary, group boundary and the default collapsed state.",
		Slots:         []Slot{defaultSlot("20-tool-group"), toggledSlot("20-tool-group")},
		Features:      []string{"tool_invocation"},
		CandidateNote: "Three or more consecutive tool invocations in one turn.",
	},
	{
		ID:          "21-nested-fold",
		Title:       "Nested fold",
		Goal:        "The default state when parent and child foldable blocks exist at the same time.",
		Extractable: "Nesting levels, parent/child summaries and fold boundaries.",
		Slots: []Slot{
			defaultSlot("21-nested-fold"),
			{LogicalName: "21-nested-fold-inner-toggled", Label: "Inner toggled", Hint: "From the default state, toggle only the inner block; keep the outer at default."},
			{LogicalName: "21-nested-fold-outer-toggled", Label: "Outer toggled", Hint: "Start over from the default state and toggle only the outer block."},
		},
		Features:      []string{"tool_invocation", "subagent"},
		CandidateNote: "Tool activity nested inside a subagent block.",
	},
}

// checklistItemByID indexes the fixed checklist.
var checklistItemByID = func() map[string]*ChecklistItem {
	m := make(map[string]*ChecklistItem, len(checklist))
	for i := range checklist {
		m[checklist[i].ID] = &checklist[i]
	}
	return m
}()

// knownLogicalNames contains every canonical logical file base name,
// including the fold variants.
var knownLogicalNames = func() map[string]bool {
	m := map[string]bool{}
	for _, item := range checklist {
		for _, slot := range item.Slots {
			m[slot.LogicalName] = true
		}
	}
	return m
}()

// logicalNameItemID maps a logical file base name back to its checklist item.
var logicalNameItemID = func() map[string]string {
	m := map[string]string{}
	for _, item := range checklist {
		for _, slot := range item.Slots {
			m[slot.LogicalName] = item.ID
		}
	}
	return m
}()

// itemFeatures returns the presentation features a logical file feeds, for
// work-order generation.
func itemFeatures(logicalName string) []string {
	itemID, ok := logicalNameItemID[logicalName]
	if !ok {
		return nil
	}
	return checklistItemByID[itemID].Features
}
