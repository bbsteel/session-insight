package collaboration

import (
	"fmt"
	"sort"
	"strings"
)

// IssueCode identifies one contract violation or quarantine decision.
// Codes are stable machine values; conformance assertions match on them.
type IssueCode string

const (
	// IssueMissingField: a required field is empty.
	IssueMissingField IssueCode = "missing_field"
	// IssueInvalidStatus: an invocation status is not a known value.
	IssueInvalidStatus IssueCode = "invalid_status"
	// IssueInvalidExecutionMode: a delegation execution mode is unknown.
	IssueInvalidExecutionMode IssueCode = "invalid_execution_mode"
	// IssueInvalidEvidence: an evidence state is unknown, or a non-exact
	// fact has no reason code.
	IssueInvalidEvidence IssueCode = "invalid_evidence"
	// IssueNoRoot: the graph has no deterministic root invocation.
	IssueNoRoot IssueCode = "no_root"
	// IssueDuplicateInvocation: two invocations share one ID; the first
	// (in graph order) is kept.
	IssueDuplicateInvocation IssueCode = "duplicate_invocation"
	// IssueDuplicateDelegation: two delegations share one ID; the first
	// (sorted by ID, then parent/child) is kept.
	IssueDuplicateDelegation IssueCode = "duplicate_delegation"
	// IssueRootHasParent: a delegation names the root invocation as its
	// child; the delegation is quarantined.
	IssueRootHasParent IssueCode = "root_has_parent"
	// IssueSelfLink: a delegation's parent equals its child; the
	// delegation is quarantined.
	IssueSelfLink IssueCode = "self_link"
	// IssueUnknownChild: a delegation references a child invocation that
	// does not exist; the delegation is quarantined.
	IssueUnknownChild IssueCode = "unknown_child"
	// IssueMissingParent: a delegation references a parent invocation that
	// does not exist. The child attaches to the Unlinked child Agents
	// group; its transcript is preserved.
	IssueMissingParent IssueCode = "missing_parent"
	// IssueDuplicateRelation: more than one delegation records the same
	// parent-child pair. The first (sorted by delegation ID) is canonical;
	// the rest are quarantined but preserved on the graph.
	IssueDuplicateRelation IssueCode = "duplicate_relation"
	// IssueMultipleParents: a child has delegations from more than one
	// distinct parent. V1 selects one canonical parent (first by sorted
	// delegation ID); the extra evidence is preserved, not quarantined.
	IssueMultipleParents IssueCode = "multiple_parents"
	// IssueCycle: a delegation would close a causal cycle; it is
	// quarantined.
	IssueCycle IssueCode = "cycle"
)

// Issue is one validation finding. InvocationID and DelegationID identify
// the offending record when applicable; Field names an offending field for
// field-level findings.
type Issue struct {
	Code         IssueCode `json:"code"`
	InvocationID string    `json:"invocation_id,omitempty"`
	DelegationID string    `json:"delegation_id,omitempty"`
	Field        string    `json:"field,omitempty"`
	Detail       string    `json:"detail"`
}

// Validation is the result of validating and canonically projecting one
// graph. The graph itself is never mutated: quarantined delegations and
// extra relation evidence stay on the graph.
type Validation struct {
	// Issues lists every finding in deterministic order.
	Issues []Issue `json:"issues"`
	// RootID is the deterministic root invocation ID, empty when no root
	// exists.
	RootID string `json:"root_id,omitempty"`
	// CanonicalParent maps each linked child invocation ID to the ID of
	// its canonical delegation. V1 has exactly one canonical parent per
	// child.
	CanonicalParent map[string]string `json:"canonical_parent,omitempty"`
	// Unlinked lists invocation IDs (sorted) that attach to the
	// UnlinkedGroupLabel group because no canonical parent exists. Their
	// transcripts are preserved on the graph.
	Unlinked []string `json:"unlinked,omitempty"`
	// Quarantined lists delegation IDs (sorted) excluded from the
	// canonical projection: self-links, cycles, duplicate relations,
	// unknown endpoints, and parent links into the root.
	Quarantined []string `json:"quarantined,omitempty"`
}

// OK reports whether the graph has no findings.
func (v Validation) OK() bool {
	return len(v.Issues) == 0
}

// Has reports whether any finding carries the given code.
func (v Validation) Has(code IssueCode) bool {
	for _, i := range v.Issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

// Validate checks one graph against the frozen contract rules and derives
// the canonical projection. It is pure and deterministic: the same graph
// always produces the same Validation.
func Validate(g *CollaborationGraph) Validation {
	v := Validation{CanonicalParent: map[string]string{}}

	// --- Field-level checks -------------------------------------------------
	checkFact := func(f FactEvidence, owner, field string) {
		if !isKnownEvidenceState(f.State) {
			v.Issues = append(v.Issues, Issue{
				Code:   IssueInvalidEvidence,
				Field:  field,
				Detail: fmt.Sprintf("%s: unknown evidence state %q", owner, f.State),
			})
			return
		}
		if f.State != EvidenceExact && strings.TrimSpace(f.ReasonCode) == "" {
			v.Issues = append(v.Issues, Issue{
				Code:   IssueInvalidEvidence,
				Field:  field,
				Detail: fmt.Sprintf("%s: non-exact fact %q requires a reason code", owner, f.State),
			})
		}
	}
	checkAnchor := func(a *SourceAnchor, owner, field string) {
		if a == nil {
			return
		}
		if !validIDComponent(a.AgentType) || !validIDComponent(a.SessionID) {
			v.Issues = append(v.Issues, Issue{
				Code:   IssueMissingField,
				Field:  field,
				Detail: owner + ": anchor requires agent_type and session_id",
			})
		}
		checkFact(a.Precision, owner, field+".precision")
	}

	checkFact(g.Completeness, "graph", "completeness")

	invByID := map[string]int{}
	for i, inv := range g.Invocations {
		owner := fmt.Sprintf("invocation %q", inv.ID)
		if !validIDComponent(inv.ID) {
			v.Issues = append(v.Issues, Issue{Code: IssueMissingField, Field: "id",
				Detail: fmt.Sprintf("invocation at index %d: id is required", i)})
		}
		if !validIDComponent(inv.DisplayName) {
			v.Issues = append(v.Issues, Issue{Code: IssueMissingField, InvocationID: inv.ID, Field: "display_name",
				Detail: owner + ": display_name is required (synthesize one from role/tool label when the source has none)"})
		}
		if !validIDComponent(inv.AgentType) {
			v.Issues = append(v.Issues, Issue{Code: IssueMissingField, InvocationID: inv.ID, Field: "agent_type",
				Detail: owner + ": agent_type is required"})
		}
		if !IsKnownStatus(inv.Status) {
			v.Issues = append(v.Issues, Issue{Code: IssueInvalidStatus, InvocationID: inv.ID, Field: "status",
				Detail: fmt.Sprintf("%s: unknown status %q (unknown is first-class; never omit status)", owner, inv.Status)})
		}
		checkFact(inv.TimePrecision, owner, "time_precision")
		checkFact(inv.ContentPrecision, owner, "content_precision")
		if inv.BackingSession != nil &&
			(!validIDComponent(inv.BackingSession.AgentType) || !validIDComponent(inv.BackingSession.SessionID)) {
			v.Issues = append(v.Issues, Issue{Code: IssueMissingField, InvocationID: inv.ID, Field: "backing_session",
				Detail: owner + ": backing_session requires agent_type and session_id"})
		}
		if !validIDComponent(inv.SourceIdentity.NativeID) {
			v.Issues = append(v.Issues, Issue{Code: IssueMissingField, InvocationID: inv.ID, Field: "source_identity.native_id",
				Detail: owner + ": source_identity.native_id is required for diagnostics and conformance"})
		}
		if _, dup := invByID[inv.ID]; dup {
			v.Issues = append(v.Issues, Issue{Code: IssueDuplicateInvocation, InvocationID: inv.ID,
				Detail: owner + ": duplicate invocation ID; first occurrence kept"})
			continue
		}
		invByID[inv.ID] = i
	}

	// --- Root ---------------------------------------------------------------
	rootID := RootInvocationID(g.RootAgentType, g.RootSessionID)
	if _, ok := invByID[rootID]; ok {
		v.RootID = rootID
	} else {
		v.Issues = append(v.Issues, Issue{Code: IssueNoRoot,
			Detail: fmt.Sprintf("graph %s/%s has no deterministic root invocation %q",
				g.RootAgentType, g.RootSessionID, rootID)})
	}

	// --- Delegation edges, deterministic order ------------------------------
	sorted := make([]Delegation, len(g.Delegations))
	copy(sorted, g.Delegations)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].ID != sorted[j].ID {
			return sorted[i].ID < sorted[j].ID
		}
		if sorted[i].ParentInvocationID != sorted[j].ParentInvocationID {
			return sorted[i].ParentInvocationID < sorted[j].ParentInvocationID
		}
		return sorted[i].ChildInvocationID < sorted[j].ChildInvocationID
	})

	seenDelegationIDs := map[string]bool{}
	seenPairs := map[string]string{}   // parent+"\x00"+child -> first delegation ID
	parentChain := map[string]string{} // child invocation ID -> canonical parent invocation ID
	quarantined := map[string]bool{}

	for _, d := range sorted {
		owner := fmt.Sprintf("delegation %q", d.ID)
		if !validIDComponent(d.ID) {
			v.Issues = append(v.Issues, Issue{Code: IssueMissingField, Field: "id",
				Detail: "delegation: id is required (derive it from parent and child invocation IDs)"})
		}
		if !IsKnownExecutionMode(d.ExecutionMode) {
			v.Issues = append(v.Issues, Issue{Code: IssueInvalidExecutionMode, DelegationID: d.ID, Field: "execution_mode",
				Detail: fmt.Sprintf("%s: unknown execution_mode %q", owner, d.ExecutionMode)})
		}
		checkFact(d.Evidence.Trigger, owner, "evidence.trigger")
		checkFact(d.Evidence.Timing, owner, "evidence.timing")
		checkFact(d.Evidence.Task, owner, "evidence.task")
		checkFact(d.Evidence.Result, owner, "evidence.result")
		checkAnchor(d.Trigger, owner, "trigger")
		checkAnchor(d.Result, owner, "result")

		if seenDelegationIDs[d.ID] {
			v.Issues = append(v.Issues, Issue{Code: IssueDuplicateDelegation, DelegationID: d.ID,
				Detail: owner + ": duplicate delegation ID; first occurrence kept"})
			quarantined[d.ID] = true
			continue
		}
		seenDelegationIDs[d.ID] = true

		if _, ok := invByID[d.ChildInvocationID]; !ok {
			v.Issues = append(v.Issues, Issue{Code: IssueUnknownChild, DelegationID: d.ID, InvocationID: d.ChildInvocationID,
				Detail: fmt.Sprintf("%s: child invocation %q does not exist", owner, d.ChildInvocationID)})
			quarantined[d.ID] = true
			continue
		}
		if d.ParentInvocationID == d.ChildInvocationID {
			v.Issues = append(v.Issues, Issue{Code: IssueSelfLink, DelegationID: d.ID, InvocationID: d.ChildInvocationID,
				Detail: owner + ": parent and child are the same invocation; quarantined"})
			quarantined[d.ID] = true
			continue
		}
		if d.ChildInvocationID == v.RootID {
			v.Issues = append(v.Issues, Issue{Code: IssueRootHasParent, DelegationID: d.ID, InvocationID: d.ChildInvocationID,
				Detail: owner + ": the root invocation cannot be a child; quarantined"})
			quarantined[d.ID] = true
			continue
		}
		if _, ok := invByID[d.ParentInvocationID]; !ok {
			v.Issues = append(v.Issues, Issue{Code: IssueMissingParent, DelegationID: d.ID, InvocationID: d.ChildInvocationID,
				Detail: fmt.Sprintf("%s: parent invocation %q is missing; child attaches to %q",
					owner, d.ParentInvocationID, UnlinkedGroupLabel)})
			continue
		}
		pair := d.ParentInvocationID + "\x00" + d.ChildInvocationID
		if first, dup := seenPairs[pair]; dup {
			v.Issues = append(v.Issues, Issue{Code: IssueDuplicateRelation, DelegationID: d.ID, InvocationID: d.ChildInvocationID,
				Detail: fmt.Sprintf("%s: duplicate relation evidence for the pair first recorded by %q; quarantined, evidence preserved", owner, first)})
			quarantined[d.ID] = true
			continue
		}
		seenPairs[pair] = d.ID
		if _, has := v.CanonicalParent[d.ChildInvocationID]; has {
			v.Issues = append(v.Issues, Issue{Code: IssueMultipleParents, DelegationID: d.ID, InvocationID: d.ChildInvocationID,
				Detail: fmt.Sprintf("%s: child already has canonical parent via %q; extra evidence preserved, not canonical", owner, v.CanonicalParent[d.ChildInvocationID])})
			continue
		}
		if createsCycle(parentChain, d.ParentInvocationID, d.ChildInvocationID) {
			v.Issues = append(v.Issues, Issue{Code: IssueCycle, DelegationID: d.ID, InvocationID: d.ChildInvocationID,
				Detail: owner + ": accepting this delegation would close a causal cycle; quarantined"})
			quarantined[d.ID] = true
			continue
		}
		parentChain[d.ChildInvocationID] = d.ParentInvocationID
		v.CanonicalParent[d.ChildInvocationID] = d.ID
	}

	// --- Unlinked children ----------------------------------------------------
	for id := range invByID {
		if id == v.RootID {
			continue
		}
		if _, linked := v.CanonicalParent[id]; !linked {
			v.Unlinked = append(v.Unlinked, id)
		}
	}
	sort.Strings(v.Unlinked)
	for id := range quarantined {
		v.Quarantined = append(v.Quarantined, id)
	}
	sort.Strings(v.Quarantined)

	sort.SliceStable(v.Issues, func(i, j int) bool {
		a, b := v.Issues[i], v.Issues[j]
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		if a.DelegationID != b.DelegationID {
			return a.DelegationID < b.DelegationID
		}
		if a.InvocationID != b.InvocationID {
			return a.InvocationID < b.InvocationID
		}
		return a.Field < b.Field
	})
	return v
}

// createsCycle reports whether adding the causal edge parent→child would
// close a cycle: it would if child is already an ancestor of parent.
func createsCycle(parentChain map[string]string, parent, child string) bool {
	for cur := parent; ; {
		if cur == child {
			return true
		}
		next, ok := parentChain[cur]
		if !ok {
			return false
		}
		cur = next
	}
}

func isKnownEvidenceState(s EvidenceState) bool {
	switch s {
	case EvidenceExact, EvidenceEstimated, EvidenceMissing, EvidenceNotApplicable, EvidenceUnsupported:
		return true
	default:
		return false
	}
}
