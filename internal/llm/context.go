package llm

import (
	"fmt"
	"strings"

	"github.com/bbsteel/session-insight/internal/model"
)

// Budget caps for the session transcript fed to a model, in runes. Rune
// counts are a crude token proxy but hold across CJK/ASCII mixes well
// enough for a safety cap.
const (
	contextBudgetRunes = 60000
	// Handoff feeds smaller models a structured brief, not a replay — a
	// full 60k-rune transcript reliably stalls cheap API models on the
	// "请求模型" step, and the handoff template only needs task framing plus
	// the recent tail.
	handoffBudgetRunes = 24000
	userMsgMaxRunes    = 2000
	assistantMaxRunes  = 1200
	lightUserMaxRunes  = 400
	toolErrMaxRunes    = 200

	// Full-detail turns preserved at each end when the transcript must be
	// squeezed: the opening frames the task, the tail carries current state.
	headFullTurns = 2
	tailFullTurns = 6
)

func truncateRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…(截断)"
}

// BuildSessionContext renders a session transcript within the default rune
// budget. See buildSessionContext for the reduction strategy.
func BuildSessionContext(detail *model.SessionDetail) string {
	return buildSessionContext(detail, contextBudgetRunes)
}

// BuildHandoffContext renders the slimmer transcript used for handoff
// prompts.
func BuildHandoffContext(detail *model.SessionDetail) string {
	return buildSessionContext(detail, handoffBudgetRunes)
}

// buildSessionContext renders a session transcript within budgetRunes.
// Reduction order when over budget: middle turns lose assistant/tool detail
// first, then middle turns are elided entirely (head+tail kept) with an
// explicit "(中间 N 轮已省略)" marker so the model knows the gap exists.
func buildSessionContext(detail *model.SessionDetail, budgetRunes int) string {
	var header strings.Builder
	fmt.Fprintf(&header, "# 会话记录\n\n")
	fmt.Fprintf(&header, "- Agent: %s\n", detail.AgentType)
	if detail.Project != "" {
		fmt.Fprintf(&header, "- 项目: %s\n", detail.Project)
	}
	if detail.Repository != "" {
		repo := detail.Repository
		if detail.Branch != "" {
			repo += " @" + detail.Branch
		}
		fmt.Fprintf(&header, "- 仓库: %s\n", repo)
	}
	if detail.CWD != "" {
		fmt.Fprintf(&header, "- 工作目录: %s\n", detail.CWD)
	}
	if detail.ModelName != "" {
		fmt.Fprintf(&header, "- 模型: %s\n", detail.ModelName)
	}
	fmt.Fprintf(&header, "- 轮数: %d\n", len(detail.Turns))
	if len(detail.Todos) > 0 {
		header.WriteString("- Todos:\n")
		for _, t := range detail.Todos {
			fmt.Fprintf(&header, "  - [%s] %s\n", t.Status, t.Title)
		}
	}
	header.WriteString("\n")

	full := make([]string, len(detail.Turns))
	for i, t := range detail.Turns {
		full[i] = renderTurn(t, false)
	}

	budget := budgetRunes - len([]rune(header.String()))
	if total := runesLen(full); total <= budget {
		return header.String() + strings.Join(full, "\n")
	}

	// Pass 1: middle turns drop to user-message-only.
	blocks := make([]string, len(detail.Turns))
	for i, t := range detail.Turns {
		if i < headFullTurns || i >= len(detail.Turns)-tailFullTurns {
			blocks[i] = full[i]
		} else {
			blocks[i] = renderTurn(t, true)
		}
	}
	if runesLen(blocks) <= budget {
		return header.String() + strings.Join(blocks, "\n")
	}

	// Pass 2: elide middle turns entirely, dropping from the oldest side of
	// the middle first so the recent narrative survives.
	head := blocks[:min(headFullTurns, len(blocks))]
	tailStart := max(len(blocks)-tailFullTurns, len(head))
	tail := blocks[tailStart:]
	middle := blocks[len(head):tailStart]

	fixed := runesLen(head) + runesLen(tail)
	var kept []string
	remaining := budget - fixed - 40 // reserve room for the elision marker
	for i := len(middle) - 1; i >= 0; i-- {
		n := len([]rune(middle[i]))
		if remaining-n < 0 {
			break
		}
		remaining -= n
		kept = append([]string{middle[i]}, kept...)
	}

	elided := len(middle) - len(kept)
	var out []string
	out = append(out, head...)
	if elided > 0 {
		out = append(out, fmt.Sprintf("(中间 %d 轮已省略)\n", elided))
	}
	out = append(out, kept...)
	out = append(out, tail...)
	return header.String() + strings.Join(out, "\n")
}

func renderTurn(t model.TurnVM, light bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Turn %d\n", t.TurnIndex)
	if t.UserMessage != "" {
		limit := userMsgMaxRunes
		if light {
			limit = lightUserMaxRunes
		}
		fmt.Fprintf(&b, "用户: %s\n", truncateRunes(t.UserMessage, limit))
	}
	if light {
		return b.String()
	}
	if t.AssistantMessage != "" {
		fmt.Fprintf(&b, "助手: %s\n", truncateRunes(t.AssistantMessage, assistantMaxRunes))
	}
	if len(t.ToolNames) > 0 {
		fmt.Fprintf(&b, "工具: %s\n", strings.Join(t.ToolNames, ", "))
	}
	for _, tc := range t.ToolDetails {
		if tc.ErrorMessage == "" {
			continue
		}
		fmt.Fprintf(&b, "工具报错 [%s]: %s\n", tc.Name, truncateRunes(tc.ErrorMessage, toolErrMaxRunes))
	}
	return b.String()
}

func runesLen(blocks []string) int {
	n := 0
	for _, b := range blocks {
		n += len([]rune(b))
	}
	return n
}
