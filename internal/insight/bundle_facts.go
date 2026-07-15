package insight

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bbsteel/session-insight/internal/analytics"
	"github.com/bbsteel/session-insight/internal/model"
)

// buildSessionFact produces the single session-level aggregate fact keyed
// "session:summary" — the ID findings reference via a session EvidenceRef.
func buildSessionFact(detail *model.SessionDetail, res analytics.Result) EvidenceFact {
	userMsgs, subagents, totalReq := 0, 0, 0
	for _, t := range detail.Turns {
		if t.UserMessage != "" {
			userMsgs++
		}
		subagents += len(t.Subagents)
		totalReq += t.RequestCount
	}
	return EvidenceFact{
		ID:   "session:summary",
		Kind: "session",
		Statement: fmt.Sprintf("会话共 %d 轮，%d 条用户消息，%d 次模型请求，%d 次工具调用（其中失败 %d 次），派生 %d 个 subagent。",
			len(detail.Turns), userMsgs, totalReq, res.TotalTools, res.TotalErrors, subagents),
		Values: map[string]any{
			"active_turns":   len(detail.Turns),
			"user_messages":  userMsgs,
			"total_requests": totalReq,
			"total_tools":    res.TotalTools,
			"total_errors":   res.TotalErrors,
			"subagent_count": subagents,
		},
		Source: "analytics",
	}
}

// buildBillingFacts turns the session bill and context-pressure metrics into
// citable metric facts. Presence is honored: a bucket the agent never reported
// is omitted rather than emitted as 0, so the model can't read a gap as zero.
func buildBillingFacts(res analytics.Result) []EvidenceFact {
	var out []EvidenceFact
	bl := res.Billing
	if bl != nil {
		if bl.BillingUnit != "" && bl.BillingAmount > 0 {
			out = append(out, EvidenceFact{
				ID:        "metric:bill-total",
				Kind:      "metric",
				Statement: fmt.Sprintf("会话账单合计 %.4f %s。", bl.BillingAmount, bl.BillingUnit),
				Values:    map[string]any{"amount": bl.BillingAmount, "unit": bl.BillingUnit},
				Precision: bl.Precision,
				Source:    "billing",
			})
		}
		if bl.Totals.Present.Input == model.PresenceExact && bl.Totals.Present.CacheRead == model.PresenceExact {
			in := bl.Totals.PromptTokens
			cr := bl.Totals.CacheReadTokens
			cw := bl.Totals.CacheWriteTokens
			if side := in + cr + cw; side > 0 {
				out = append(out, EvidenceFact{
					ID:        "metric:input-composition",
					Kind:      "metric",
					Statement: fmt.Sprintf("输入侧 token 共 %d，其中 cache read %d（占 %.0f%%）、fresh input %d、cache write %d。", side, cr, float64(cr)/float64(side)*100, in, cw),
					Values: map[string]any{
						"input_side_tokens":  side,
						"cache_read_tokens":  cr,
						"fresh_input_tokens": in,
						"cache_write_tokens": cw,
					},
					Precision: string(model.PresenceExact),
					Source:    "billing",
				})
			}
		}
	}
	if res.ContextWindow > 0 && res.ContextPeak > 0 {
		out = append(out, EvidenceFact{
			ID:        "metric:context-pressure",
			Kind:      "metric",
			Statement: fmt.Sprintf("上下文峰值约 %d tokens，约为模型窗口 %d 的 %.0f%%。", res.ContextPeak, res.ContextWindow, res.PressurePct),
			Values:    map[string]any{"peak_tokens": res.ContextPeak, "context_window": res.ContextWindow, "pressure_pct": res.PressurePct},
			Source:    "analytics",
		})
	}
	return out
}

// buildTurnFacts emits one fact per turn. Finding-referenced turns are kept
// unconditionally; the rest fill the remaining rune budget, dropping from the
// oldest non-referenced turn first. Returns the facts and the count of
// non-referenced turns that were elided.
func buildTurnFacts(detail *model.SessionDetail, referenced map[string]int, budgetRunes int) ([]EvidenceFact, int) {
	refSet := map[int]bool{}
	for _, ti := range referenced {
		refSet[ti] = true
	}

	facts := make([]EvidenceFact, len(detail.Turns))
	for i, t := range detail.Turns {
		facts[i] = buildTurnFact(t)
	}

	// Referenced turns are mandatory; account their cost first.
	used := 0
	keep := make([]bool, len(facts))
	for i, t := range detail.Turns {
		if refSet[t.TurnIndex] {
			keep[i] = true
			used += factRunes(facts[i])
		}
	}

	// Fill remaining budget from the most recent non-referenced turns backward,
	// so the recent narrative survives before older middle turns.
	elided := 0
	for i := len(facts) - 1; i >= 0; i-- {
		if keep[i] {
			continue
		}
		cost := factRunes(facts[i])
		if used+cost > budgetRunes {
			elided++
			continue
		}
		used += cost
		keep[i] = true
	}

	var out []EvidenceFact
	for i := range facts {
		if keep[i] {
			out = append(out, facts[i])
		}
	}
	return out, elided
}

func buildTurnFact(t model.TurnVM) EvidenceFact {
	i := t.TurnIndex
	var parts []string
	if t.UserMessage != "" {
		parts = append(parts, "用户: "+truncateRunes(t.UserMessage, userMsgMaxRunes))
	}
	if t.AssistantMessage != "" {
		parts = append(parts, "助手结论: "+truncateRunes(t.AssistantMessage, assistantMaxRunes))
	}
	for _, tc := range t.ToolDetails {
		if tc.ErrorMessage == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("工具报错[%s]: %s", tc.Name, truncateRunes(tc.ErrorMessage, toolErrMaxRunes)))
	}
	tok := t.TokenUsage.PromptTokens + t.TokenUsage.CompletionTokens
	values := map[string]any{
		"requests":    t.RequestCount,
		"tool_calls":  t.ToolCallCount,
		"errors":      t.ErrorCount,
		"duration_ms": t.DurationMs,
		"tokens":      tok,
	}
	if len(t.Subagents) > 0 {
		values["subagents"] = len(t.Subagents)
	}
	if t.RolledBack {
		values["rolled_back"] = true
	}
	return EvidenceFact{
		ID:        fmt.Sprintf("turn:%d", t.TurnIndex),
		Kind:      "turn",
		Statement: strings.Join(parts, " / "),
		Values:    values,
		TurnIndex: &i,
		Source:    "session_detail",
	}
}

// buildSubagentFacts emits one fact per subagent from reader-specific evidence.
// The ID is derived from the stable tool_call_id, matching the finding-side
// convention "subagent:<tool_call_id>".
func buildSubagentFacts(subs []model.SubagentEvidence) []EvidenceFact {
	var out []EvidenceFact
	for _, s := range subs {
		id := s.ToolCallID
		if id == "" {
			continue
		}
		i := s.TurnIndex
		var parts []string
		if s.Name != "" {
			parts = append(parts, "名称: "+s.Name)
		}
		if s.Description != "" {
			parts = append(parts, "委派描述: "+truncateRunes(s.Description, subagentPromptMax))
		}
		if s.Mode != "" {
			parts = append(parts, "模式: "+s.Mode)
		}
		fact := EvidenceFact{
			ID:        "subagent:" + id,
			Kind:      "subagent",
			Statement: strings.Join(parts, " / "),
			TurnIndex: &i,
			EventID:   s.ToolCallID,
			Source:    "reader_evidence",
			Values: map[string]any{
				"model":          s.Model,
				"mode":           s.Mode,
				"duration_ms":    s.DurationMs,
				"request_count":  s.RequestCount,
				"output_tokens":  s.OutputTokens,
				"prompt_chars":   s.PromptChars,
				"overlaps_other": s.OverlapsOther,
			},
		}
		out = append(out, fact)
	}
	return out
}

// buildToolFacts emits facts for notable tool events (failures / timeouts /
// rejections). Successful, unremarkable calls are represented by the
// session-level aggregate, not per-call facts, to stay within budget.
func buildToolFacts(tools []model.ToolEvidence) []EvidenceFact {
	// Group repeated failures of the same tool so the model sees churn, not a
	// flat list. Key on tool name + error kind.
	type key struct{ name, kind string }
	groups := map[key][]model.ToolEvidence{}
	var order []key
	for _, t := range tools {
		if t.ExitCode == 0 && !t.TimedOut && !t.Rejected {
			continue
		}
		k := key{t.Name, t.ErrorKind}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], t)
	}
	sort.SliceStable(order, func(i, j int) bool { return len(groups[order[i]]) > len(groups[order[j]]) })

	var out []EvidenceFact
	for _, k := range order {
		g := groups[k]
		first := g[0]
		i := first.TurnIndex
		desc := "失败"
		switch {
		case first.TimedOut:
			desc = "超时"
		case first.Rejected:
			desc = "被拒绝"
		}
		out = append(out, EvidenceFact{
			ID:        fmt.Sprintf("tool:%s:%s", safeKey(k.name), safeKey(k.kind)),
			Kind:      "tool",
			Statement: fmt.Sprintf("工具 %s %s 共 %d 次；首例报错: %s", k.name, desc, len(g), truncateRunes(first.ErrorMsg, toolErrMaxRunes)),
			TurnIndex: &i,
			Source:    "reader_evidence",
			Values:    map[string]any{"count": len(g), "error_kind": k.kind},
		})
	}
	return out
}

func safeKey(s string) string {
	if s == "" {
		return "na"
	}
	s = strings.ReplaceAll(s, ":", "-")
	s = strings.ReplaceAll(s, " ", "-")
	return s
}

func factRunes(f EvidenceFact) int {
	return len([]rune(f.Statement)) + len(f.ID) + 40
}
