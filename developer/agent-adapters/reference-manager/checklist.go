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
	LabelZH     string `json:"label_zh"`
	Hint        string `json:"hint"`
	HintZH      string `json:"hint_zh"`
}

// ChecklistItem is one row of the fixed screenshot checklist.
type ChecklistItem struct {
	ID            string `json:"id"` // e.g. "04-thinking"
	Title         string `json:"title"`
	TitleZH       string `json:"title_zh"`
	Goal          string `json:"goal"`
	GoalZH        string `json:"goal_zh"`
	Extractable   string `json:"extractable"`
	ExtractableZH string `json:"extractable_zh"`
	Slots         []Slot `json:"slots"`
	// Features maps this item to presentation feature IDs from the v0.8.0
	// terminal polymorphism design. Empty means context-only evidence.
	Features []string `json:"features"`
	// CandidateNote describes the structured candidate condition. Empty means
	// the tool never proposes a machine candidate for this item.
	CandidateNote   string `json:"candidate_note"`
	CandidateNoteZH string `json:"candidate_note_zh"`
}

func defaultSlot(id string) Slot {
	return Slot{
		LogicalName: id,
		Label:       "Default state",
		LabelZH:     "默认状态",
		Hint:        "Capture the scene exactly as the native CLI shows it before touching any fold.",
		HintZH:      "按原生 CLI 默认画面截取完整全屏，先不要操作该折叠项。",
	}
}

func toggledSlot(id string) Slot {
	return Slot{
		LogicalName: id + "-toggled",
		Label:       "After one toggle",
		LabelZH:     "切换一次后",
		Hint:        "Toggle the same block once, then capture another complete full-screen shot.",
		HintZH:      "只对同一区块切换一次，再截一张完整全屏。",
	}
}

// checklist is the complete logical file list. Order is display order.
var checklist = []ChecklistItem{
	{
		ID:            "00-version",
		Title:         "Agent version",
		TitleZH:       "Agent 版本",
		Goal:          "A native terminal view that confirms the Agent product version.",
		GoalZH:        "原生终端中可确认 Agent 产品版本的画面。",
		Extractable:   "Version context observed with the capture. A version change alone never invalidates other images.",
		ExtractableZH: "记录截图观察到的版本上下文；版本变化本身不使其他图片失效。",
		Slots:         []Slot{defaultSlot("00-version")},
	},
	{
		ID:            "01-session-overview",
		Title:         "Session overview",
		TitleZH:       "会话总览",
		Goal:          "The representative full view after resuming a session: header, body, status or prompt areas.",
		GoalZH:        "恢复会话后的完整代表性画面，包含主要头部、正文、状态区或提示区。",
		Extractable:   "Overall information hierarchy, density, margins, dominant visual language, default states.",
		ExtractableZH: "整体信息层级、密度、边距、主要视觉语言和默认状态。",
		Slots:         []Slot{defaultSlot("01-session-overview")},
		Features:      []string{"turn_boundary"},
	},
	{
		ID:              "02-user-prompt",
		Title:           "User prompt",
		TitleZH:         "用户输入",
		Goal:            "User input, preferably multi-line or naturally wrapped.",
		GoalZH:          "用户输入，优先包含多行或自然换行。",
		Extractable:     "Prompt marker, user badge, background, indentation, continuation and separators.",
		ExtractableZH:   "提示符、用户标记、背景、缩进、续行和分隔方式。",
		Slots:           []Slot{defaultSlot("02-user-prompt")},
		Features:        []string{"user_prompt"},
		CandidateNote:   "UserPrompt event; multi-line text preferred.",
		CandidateNoteZH: "UserPrompt 事件；优先多行文本。",
	},
	{
		ID:              "03-assistant-response",
		Title:           "Assistant response",
		TitleZH:         "助手回复",
		Goal:            "A normal assistant reply, preferably with paragraphs, lists, code or links.",
		GoalZH:          "助手普通回复，优先包含段落、列表、代码或链接。",
		Extractable:     "Assistant identity, Markdown hierarchy, body density, code blocks and paragraph spacing.",
		ExtractableZH:   "助手标识、Markdown 层级、正文密度、代码块和段间距。",
		Slots:           []Slot{defaultSlot("03-assistant-response")},
		Features:        []string{"assistant_text"},
		CandidateNote:   "Assistant text chunk; fenced code preferred.",
		CandidateNoteZH: "助手文本块；优先带围栏代码。",
	},
	{
		ID:              "04-thinking",
		Title:           "Thinking",
		TitleZH:         "思考",
		Goal:            "The native default state of thinking / reasoning.",
		GoalZH:          "thinking / reasoning 的原生默认状态。",
		Extractable:     "Thought marker, summary, body, side line, colors and the default expanded state.",
		ExtractableZH:   "思考标记、摘要、正文、侧边线、颜色和默认展开状态。",
		Slots:           []Slot{defaultSlot("04-thinking"), toggledSlot("04-thinking")},
		Features:        []string{"thinking"},
		CandidateNote:   "ThinkingStart / ThinkingChunk event.",
		CandidateNoteZH: "ThinkingStart / ThinkingChunk 事件。",
	},
	{
		ID:              "05-tool-invocation",
		Title:           "Tool invocation",
		TitleZH:         "工具调用",
		Goal:            "One representative complete tool call with parameters and result context.",
		GoalZH:          "一个具有代表性的完整工具调用，保留参数和结果上下文。",
		Extractable:     "Tool name, parameter summary, frame, bullet, indentation and single-tool structure.",
		ExtractableZH:   "工具名称、参数摘要、框线、bullet、缩进及单工具结构。",
		Slots:           []Slot{defaultSlot("05-tool-invocation"), toggledSlot("05-tool-invocation")},
		Features:        []string{"tool_invocation"},
		CandidateNote:   "ToolInvocation event, preferably paired with its result.",
		CandidateNoteZH: "ToolInvocation 事件，优先与结果成对。",
	},
	{
		ID:              "06-tool-running",
		Title:           "Tool running",
		TitleZH:         "工具执行中",
		Goal:            "The visible state while a tool is executing.",
		GoalZH:          "工具执行中的可见状态。",
		Extractable:     "Running marker, static spinner shape, status copy and colors; never used to simulate animation timing.",
		ExtractableZH:   "运行中标记、静态 spinner 形态、状态文案和颜色；不据此模拟动画时序。",
		Slots:           []Slot{defaultSlot("06-tool-running")},
		Features:        []string{"tool_running"},
		CandidateNote:   "Live session with an unpaired ToolInvocation.",
		CandidateNoteZH: "进行中的会话，且存在未配对的 ToolInvocation。",
	},
	{
		ID:              "07-tool-success",
		Title:           "Tool success",
		TitleZH:         "工具成功",
		Goal:            "A successful tool result.",
		GoalZH:          "工具成功结果。",
		Extractable:     "Success glyph, footer, duration, exit status and output boundaries.",
		ExtractableZH:   "成功符号、footer、耗时、exit 状态和输出边界。",
		Slots:           []Slot{defaultSlot("07-tool-success")},
		Features:        []string{"tool_result_success"},
		CandidateNote:   "ToolResult with exact success status.",
		CandidateNoteZH: "带精确成功状态的 ToolResult。",
	},
	{
		ID:              "08-tool-failure",
		Title:           "Tool failure",
		TitleZH:         "工具失败",
		Goal:            "A failed tool, stderr output or a non-zero exit.",
		GoalZH:          "工具失败、stderr 或非零退出。",
		Extractable:     "Failure glyph, error color, body and failure footer.",
		ExtractableZH:   "失败符号、错误颜色、正文和失败 footer。",
		Slots:           []Slot{defaultSlot("08-tool-failure")},
		Features:        []string{"tool_result_failure"},
		CandidateNote:   "ToolResult with ExitCode != 0, ErrorKind set, or structured stderr.",
		CandidateNoteZH: "ToolResult 的 ExitCode != 0、ErrorKind 有值，或存在结构化 stderr。",
	},
	{
		ID:              "09-tool-timeout",
		Title:           "Tool timeout",
		TitleZH:         "工具超时",
		Goal:            "An explicit tool timeout result.",
		GoalZH:          "明确的工具超时结果。",
		Extractable:     "How timeout differs visually from a plain failure.",
		ExtractableZH:   "timeout 与普通 failure 的视觉区别。",
		Slots:           []Slot{defaultSlot("09-tool-timeout")},
		Features:        []string{"tool_result_timeout"},
		CandidateNote:   "ToolResult.TimedOut == true.",
		CandidateNoteZH: "ToolResult.TimedOut == true。",
	},
	{
		ID:              "10-tool-rejected",
		Title:           "Tool rejected",
		TitleZH:         "工具被拒绝",
		Goal:            "A tool rejected by the user, a hook or a permission policy.",
		GoalZH:          "用户、hook 或权限策略拒绝工具。",
		Extractable:     "How rejection differs visually from failure and timeout.",
		ExtractableZH:   "rejected 与 failure / timeout 的视觉区别。",
		Slots:           []Slot{defaultSlot("10-tool-rejected")},
		Features:        []string{"tool_result_rejected"},
		CandidateNote:   "ToolResult.Rejected == true.",
		CandidateNoteZH: "ToolResult.Rejected == true。",
	},
	{
		ID:              "11-file-change",
		Title:           "File change",
		TitleZH:         "文件变化",
		Goal:            "The most representative file creation, modification, deletion or diff.",
		GoalZH:          "新增、修改、删除或 Diff 中最有代表性的文件变化。",
		Extractable:     "Path, hunks, +/- markers, background colors, gutter and the change summary.",
		ExtractableZH:   "路径、hunk、+/-、背景色、gutter 和文件变化摘要。",
		Slots:           []Slot{defaultSlot("11-file-change"), toggledSlot("11-file-change")},
		Features:        []string{"file_change"},
		CandidateNote:   "ToolInvocation recognized as a file-edit call.",
		CandidateNoteZH: "被识别为文件编辑的 ToolInvocation。",
	},
	{
		ID:              "12-subagent",
		Title:           "Subagent",
		TitleZH:         "子 Agent",
		Goal:            "A child agent / delegation run or result.",
		GoalZH:          "子 Agent / 委托的运行或结果。",
		Extractable:     "Parent-child hierarchy, task summary, status, result and the collaboration structure.",
		ExtractableZH:   "父子层级、任务摘要、状态、结果和协作视觉结构。",
		Slots:           []Slot{defaultSlot("12-subagent"), toggledSlot("12-subagent")},
		Features:        []string{"subagent"},
		CandidateNote:   "Collaboration invocation or an exact parent-child relation.",
		CandidateNoteZH: "协作 invocation 或精确的父子关系。",
	},
	{
		ID:              "13-context-boundary",
		Title:           "Context boundary",
		TitleZH:         "上下文边界",
		Goal:            "Compaction, rollback, rewind or another context boundary.",
		GoalZH:          "compaction、rollback、rewind 或其他上下文边界。",
		Extractable:     "Separator, history hint, boundary title and rollback/compaction treatment.",
		ExtractableZH:   "分隔符、历史提示、边界标题和回滚/压缩表现。",
		Slots:           []Slot{defaultSlot("13-context-boundary"), toggledSlot("13-context-boundary")},
		Features:        []string{"context_boundary"},
		CandidateNote:   "Compaction / rollback / rewind normalized events.",
		CandidateNoteZH: "compaction / rollback / rewind 归一化事件。",
	},
	{
		ID:              "14-permission-request",
		Title:           "Permission request",
		TitleZH:         "权限请求",
		Goal:            "A native CLI permission request.",
		GoalZH:          "原生 CLI 的权限请求。",
		Extractable:     "Options, focus, danger hints and the default selection. Undecided outcomes are never inferred from the image.",
		ExtractableZH:   "选项、焦点、危险提示和默认选择；不能从图片反推未持久化的决定。",
		Slots:           []Slot{defaultSlot("14-permission-request")},
		Features:        []string{"permission_request"},
		CandidateNote:   "Explicit permission-request event or subtype.",
		CandidateNoteZH: "明确的权限请求事件或 subtype。",
	},
	{
		ID:              "15-long-output",
		Title:           "Long output",
		TitleZH:         "长输出",
		Goal:            "Long output, soft wrapping, native truncation or elision.",
		GoalZH:          "长输出、软换行、原生截断或省略。",
		Extractable:     "Continuation lines, indentation, truncation summary, expand entry and output density.",
		ExtractableZH:   "续行、缩进、截断摘要、展开入口和输出密度。",
		Slots:           []Slot{defaultSlot("15-long-output"), toggledSlot("15-long-output")},
		Features:        []string{"long_output"},
		CandidateNote:   "Output above a stable threshold or an exact truncation marker.",
		CandidateNoteZH: "输出超过稳定阈值，或有精确 truncation 标记。",
	},
	{
		ID:              "16-live-status",
		Title:           "Live status",
		TitleZH:         "实时状态",
		Goal:            "Session-level state while the Agent is thinking, waiting for a tool or waiting for the user.",
		GoalZH:          "Agent 正在思考、等待工具或等待用户的会话级状态。",
		Extractable:     "Session-level live hint; never confused with a single tool's running state.",
		ExtractableZH:   "会话级 live 提示；不与单个工具运行状态混淆。",
		Slots:           []Slot{defaultSlot("16-live-status")},
		Features:        []string{"live_status"},
		CandidateNote:   "Exact session live state from the adapter.",
		CandidateNoteZH: "adapter 提供的精确会话 live 状态。",
	},
	{
		ID:              "17-session-completed",
		Title:           "Session completed",
		TitleZH:         "会话完成",
		Goal:            "A session or task that finished naturally.",
		GoalZH:          "会话或任务自然完成。",
		Extractable:     "Completion marker, final prompt, elapsed time or statistics.",
		ExtractableZH:   "完成标志、最终提示、耗时或统计。",
		Slots:           []Slot{defaultSlot("17-session-completed")},
		Features:        []string{"session_completed"},
		CandidateNote:   "Non-live finished session (low confidence suggestion).",
		CandidateNoteZH: "非 live 的已结束会话（低置信度建议）。",
	},
	{
		ID:              "18-session-interrupted",
		Title:           "Session interrupted",
		TitleZH:         "会话中断",
		Goal:            "A user interruption, abnormal termination or unfinished state.",
		GoalZH:          "用户中断、异常终止或未完成状态。",
		Extractable:     "How interrupted differs visually from completed / failed.",
		ExtractableZH:   "interrupted 与 completed / failed 的视觉区别。",
		Slots:           []Slot{defaultSlot("18-session-interrupted")},
		Features:        []string{"session_interrupted"},
		CandidateNote:   "Explicit interrupted marker.",
		CandidateNoteZH: "明确的 interrupted 标记。",
	},
	{
		ID:            "19-agent-specific",
		Title:         "Agent-specific scene",
		TitleZH:       "Agent 特有画面",
		Goal:          "The most recognizable scene of this Agent that does not fit the shared checklist.",
		GoalZH:        "该 Agent 最有辨识度但不属于通用清单的画面。",
		Extractable:   "Input for judging whether a new shared primitive is needed; never a direct Agent special-case.",
		ExtractableZH: "供后续判断是否需要新的共享原语；不能直接创建 Agent 特判。",
		Slots:         []Slot{defaultSlot("19-agent-specific")},
	},
	{
		ID:              "20-tool-group",
		Title:           "Tool group",
		TitleZH:         "工具组",
		Goal:            "The default state of a native group of consecutive tools.",
		GoalZH:          "连续多个工具组成的原生工具组默认状态。",
		Extractable:     "Group title, count, summary, group boundary and the default collapsed state.",
		ExtractableZH:   "工具组标题、计数、摘要、分组边界和默认折叠状态。",
		Slots:           []Slot{defaultSlot("20-tool-group"), toggledSlot("20-tool-group")},
		Features:        []string{"tool_group"},
		CandidateNote:   "Three or more consecutive tool invocations in one turn.",
		CandidateNoteZH: "同一回合内连续三次及以上工具调用。",
	},
	{
		ID:            "21-nested-fold",
		Title:         "Nested fold",
		TitleZH:       "嵌套折叠",
		Goal:          "The default state when parent and child foldable blocks exist at the same time.",
		GoalZH:        "同时存在父层与子层可折叠区块的默认状态。",
		Extractable:   "Nesting levels, parent/child summaries and fold boundaries.",
		ExtractableZH: "嵌套层级、父子摘要和折叠边界。",
		Slots: []Slot{
			defaultSlot("21-nested-fold"),
			{
				LogicalName: "21-nested-fold-inner-toggled",
				Label:       "Inner toggled",
				LabelZH:     "只切换内层",
				Hint:        "From the default state, toggle only the inner block; keep the outer at default.",
				HintZH:      "从默认状态只切换内层，外层保持默认。",
			},
			{
				LogicalName: "21-nested-fold-outer-toggled",
				Label:       "Outer toggled",
				LabelZH:     "只切换外层",
				Hint:        "Start over from the default state and toggle only the outer block.",
				HintZH:      "从默认状态重新开始，只切换外层。",
			},
		},
		Features:        []string{"nested_fold"},
		CandidateNote:   "Tool activity nested inside a subagent block.",
		CandidateNoteZH: "子 Agent 区块内嵌套的工具活动。",
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

// canonicalLogicalName maps a caller-supplied logical file name back to the
// checklist's own constant, so request text never flows into a path.
func canonicalLogicalName(input string) (string, bool) {
	if !knownLogicalNames[input] {
		return "", false
	}
	for name := range knownLogicalNames {
		if name == input {
			return name, true
		}
	}
	return "", false
}

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

// canonicalFeatureIDs is the unique presentation feature catalog from the
// implementation design. density is a profile-level dimension, not a feature.
var canonicalFeatureIDs = []string{
	"turn_boundary",
	"user_prompt",
	"assistant_text",
	"thinking",
	"tool_invocation",
	"tool_running",
	"tool_result_success",
	"tool_result_failure",
	"tool_result_timeout",
	"tool_result_rejected",
	"file_change",
	"subagent",
	"context_boundary",
	"permission_request",
	"long_output",
	"live_status",
	"session_completed",
	"session_interrupted",
	"tool_group",
	"nested_fold",
}

func isCanonicalFeature(id string) bool {
	for _, want := range canonicalFeatureIDs {
		if id == want {
			return true
		}
	}
	return false
}
