package analytics

import (
	"fmt"
	"sort"

	"github.com/bbsteel/session-insight/internal/model"
)

// Finding is one conclusion-style waste pattern detected in a session,
// phrased so the user doesn't have to read it out of the charts themselves.
//
// The first layer stays deterministic and model-free: Detail describes only
// what the rule measured (facts + the billing mechanism they trigger), never
// an open-ended "why". Any unverified causal reading belongs to the Deep
// Insight layer, which consumes Code + Metrics + EvidenceRefs — not Detail —
// so heuristic prose can't anchor the model's cause judgement.
type Finding struct {
	// Code is the stable rule identifier (e.g. "instruction_amplification").
	// Wording may change; Code must not, so results and evidence can be keyed
	// to a rule across releases.
	Code         string         `json:"code"`
	Severity     string         `json:"severity"` // "critical" | "warn" | "info"
	Title        string         `json:"title"`
	Detail       string         `json:"detail"`
	TurnIndex    *int           `json:"turn_index,omitempty"`
	Metrics      map[string]any `json:"metrics,omitempty"`
	EvidenceRefs []EvidenceRef  `json:"evidence_refs,omitempty"`
}

// EvidenceRef points a Finding at the minimal evidence that supports it. ID is
// a stable source key (never an array index) shared with the Evidence Bundle,
// so a Deep Insight can resolve the same fact the rule saw.
type EvidenceRef struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"` // session | turn | tool | subagent | request | skill
	TurnIndex *int   `json:"turn_index,omitempty"`
	EventID   string `json:"event_id,omitempty"`
	Label     string `json:"label,omitempty"`
}

// Stable Finding codes. These are the contract the insight layer keys on.
const (
	CodeInstructionAmplification = "instruction_amplification"
	CodeCostConcentration        = "cost_concentration"
	CodeLongContextReplay        = "long_context_replay"
	CodeSubagentFanout           = "subagent_fanout"
	CodeToolLoop                 = "tool_loop"
	CodeToolFailureChurn         = "tool_failure_churn"
	CodeToolTimeoutChurn         = "tool_timeout_churn"
	CodeToolRejectionChurn       = "tool_rejection_churn"
	CodeContinuationPressure     = "continuation_pressure"
)

const (
	sevCritical = "critical"
	sevWarn     = "warn"
	sevInfo     = "info"
)

// sessionRef is the evidence handle for a session-level aggregate fact. The
// Evidence Bundle publishes an EvidenceFact under the same ID.
func sessionRef(label string) EvidenceRef {
	return EvidenceRef{ID: "session:summary", Kind: "session", Label: label}
}

// turnRef is the evidence handle for one turn. ID mirrors the Bundle's
// per-turn EvidenceFact so a Deep Insight can jump to the same turn.
func turnRef(idx int, label string) EvidenceRef {
	i := idx
	return EvidenceRef{ID: fmt.Sprintf("turn:%d", idx), Kind: "turn", TurnIndex: &i, Label: label}
}

// detectFindings runs a small set of hand-written waste heuristics. All
// rules operate on the unified model only; thresholds are deliberately
// conservative so a finding, when shown, is worth reading.
func detectFindings(detail *model.SessionDetail, timeline []TurnToken, billing *model.SessionBilling, hasCost bool) []Finding {
	var findings []Finding

	userMsgs := 0
	subagents := 0
	totalReq := 0
	totalTools, totalErrors := 0, 0
	totalTimeouts, totalRejected := 0, 0
	nudges := 0
	var firstNudgeTurn int
	var subagentTurns []int // turns that spawned at least one subagent
	for _, t := range detail.Turns {
		if t.UserMessage != "" {
			userMsgs++
		}
		if len(t.Subagents) > 0 {
			subagents += len(t.Subagents)
			subagentTurns = append(subagentTurns, t.TurnIndex)
		}
		totalReq += t.RequestCount
		totalTools += t.ToolCallCount
		totalErrors += t.ErrorCount
		for _, td := range t.ToolDetails {
			if td.TimedOut {
				totalTimeouts++
			}
			if td.Rejected {
				totalRejected++
			}
		}
		for _, a := range t.Anomalies {
			if a == "continuation_nudge" {
				if nudges == 0 {
					firstNudgeTurn = t.TurnIndex
				}
				nudges++
			}
		}
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
				Code:     CodeInstructionAmplification,
				Severity: sev,
				Title:    fmt.Sprintf("指令放大 %d 倍", factor),
				Detail:   fmt.Sprintf("%d 条用户消息驱动了 %d 次 API 请求，每条指令平均引发 %d 次计费调用。", userMsgs, totalReq, factor),
				Metrics: map[string]any{
					"user_messages":  userMsgs,
					"total_requests": totalReq,
					"factor":         factor,
				},
				EvidenceRefs: []EvidenceRef{sessionRef("用户消息数与 API 请求数")},
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
				Code:      CodeCostConcentration,
				Severity:  sev,
				Title:     fmt.Sprintf("消耗集中在 Turn %d", top.TurnIndex),
				Detail:    fmt.Sprintf("该 turn 占本场消耗的 %.0f%%，约 %s，包含 %d 次请求、%d 次工具调用。点击跳转查看它当时在做什么。", share*100, amount, top.Requests, top.ToolCount),
				TurnIndex: &idx,
				Metrics: map[string]any{
					"turn_index":    top.TurnIndex,
					"share":         share,
					"requests":      top.Requests,
					"tool_count":    top.ToolCount,
					"cost_by_bill":  hasCost,
				},
				EvidenceRefs: []EvidenceRef{turnRef(top.TurnIndex, "占本场消耗最高的 turn")},
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
					Code:     CodeLongContextReplay,
					Severity: sevWarn,
					Title:    "长上下文反复重放",
					Detail:   fmt.Sprintf("cache read 达 %s tokens，占输入侧 %.0f%%：每次请求都在重放同一段上下文。缓存有折扣，但上下文越长每次调用的输入成本仍越高。", fmtCount(cr), share*100),
					Metrics: map[string]any{
						"cache_read_tokens": cr,
						"input_side_tokens": inputSide,
						"cache_read_share":  share,
					},
					EvidenceRefs: []EvidenceRef{sessionRef("输入侧 token 构成（含 cache read 占比）")},
				})
			}
		}
	}

	// 4. Subagent fan-out: every subagent carries its own context.
	if subagents >= 10 {
		refs := make([]EvidenceRef, 0, len(subagentTurns)+1)
		refs = append(refs, sessionRef("subagent 派生总数"))
		for _, ti := range subagentTurns {
			refs = append(refs, turnRef(ti, "派生 subagent 的 turn"))
		}
		findings = append(findings, Finding{
			Code:     CodeSubagentFanout,
			Severity: sevInfo,
			Title:    fmt.Sprintf("启动了 %d 个 subagent", subagents),
			Detail:   fmt.Sprintf("会话共派生 %d 个 subagent，分布在 %d 个 turn 中。每个 subagent 携带独立上下文并单独计费。", subagents, len(subagentTurns)),
			Metrics: map[string]any{
				"subagent_count": subagents,
				"subagent_turns": len(subagentTurns),
			},
			EvidenceRefs: refs,
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
				Code:      CodeToolLoop,
				Severity:  sev,
				Title:     fmt.Sprintf("Turn %d 疑似长工具循环", t.TurnIndex),
				Detail:    fmt.Sprintf("单个 turn 内发生 %d 次工具调用。每次工具结果都会追加进上下文并触发新的计费请求。", t.ToolCount),
				TurnIndex: &idx,
				Metrics: map[string]any{
					"turn_index": t.TurnIndex,
					"tool_count": t.ToolCount,
				},
				EvidenceRefs: []EvidenceRef{turnRef(t.TurnIndex, "单 turn 工具调用次数")},
			})
		}
	}

	// 6. Failure churn: failed calls and their retries bill the same.
	if totalTools >= 20 && totalErrors*5 >= totalTools {
		rate := float64(totalErrors) / float64(totalTools)
		findings = append(findings, Finding{
			Code:     CodeToolFailureChurn,
			Severity: sevWarn,
			Title:    fmt.Sprintf("工具失败率 %.0f%%", rate*100),
			Detail:   fmt.Sprintf("%d 次工具调用中 %d 次失败。失败与随后的重试消耗同样计费。", totalTools, totalErrors),
			Metrics: map[string]any{
				"total_tools":   totalTools,
				"total_errors":  totalErrors,
				"failure_rate":  rate,
			},
			EvidenceRefs: []EvidenceRef{sessionRef("工具调用总数与失败数")},
		})
	}

	// 7. Timeout churn: timed-out tool calls waste the full timeout duration.
	if totalTimeouts >= 3 {
		findings = append(findings, Finding{
			Code:     CodeToolTimeoutChurn,
			Severity: sevWarn,
			Title:    fmt.Sprintf("%d 次工具超时", totalTimeouts),
			Detail:   fmt.Sprintf("会话中发生了 %d 次工具超时。超时的工具调用在等待期间占用 agent 循环且结果不可用。", totalTimeouts),
			Metrics: map[string]any{
				"timeout_count": totalTimeouts,
			},
			EvidenceRefs: []EvidenceRef{sessionRef("工具超时次数")},
		})
	}

	// 8. Rejection churn: user/hook rejected tool calls indicate friction.
	if totalRejected >= 3 {
		findings = append(findings, Finding{
			Code:     CodeToolRejectionChurn,
			Severity: sevInfo,
			Title:    fmt.Sprintf("%d 次工具被拒绝", totalRejected),
			Detail:   fmt.Sprintf("会话中 %d 次工具调用被用户或 hook 拒绝。", totalRejected),
			Metrics: map[string]any{
				"rejected_count": totalRejected,
			},
			EvidenceRefs: []EvidenceRef{sessionRef("工具被拒绝次数")},
		})
	}

	// 9. Continuation pressure: the agent keeps stopping at intermediate
	// output and the user has to push it forward with "继续"/"ok".
	if nudges >= 2 {
		idx := firstNudgeTurn
		findings = append(findings, Finding{
			Code:      CodeContinuationPressure,
			Severity:  sevWarn,
			Title:     fmt.Sprintf("需要人工续跑 %d 次", nudges),
			Detail:    fmt.Sprintf("助手 %d 次在阶段性输出后结束，用户需发「继续」/「ok」推动任务继续。点击跳转到第一次续跑。", nudges),
			TurnIndex: &idx,
			Metrics: map[string]any{
				"nudge_count":      nudges,
				"first_nudge_turn": firstNudgeTurn,
			},
			EvidenceRefs: []EvidenceRef{turnRef(firstNudgeTurn, "第一次人工续跑的 turn")},
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
