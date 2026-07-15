package insight

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/bbsteel/session-insight/internal/analytics"
)

// SourceFingerprint is a stable hash of everything that, if changed, should
// mark a saved Insight stale: the finding rule version, the bundle schema, the
// Skill (prompt) version, and the actual evidence the model saw. Two bundles
// with identical evidence and versions hash equal; any change to a fact ID or
// statement, or a version bump, changes the hash.
func SourceFingerprint(b Bundle) string {
	type factKey struct {
		ID        string `json:"id"`
		Statement string `json:"statement"`
	}
	facts := make([]factKey, 0, len(b.Facts))
	for _, f := range b.Facts {
		facts = append(facts, factKey{ID: f.ID, Statement: f.Statement})
	}
	sort.Slice(facts, func(i, j int) bool { return facts[i].ID < facts[j].ID })

	codes := make([]string, 0, len(b.Findings))
	for _, f := range b.Findings {
		codes = append(codes, f.Code)
	}
	sort.Strings(codes)

	payload := struct {
		FindingsVersion int       `json:"findings_version"`
		BundleSchema    int       `json:"bundle_schema"`
		PromptVersion   string    `json:"prompt_version"`
		FindingCodes    []string  `json:"finding_codes"`
		Facts           []factKey `json:"facts"`
	}{
		FindingsVersion: analytics.FindingsVersion,
		BundleSchema:    b.SchemaVersion,
		PromptVersion:   PromptVersion,
		FindingCodes:    codes,
		Facts:           facts,
	}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

// StoredMetadata is the JSON persisted in ai_generations.metadata for an
// insight: the validated structured output, the minimal cited-evidence
// projection (so an old Insight can still explain its dependencies after the
// session becomes stale, without storing the whole bundle), and any validation
// warnings surfaced during parsing.
type StoredMetadata struct {
	Output        *Output        `json:"output"`
	CitedEvidence []EvidenceFact `json:"cited_evidence,omitempty"`
	EvidenceGaps  []string       `json:"evidence_gaps,omitempty"`
	Warnings      []string       `json:"warnings,omitempty"`
	// ParseFailed marks a generation whose model output was not parseable; the
	// raw text is kept in content as escaped plain text for display.
	ParseFailed bool `json:"parse_failed,omitempty"`
}

// BuildMetadata assembles the persisted metadata for a validated output.
func BuildMetadata(out *Output, b Bundle, warnings []string) StoredMetadata {
	return StoredMetadata{
		Output:        out,
		CitedEvidence: CitedEvidence(out, b),
		EvidenceGaps:  out.EvidenceGaps,
		Warnings:      warnings,
	}
}
