// Package insight builds the deterministic evidence a model needs to explain
// why a session's preliminary Findings occurred, runs the versioned analysis
// Skill against a configured model source, and validates the structured output
// back into safe, citable Deep Insight. Nothing here calls a model directly;
// the bundle is pure data assembled from the unified model plus optional
// reader-specific evidence, so it stays testable and free of provider concerns.
package insight

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bbsteel/session-insight/internal/analytics"
	"github.com/bbsteel/session-insight/internal/model"
)

// BundleSchemaVersion changes whenever the evidence contract changes in a way
// that should invalidate previously-saved Insights (see freshness rules).
const BundleSchemaVersion = 1

// Rune budgets for the serialized bundle. This is a distinct budget from the
// summary/handoff transcript budget: evidence selection has its own priority
// order (findings first, then finding-referenced turns, then context), so it
// must not reuse the summary head/tail trimming.
const (
	bundleBudgetRunes = 40000
	userMsgMaxRunes   = 600
	assistantMaxRunes = 600
	toolErrMaxRunes   = 200
	subagentPromptMax = 400
)

// EvidenceFact is one referable fact in the bundle. The model may only cite an
// evidence_id that exists here; ID must come from a stable source key, never
// an array index, so citations survive re-ordering and re-analysis.
type EvidenceFact struct {
	ID        string         `json:"evidence_id"`
	Kind      string         `json:"kind"` // session | turn | tool | subagent | request | skill | metric
	Statement string         `json:"statement"`
	Values    map[string]any `json:"values,omitempty"`
	TurnIndex *int           `json:"turn_index,omitempty"`
	EventID   string         `json:"event_id,omitempty"`
	Precision string         `json:"precision,omitempty"`
	Source    string         `json:"source"`
}

// FindingBrief is the machine-facing projection of a preliminary Finding sent
// to the model: Code, computed metrics and evidence refs — never Detail. The
// heuristic prose must not anchor the model's cause judgement.
type FindingBrief struct {
	Code         string                  `json:"code"`
	Severity     string                  `json:"severity"`
	Metrics      map[string]any          `json:"metrics,omitempty"`
	EvidenceRefs []analytics.EvidenceRef `json:"evidence_refs,omitempty"`
}

// SessionMeta is neutral session context (not a citable fact on its own).
type SessionMeta struct {
	AgentType     string  `json:"agent_type"`
	ModelName     string  `json:"model_name,omitempty"`
	Project       string  `json:"project,omitempty"`
	CreatedAt     string  `json:"created_at,omitempty"`
	UpdatedAt     string  `json:"updated_at,omitempty"`
	ActiveTurns   int     `json:"active_turns"`
	RolledBack    int     `json:"rolled_back_turns,omitempty"`
	BillingUnit   string  `json:"billing_unit,omitempty"`
	BillingAmount float64 `json:"billing_amount,omitempty"`
	BillPrecision string  `json:"bill_precision,omitempty"`
}

// Bundle is the complete, serializable evidence payload handed to the Skill.
type Bundle struct {
	SchemaVersion int            `json:"schema_version"`
	Session       SessionMeta    `json:"session"`
	Findings      []FindingBrief `json:"findings"`
	Facts         []EvidenceFact `json:"evidence_facts"`
	EvidenceGaps  []string       `json:"evidence_gaps,omitempty"`
}

// BuildBundle assembles the generic evidence bundle from a SessionDetail, its
// analytics Result, and optional reader-specific evidence. It guarantees every
// Finding.EvidenceRefs ID resolves to a fact in the bundle, so a model citation
// of a referenced ID can always be validated.
func BuildBundle(detail *model.SessionDetail, res analytics.Result, ev *model.InsightEvidence) Bundle {
	return buildBundleWithBudget(detail, res, ev, bundleBudgetRunes)
}

func buildBundleWithBudget(detail *model.SessionDetail, res analytics.Result, ev *model.InsightEvidence, budgetRunes int) Bundle {
	b := Bundle{
		SchemaVersion: BundleSchemaVersion,
		Session: SessionMeta{
			AgentType:   detail.AgentType,
			ModelName:   detail.ModelName,
			Project:     detail.Project,
			ActiveTurns: len(detail.Turns),
			RolledBack:  res.RolledBackTurnCount,
		},
	}
	if !detail.CreatedAt.IsZero() {
		b.Session.CreatedAt = detail.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	if !detail.UpdatedAt.IsZero() {
		b.Session.UpdatedAt = detail.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	if res.Billing != nil {
		b.Session.BillingUnit = res.Billing.BillingUnit
		b.Session.BillingAmount = res.Billing.BillingAmount
		b.Session.BillPrecision = res.Billing.Precision
	}

	for _, f := range res.Findings {
		b.Findings = append(b.Findings, FindingBrief{
			Code: f.Code, Severity: f.Severity,
			Metrics: f.Metrics, EvidenceRefs: f.EvidenceRefs,
		})
	}

	facts := newFactSet()
	var gaps []string

	// (1) Session-level aggregate fact — always present, referenced by many
	// findings via sessionRef("session:summary").
	facts.add(buildSessionFact(detail, res))

	// (1b) Billing / context-pressure metric facts.
	for _, f := range buildBillingFacts(res) {
		facts.add(f)
	}

	// (2) Finding-referenced turns come first; (3) then remaining turns fill
	// the remaining budget, oldest-middle dropped first so recent narrative and
	// referenced turns survive.
	referenced := referencedTurns(res.Findings)
	turnFacts, elided := buildTurnFacts(detail, referenced, budgetRunes)
	for _, f := range turnFacts {
		facts.add(f)
	}
	if elided > 0 {
		gaps = append(gaps, fmt.Sprintf("%d 个未被 Finding 直接引用的 turn 因预算裁剪未展开原文", elided))
	}

	// (4) Reader-specific aggregates (subagents / model requests / tools).
	if ev != nil {
		for _, f := range buildSubagentFacts(ev.Subagents) {
			facts.add(f)
		}
		for _, f := range buildToolFacts(ev.ToolEvents) {
			facts.add(f)
		}
	} else if hasSubagents(detail) {
		gaps = append(gaps, "该 agent 未提供 subagent 深层证据（委派描述/模型/时间/父子归属缺失），只能基于通用证据做低置信推断")
	}

	// Guarantee referential integrity: any finding ref whose ID didn't get a
	// fact becomes an explicit gap rather than a dangling citation target.
	for id := range referenced {
		if !facts.has(id) {
			gaps = append(gaps, fmt.Sprintf("Finding 引用的证据 %s 在本 bundle 中不可展开", id))
		}
	}

	b.Facts = facts.sorted()
	b.EvidenceGaps = gaps
	return b
}

// referencedTurns collects the turn indices that findings point at via a
// turn-kind EvidenceRef, keyed by the ref ID ("turn:N").
func referencedTurns(findings []analytics.Finding) map[string]int {
	out := map[string]int{}
	for _, f := range findings {
		for _, ref := range f.EvidenceRefs {
			if ref.Kind == "turn" && ref.TurnIndex != nil {
				out[ref.ID] = *ref.TurnIndex
			}
		}
	}
	return out
}

func hasSubagents(detail *model.SessionDetail) bool {
	for _, t := range detail.Turns {
		if len(t.Subagents) > 0 {
			return true
		}
	}
	return false
}

// factSet dedupes facts by ID and preserves insertion for stable output.
type factSet struct {
	byID  map[string]bool
	facts []EvidenceFact
}

func newFactSet() *factSet { return &factSet{byID: map[string]bool{}} }

func (s *factSet) add(f EvidenceFact) {
	if f.ID == "" || s.byID[f.ID] {
		return
	}
	s.byID[f.ID] = true
	s.facts = append(s.facts, f)
}

func (s *factSet) has(id string) bool { return s.byID[id] }

func (s *factSet) sorted() []EvidenceFact {
	out := append([]EvidenceFact(nil), s.facts...)
	sort.SliceStable(out, func(i, j int) bool { return factRank(out[i]) < factRank(out[j]) })
	return out
}

// factRank groups facts by kind for a readable, stable serialization order:
// session first, then metrics, turns, then reader-specific.
func factRank(f EvidenceFact) int {
	switch f.Kind {
	case "session":
		return 0
	case "metric":
		return 1
	case "turn":
		return 2
	case "subagent":
		return 3
	case "request":
		return 4
	case "tool":
		return 5
	default:
		return 6
	}
}

func truncateRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…(截断)"
}
