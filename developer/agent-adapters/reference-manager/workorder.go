package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Work-order states derived by comparing frozen input hashes with the
// catalog's current captures.
const (
	WorkOrderActive   = "active"   // frozen inputs still match; safe to implement
	WorkOrderStale    = "stale"    // an input changed after freezing; regenerate
	WorkOrderConsumed = "consumed" // every frozen input is now the accepted version
)

// workOrderRoot is the Git-ignored checkout directory for temporary work
// orders (".runtime" is repository-wide ignored).
func workOrderRoot(checkoutDir string) string {
	return filepath.Join(checkoutDir, ".runtime", "reference-work")
}

// pendingInputs lists the logical files that need implementation work:
// captured but not accepted, or updated after acceptance.
func pendingInputs(cat *AgentCatalog) []string {
	var out []string
	for name, st := range cat.Items {
		if st.NotApplicable || st.Current == nil {
			continue
		}
		if st.AcceptedHash == "" || st.AcceptedHash != st.Current.Hash {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// allowedGaps lists checklist items with no usable evidence, for the work
// order's explicit "allowed missing" section.
func allowedGaps(cat *AgentCatalog, candidates map[string]*Candidate) []string {
	var out []string
	for _, item := range checklist {
		for _, slot := range item.Slots {
			st := cat.Items[slot.LogicalName]
			if st != nil && (st.Current != nil || st.NotApplicable) {
				continue
			}
			if candidates[slotItemCandidateKey(slot.LogicalName)] != nil {
				continue
			}
			out = append(out, slot.LogicalName)
		}
	}
	return out
}

// slotItemCandidateKey maps a logical file to the item used for candidate
// lookup (fold variants share the base item's candidate).
func slotItemCandidateKey(logicalName string) string {
	return logicalNameItemID[logicalName]
}

// frozenEntry pairs a pending logical file with its capture at freeze time.
type frozenEntry struct {
	logical string
	rec     *CaptureRecord
}

// workOrderTimestamp stamps work-order IDs. Wrapped so tests can force
// same-second collisions.
var workOrderTimestamp = func() string { return time.Now().UTC().Format("20060102-150405") }

// createWorkOrderDir exclusively creates the work-order directory, appending
// a numeric suffix when the one-second timestamp collides with an existing
// order. Two generations never share or overwrite a directory.
func createWorkOrderDir(checkoutDir, canonicalAgent string) (id string, dir string, err error) {
	rootDir := workOrderRoot(checkoutDir)
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return "", "", err
	}
	baseID := canonicalAgent + "-" + workOrderTimestamp()
	for attempt := 0; ; attempt++ {
		id = baseID
		if attempt > 0 {
			id = fmt.Sprintf("%s-%d", baseID, attempt+1)
		}
		dir = filepath.Join(rootDir, id)
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			return id, dir, nil
		}
		if !os.IsExist(err) {
			return "", "", err
		}
	}
}

// generateWorkOrder freezes the pending inputs of one Agent into a work order
// directory. The manager's boundary ends here: it never creates goals,
// branches, PRs or product-code edits.
func generateWorkOrder(s *Store, checkoutDir, agent string, candidates map[string]*Candidate) (*WorkOrderRecord, error) {
	canonicalAgent, err := s.canonicalAgent(agent)
	if err != nil {
		return nil, err
	}
	cat, err := s.LoadCatalog(canonicalAgent)
	if err != nil {
		return nil, err
	}
	pending := pendingInputs(cat)
	if len(pending) == 0 {
		return nil, fmt.Errorf("no pending reference inputs for %s; nothing to freeze", canonicalAgent)
	}

	id, dir, err := createWorkOrderDir(checkoutDir, canonicalAgent)
	if err != nil {
		return nil, err
	}
	assetsDir := filepath.Join(dir, "selected-reference-assets")
	contextDir := filepath.Join(dir, "local-candidate-context")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		return nil, err
	}

	frozen := map[string]string{}
	featureSet := map[string]bool{}
	var entries []frozenEntry
	for _, name := range pending {
		st := cat.Items[name]
		frozen[name] = st.Current.Hash
		entries = append(entries, frozenEntry{name, st.Current})
		for _, f := range itemFeatures(name) {
			featureSet[f] = true
		}
		// Copy the frozen input next to the work order so later development
		// reads the exact content even if the store slot advances.
		src := s.blobPath(canonicalAgent, st.Current.Hash, st.Current.Ext)
		data, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("freeze %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(assetsDir, name+st.Current.Ext), data, 0o600); err != nil {
			return nil, err
		}
	}

	features := make([]string, 0, len(featureSet))
	for f := range featureSet {
		features = append(features, f)
	}
	sort.Strings(features)

	gaps := allowedGaps(cat, candidates)

	// Candidate context stays local-only: session IDs, resume commands and
	// event positions are never committed to Git (.runtime is ignored).
	var contextLines []string
	for _, name := range pending {
		itemID := logicalNameItemID[name]
		if c := candidates[itemID]; c != nil {
			contextLines = append(contextLines, fmt.Sprintf(
				"%s:\n  session: %s\n  resume_id: %s\n  resume: %s\n  turn: %d  event: %s\n  scene: %s (%s)",
				name, c.SessionID, c.ResumeID, c.ResumeCommand, c.TurnIndex, c.EventID, c.Summary, c.Precision))
		}
	}
	if len(contextLines) > 0 {
		content := "Local-only candidate context. Never commit, copy into PRs, issues or logs.\n\n" +
			strings.Join(contextLines, "\n\n") + "\n"
		if err := os.WriteFile(filepath.Join(contextDir, "candidates.txt"), []byte(content), 0o600); err != nil {
			return nil, err
		}
	}

	record := &WorkOrderRecord{
		ID:        id,
		Dir:       filepath.Join(".runtime", "reference-work", id),
		CreatedAt: nowRFC3339(),
		Items:     pending,
		Features:  features,
		Frozen:    frozen,
	}

	md := renderWorkOrderMarkdown(canonicalAgent, record, entries, cat, gaps, candidates)
	if err := os.WriteFile(filepath.Join(dir, "WORK_ORDER.md"), []byte(md), 0o600); err != nil {
		return nil, err
	}

	err = s.UpdateCatalog(canonicalAgent, func(c *AgentCatalog) error {
		c.WorkOrders = append(c.WorkOrders, *record)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return record, nil
}

func renderWorkOrderMarkdown(agent string, record *WorkOrderRecord, entries []frozenEntry, cat *AgentCatalog, gaps []string, candidates map[string]*Candidate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Terminal reference work order: %s\n\n", agent)
	fmt.Fprintf(&b, "- Generated: %s\n", record.CreatedAt)
	fmt.Fprintf(&b, "- Work order ID: `%s`\n", record.ID)
	if cat.CurrentVersion != "" {
		fmt.Fprintf(&b, "- Installed Agent version context: %s\n", cat.CurrentVersion)
	}
	b.WriteString("\n## Frozen inputs\n\n")
	b.WriteString("Every input hash below is frozen. If any input image changes after generation, this work order is void as acceptance input and must be regenerated.\n\n")
	b.WriteString("| Logical file | Candidate hash | Accepted hash | Features |\n|---|---|---|---|\n")
	for _, e := range entries {
		accepted := cat.Items[e.logical].AcceptedHash
		if accepted == "" {
			accepted = "(none)"
		}
		features := itemFeatures(e.logical)
		if len(features) == 0 {
			features = []string{"(context only)"}
		}
		fmt.Fprintf(&b, "| `%s%s` | `%s` | `%s` | %s |\n", e.logical, e.rec.Ext, shortHash(e.rec.Hash), shortHash(accepted), strings.Join(features, ", "))
	}
	if len(gaps) > 0 {
		b.WriteString("\n## Allowed gaps (no evidence, out of scope)\n\n")
		for _, g := range gaps {
			fmt.Fprintf(&b, "- `%s`\n", g)
		}
	}
	b.WriteString("\n## Boundaries\n\n")
	b.WriteString("- This work order is local development data under `.runtime/`; do not commit it or copy private context into PRs, issues or logs.\n")
	b.WriteString("- Presentation implementation, revision bumps and delivery are out of scope here and happen only after the user explicitly starts the next phase.\n")
	b.WriteString("- Missing images stay missing: do not fabricate scenes or placeholder captures.\n")
	b.WriteString("- Candidate sessions and event positions are listed in `local-candidate-context/` (local only).\n")
	return b.String()
}

func shortHash(hash string) string {
	if hash == "" || hash == "(none)" {
		return hash
	}
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

// workOrderState compares a record's frozen inputs with the catalog.
func workOrderState(record WorkOrderRecord, cat *AgentCatalog) string {
	consumed := 0
	for name, frozenHash := range record.Frozen {
		st := cat.Items[name]
		if st == nil || st.Current == nil {
			return WorkOrderStale
		}
		if st.AcceptedHash == frozenHash && st.Current.Hash == frozenHash {
			consumed++
			continue
		}
		if st.Current.Hash != frozenHash {
			return WorkOrderStale
		}
	}
	if consumed == len(record.Frozen) && len(record.Frozen) > 0 {
		return WorkOrderConsumed
	}
	return WorkOrderActive
}
