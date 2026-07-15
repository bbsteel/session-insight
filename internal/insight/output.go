package insight

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// OutputSchemaVersion is the structured-output contract version the server
// supports. A model output declaring a different schema_version is rejected.
const OutputSchemaVersion = 1

// Output is the structured Deep Insight the model must return as a single JSON
// object. Human-readable Markdown is generated server-side from these validated
// fields; the model's free text is never trusted for display.
type Output struct {
	SchemaVersion int       `json:"schema_version"`
	Summary       string    `json:"summary"`
	Insights      []Insight `json:"insights"`
	EvidenceGaps  []string  `json:"evidence_gaps,omitempty"`
}

type Insight struct {
	Title              string        `json:"title"`
	FindingCodes       []string      `json:"finding_codes,omitempty"`
	Confidence         string        `json:"confidence"`
	Cause              Cause         `json:"cause"`
	Impact             Impact        `json:"impact"`
	CounterEvidenceIDs []string      `json:"counter_evidence_ids,omitempty"`
	Alternatives       []Alternative `json:"alternatives,omitempty"`
	Recommendations    []string      `json:"recommendations,omitempty"`
	Caveats            []string      `json:"caveats,omitempty"`
}

type Cause struct {
	Statement       string   `json:"statement"`
	EpistemicStatus string   `json:"epistemic_status"`
	CausalStrength  string   `json:"causal_strength"`
	EvidenceIDs     []string `json:"evidence_ids,omitempty"`
	Confounders     []string `json:"confounders,omitempty"`
}

type Impact struct {
	Statement   string   `json:"statement"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

type Alternative struct {
	Statement           string   `json:"statement"`
	EvidenceIDs         []string `json:"evidence_ids,omitempty"`
	OpposingEvidenceIDs []string `json:"opposing_evidence_ids,omitempty"`
	Assessment          string   `json:"assessment"`
}

var (
	validConfidence = map[string]bool{"high": true, "medium": true, "low": true}
	validEpistemic  = map[string]bool{"observed": true, "inferred": true, "unknown": true}
	validStrength   = map[string]bool{"none": true, "weak": true, "moderate": true, "strong": true}
)

// ErrUnparseable means the model output was not a single JSON object. Callers
// keep the raw text as an escaped plain-text fallback rather than discarding
// the generation.
var ErrUnparseable = errors.New("insight output is not a valid JSON object")

// ParseAndValidate turns raw model text into a validated Output. Deterministic
// validation covers schema version, enums, finding-code membership, and
// evidence-ID existence/referential integrity: an insight that cites a
// nonexistent evidence_id or an unsupported enum is dropped and a warning is
// recorded, never silently kept. A structural parse failure returns
// ErrUnparseable so the caller can fall back to escaped raw text.
func ParseAndValidate(raw string, b Bundle) (*Output, []string, error) {
	trimmed := stripCodeFence(strings.TrimSpace(raw))
	if trimmed == "" || trimmed[0] != '{' {
		return nil, nil, ErrUnparseable
	}
	var out Output
	dec := json.NewDecoder(strings.NewReader(trimmed))
	if err := dec.Decode(&out); err != nil {
		return nil, nil, ErrUnparseable
	}
	if out.SchemaVersion != OutputSchemaVersion {
		return nil, nil, fmt.Errorf("unsupported schema_version %d", out.SchemaVersion)
	}

	validIDs := map[string]bool{}
	for _, f := range b.Facts {
		validIDs[f.ID] = true
	}
	validCodes := map[string]bool{}
	for _, f := range b.Findings {
		validCodes[f.Code] = true
	}

	var warnings []string
	kept := make([]Insight, 0, len(out.Insights))
	for i, ins := range out.Insights {
		if w, ok := validateInsight(&ins, i, validIDs, validCodes); ok {
			kept = append(kept, ins)
			warnings = append(warnings, w...)
		} else {
			warnings = append(warnings, w...)
		}
	}
	out.Insights = kept
	return &out, warnings, nil
}

// validateInsight returns collected warnings and whether the insight survives.
// It drops the insight when a required enum is invalid or when any cited
// evidence_id does not exist in the bundle.
func validateInsight(ins *Insight, idx int, validIDs, validCodes map[string]bool) ([]string, bool) {
	label := fmt.Sprintf("insight[%d] %q", idx, ins.Title)
	var warnings []string

	if !validConfidence[ins.Confidence] {
		return append(warnings, label+": 非法 confidence，已丢弃"), false
	}
	if !validEpistemic[ins.Cause.EpistemicStatus] {
		return append(warnings, label+": 非法 epistemic_status，已丢弃"), false
	}
	if !validStrength[ins.Cause.CausalStrength] {
		return append(warnings, label+": 非法 causal_strength，已丢弃"), false
	}

	// finding_codes not from input are filtered with a warning (non-fatal).
	filteredCodes := ins.FindingCodes[:0:0]
	for _, c := range ins.FindingCodes {
		if validCodes[c] {
			filteredCodes = append(filteredCodes, c)
		} else {
			warnings = append(warnings, fmt.Sprintf("%s: finding_code %q 不在输入中，已剔除", label, c))
		}
	}
	ins.FindingCodes = filteredCodes

	// Any cited evidence_id must exist. A dangling citation invalidates the
	// insight — we do not present a cause backed by evidence that isn't there.
	for _, id := range allCitedIDs(ins) {
		if !validIDs[id] {
			return append(warnings, fmt.Sprintf("%s: 引用了不存在的 evidence_id %q，已丢弃", label, id)), false
		}
	}
	return warnings, true
}

func allCitedIDs(ins *Insight) []string {
	var ids []string
	ids = append(ids, ins.Cause.EvidenceIDs...)
	ids = append(ids, ins.Impact.EvidenceIDs...)
	ids = append(ids, ins.CounterEvidenceIDs...)
	for _, a := range ins.Alternatives {
		ids = append(ids, a.EvidenceIDs...)
		ids = append(ids, a.OpposingEvidenceIDs...)
	}
	return ids
}

// stripCodeFence removes a wrapping ```...``` fence if the model added one
// despite instructions, so an otherwise-valid JSON object still parses.
func stripCodeFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[nl+1:]
	}
	if end := strings.LastIndex(s, "```"); end >= 0 {
		s = s[:end]
	}
	return strings.TrimSpace(s)
}
