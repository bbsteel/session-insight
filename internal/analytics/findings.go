package analytics

import (
	"fmt"
	"sort"

	"github.com/bbsteel/session-insight/internal/model"
)

// Finding is one conclusion-style waste pattern detected in a session,
// phrased so the user doesn't have to read it out of the charts themselves.
type Finding struct {
	Severity  string `json:"severity"` // "critical" | "warn" | "info"
	Title     string `json:"title"`
	Detail    string `json:"detail"`
	TurnIndex *int   `json:"turn_index,omitempty"`
}

const (
	sevCritical = "critical"
	sevWarn     = "warn"
	sevInfo     = "info"
)

// detectFindings runs a small set of hand-written waste heuristics. All
// rules operate on the unified model only; thresholds are deliberately
// conservative so a finding, when shown, is worth reading.
func detectFindings(detail *model.SessionDetail, timeline []TurnToken, billing *model.SessionBilling, hasCost bool) []Finding {
	var findings []Finding

	userMsgs := 0
	subagents := 0
	totalReq := 0
	totalTools, totalErrors := 0, 0
	for _, t := range detail.Turns {
		if t.UserMessage != "" {
			userMsgs++
		}
		subagents += len(t.Subagents)
		totalReq += t.RequestCount
		totalTools += t.ToolCallCount
		totalErrors += t.ErrorCount
	}

	// 1. Instruction amplification: few user messages driving many billed
	// API requests.
	if userMsgs > 0 && totalReq > 0 {
		factor := totalReq / userMsgs
		if factor >= 15 {
			sev := sevWarn
			if factor >= 30 {
				sev = sevCritical
			}
			findings = append(findings, Finding{
				Severity: sev,
				Title:    fmt.Sprintf("指令放大 %d 倍", factor),
				Detail:   fmt.Sprintf("%d 条用户消息驱动了 %d 次 API 请求，每条指令平均引发 %d 次计费调用。开放式指令（如“把它修好”）容易触发超长 agent 循环。", userMsgs, totalReq, factor),
			})
		}
	}

	// 2. Cost concentration: one turn dominating the session bill (or the
	// token volume when no bill was attributed).
	var sessionTotal float64
	value := func(t TurnToken) float64 {
		if hasCost {
			return t.EstCost
		}
		return float64(t.Tokens)
	}
	for _, t := range timeline {
		sessionTotal += value(t)
	}
	if sessionTotal > 0 && len(timeline) >= 3 {
		top := timeline[0]
		for _, t := range timeline[1:] {
			if value(t) > value(top) {
				top = t
			}
		}
		if share := value(top) / sessionTotal; share >= 0.5 {
			sev := sevWarn
			if share >= 0.7 {
				sev = sevCritical
			}
			amount := fmt.Sprintf("%s tokens", fmtCount(int64(value(top))))
			if hasCost && billing != nil {
				amount = fmtAmount(top.EstCost, billing.BillingUnit) + "（估算）"
			}
			idx := top.TurnIndex
			findings = append(findings, Finding{
				Severity:  sev,
				Title:     fmt.Sprintf("消耗集中在 Turn %d", top.TurnIndex),
				Detail:    fmt.Sprintf("该 turn 占本场消耗的 %.0f%%，约 %s，包含 %d 次请求、%d 次工具调用。点击跳转查看它当时在做什么。", share*100, amount, top.Requests, top.ToolCount),
				TurnIndex: &idx,
			})
		}
	}

	// 3. Long-context replay: cache reads dwarfing fresh input means the
	// whole context is re-sent on every request.
	if billing != nil &&
		billing.Totals.Present.Input == model.PresenceExact &&
		billing.Totals.Present.CacheRead == model.PresenceExact {
		in, cr, cw := billing.Totals.PromptTokens, billing.Totals.CacheReadTokens, billing.Totals.CacheWriteTokens
		if inputSide := in + cr + cw; inputSide > 0 && cr >= 1_000_000 {
			if share := float64(cr) / float64(inputSide); share >= 0.9 {
				findings = append(findings, Finding{
					Severity: sevWarn,
					Title:    "长上下文反复重放",
					Detail:   fmt.Sprintf("cache read 达 %s tokens，占输入侧 %.0f%%：每次请求都会重放整个上下文。缓存虽有折扣，上下文越长每次调用仍越贵，考虑更早开新会话或让 agent 压缩上下文。", fmtCount(cr), share*100),
				})
			}
		}
	}

	// 4. Subagent fan-out: every subagent carries its own context.
	if subagents >= 10 {
		findings = append(findings, Finding{
			Severity: sevInfo,
			Title:    fmt.Sprintf("启动了 %d 个 subagent", subagents),
			Detail:   "每个 subagent 携带独立上下文并单独计费；批量派生 subagent 是长会话中常见的成本放大来源。",
		})
	}

	// 5. Suspected runaway tool loop inside a single turn.
	for _, t := range timeline {
		if t.ToolCount >= 100 {
			sev := sevWarn
			if t.ToolCount >= 300 {
				sev = sevCritical
			}
			idx := t.TurnIndex
			findings = append(findings, Finding{
				Severity:  sev,
				Title:     fmt.Sprintf("Turn %d 疑似长工具循环", t.TurnIndex),
				Detail:    fmt.Sprintf("单个 turn 内发生 %d 次工具调用。每次工具结果都会追加进上下文并触发新的计费请求。", t.ToolCount),
				TurnIndex: &idx,
			})
		}
	}

	// 6. Failure churn: failed calls and their retries bill the same.
	if totalTools >= 20 && totalErrors*5 >= totalTools {
		findings = append(findings, Finding{
			Severity: sevWarn,
			Title:    fmt.Sprintf("工具失败率 %.0f%%", float64(totalErrors)/float64(totalTools)*100),
			Detail:   fmt.Sprintf("%d 次工具调用中 %d 次失败。失败与随后的重试消耗同样计费，反复失败通常意味着 agent 卡在同一个问题上。", totalTools, totalErrors),
		})
	}

	sort.SliceStable(findings, func(i, j int) bool {
		return sevRank(findings[i].Severity) < sevRank(findings[j].Severity)
	})
	return findings
}

func sevRank(s string) int {
	switch s {
	case sevCritical:
		return 0
	case sevWarn:
		return 1
	default:
		return 2
	}
}

func fmtCount(n int64) string {
	switch {
	case n >= 100_000_000:
		return fmt.Sprintf("%.1f 亿", float64(n)/1e8)
	case n >= 10_000:
		return fmt.Sprintf("%.0f 万", float64(n)/1e4)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func fmtAmount(v float64, unit string) string {
	switch unit {
	case "aiu":
		return fmt.Sprintf("%.1f AIU", v)
	case "usd":
		return fmt.Sprintf("$%.4f", v)
	default:
		return fmt.Sprintf("%.1f %s", v, unit)
	}
}
