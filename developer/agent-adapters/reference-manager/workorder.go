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
	WorkOrderActive      = "active"             // frozen inputs still match; safe to implement
	WorkOrderStale       = "stale"              // an input changed after freezing; regenerate
	WorkOrderConsumed    = "consumed"           // main lock matches every frozen hash
	WorkOrderUnsupported = "unsupported_schema" // not schema v2; regenerate
)

// workOrderRoot is the Git-ignored checkout directory for temporary work
// orders (".runtime" is repository-wide ignored).
func workOrderRoot(checkoutDir string) string {
	return filepath.Join(checkoutDir, ".runtime", "reference-work")
}

// pendingInputs lists the logical files that need implementation work:
// captured but not accepted, or updated after acceptance.
func pendingInputs(cat *AgentCatalog, lockHashes map[string]string) []string {
	var out []string
	for name, st := range cat.Items {
		if st.NotApplicable || st.Current == nil {
			continue
		}
		lockHash := lockHashFor(lockHashes, name, st.Current.Ext)
		if lockHash == "" || lockHash != st.Current.Hash {
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

// alreadyFrozenError is returned when an active work order already freezes
// the current pending input hashes. The caller should reuse that record.
type alreadyFrozenError struct {
	Record WorkOrderRecord
}

func (e *alreadyFrozenError) Error() string {
	return fmt.Sprintf("these screenshots are already in work order %s", e.Record.ID)
}

func pendingHashes(cat *AgentCatalog, pending []string) map[string]string {
	out := map[string]string{}
	for _, name := range pending {
		st := cat.Items[name]
		if st == nil || st.Current == nil {
			continue
		}
		out[name] = st.Current.Hash
	}
	return out
}

func sameFrozenInputs(record WorkOrderRecord, hashes map[string]string) bool {
	if record.SchemaVersion != WorkOrderSchemaV2 {
		return false
	}
	if len(record.Frozen) == 0 || len(record.Frozen) != len(hashes) {
		return false
	}
	for name, hash := range hashes {
		if record.Frozen[name] != hash {
			return false
		}
	}
	return true
}

// activeWorkOrderForPending returns the latest active work order that already
// freezes exactly this pending set. A changed capture makes the old order
// stale and allows a new freeze; extra pending files are a new set.
func activeWorkOrderForPending(cat *AgentCatalog, pending []string, lockHashes map[string]string) *WorkOrderRecord {
	hashes := pendingHashes(cat, pending)
	if len(hashes) == 0 || len(hashes) != len(pending) {
		return nil
	}
	for i := len(cat.WorkOrders) - 1; i >= 0; i-- {
		rec := cat.WorkOrders[i]
		if workOrderState(rec, cat, lockHashes) != WorkOrderActive {
			continue
		}
		if sameFrozenInputs(rec, hashes) {
			return &cat.WorkOrders[i]
		}
	}
	return nil
}

// generateWorkOrder freezes the pending inputs of one Agent into a work order
// directory. The manager's boundary ends here: it never creates goals,
// branches, PRs or product-code edits. An active work order for the same
// frozen hashes is reused rather than duplicated.
func generateWorkOrder(s *Store, checkoutDir, agent string, candidates map[string]*Candidate) (*WorkOrderRecord, error) {
	s.generateMu.Lock()
	defer s.generateMu.Unlock()

	canonicalAgent, err := s.canonicalAgent(agent)
	if err != nil {
		return nil, err
	}
	cat, err := s.LoadCatalog(canonicalAgent)
	if err != nil {
		return nil, err
	}
	baseline, err := lookupBaseline(checkoutDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", baselineRef, err)
	}
	lock, err := loadAgentLock(checkoutDir, baseline.SHA, canonicalAgent)
	if err != nil {
		return nil, fmt.Errorf("read main evidence lock: %w", err)
	}
	lockHashes := lockHashesByLogical(lock)

	pending := pendingInputs(cat, lockHashes)
	if len(pending) == 0 {
		return nil, fmt.Errorf("no pending reference inputs for %s; nothing to freeze", canonicalAgent)
	}
	if existing := activeWorkOrderForPending(cat, pending, lockHashes); existing != nil {
		return nil, &alreadyFrozenError{Record: *existing}
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

	mainLock := map[string]string{}
	for _, name := range pending {
		st := cat.Items[name]
		ext := ""
		if st != nil && st.Current != nil {
			ext = st.Current.Ext
		}
		mainLock[name] = lockHashFor(lockHashes, name, ext)
	}
	relDir := filepath.Join(".runtime", "reference-work", id)
	absMD := filepath.Join(dir, "WORK_ORDER.md")
	record := &WorkOrderRecord{
		SchemaVersion:    WorkOrderSchemaV2,
		ID:               id,
		Dir:              relDir,
		CreatedAt:        nowRFC3339(),
		Agent:            canonicalAgent,
		Items:            pending,
		Features:         features,
		Frozen:           frozen,
		BaselineRef:      baseline.Ref,
		BaselineSHA:      baseline.SHA,
		MainLockHashes:   mainLock,
		PreflightCommand: fmt.Sprintf("./scripts/terminal-reference verify-work-order --work-order %s", absMD),
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
	fmt.Fprintf(&b, "- work_order_schema_version: 2\n")
	fmt.Fprintf(&b, "- Schema version: `2`\n")
	fmt.Fprintf(&b, "- Baseline ref: `%s`\n", record.BaselineRef)
	fmt.Fprintf(&b, "- Baseline commit: `%s`\n", record.BaselineSHA)
	if cat.CurrentVersion != "" {
		fmt.Fprintf(&b, "- Installed Agent version context: %s\n", cat.CurrentVersion)
	}
	fmt.Fprintf(&b, "- Preflight: `%s`\n", record.PreflightCommand)
	b.WriteString("\n## Captures in this work order\n\n")
	b.WriteString("The hashes below are the snapshot this work order covers. If any of these images change after generation, this work order is void and must be regenerated.\n\n")
	b.WriteString("| Logical file | Candidate SHA-256 | Main lock SHA-256 | Short | Features |\n|---|---|---|---|---|\n")
	for _, e := range entries {
		lockHash := record.MainLockHashes[e.logical]
		if lockHash == "" {
			lockHash = "(none)"
		}
		features := itemFeatures(e.logical)
		if len(features) == 0 {
			features = []string{"(context only)"}
		}
		fmt.Fprintf(&b, "| `%s%s` | `%s` | `%s` | `%s` | %s |\n",
			e.logical, e.rec.Ext, e.rec.Hash, lockHash, shortHash(e.rec.Hash), strings.Join(features, ", "))
	}
	if len(gaps) > 0 {
		b.WriteString("\n## Allowed gaps (no evidence, out of scope)\n\n")
		for _, g := range gaps {
			fmt.Fprintf(&b, "- `%s`\n", g)
		}
	}
	b.WriteString("\n## Boundaries\n\n")
	b.WriteString("- This work order is local development data under `.runtime/`; do not commit it or copy private context into PRs, issues or logs.\n")
	b.WriteString("- Presentation implementation happens only after the user explicitly starts the next phase.\n")
	b.WriteString("- Missing images stay missing: do not fabricate scenes or placeholder captures.\n")
	b.WriteString("- Candidate sessions and event positions are listed in `local-candidate-context/` (local only).\n")
	b.WriteString("- Old schema work orders are not accepted; regenerate with the current Reference Manager.\n")
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
func workOrderState(record WorkOrderRecord, cat *AgentCatalog, lockHashes map[string]string) string {
	if record.SchemaVersion != WorkOrderSchemaV2 {
		return WorkOrderUnsupported
	}
	consumed := 0
	for name, frozenHash := range record.Frozen {
		st := cat.Items[name]
		if st == nil || st.Current == nil {
			return WorkOrderStale
		}
		if st.Current.Hash != frozenHash {
			return WorkOrderStale
		}
		ext := st.Current.Ext
		if lockHashFor(lockHashes, name, ext) == frozenHash {
			consumed++
		}
	}
	if consumed == len(record.Frozen) && len(record.Frozen) > 0 {
		return WorkOrderConsumed
	}
	return WorkOrderActive
}
