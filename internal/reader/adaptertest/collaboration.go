package adaptertest

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/model"
)

// Shared conformance skeleton for the collaboration contract
// (internal/collaboration). It is imported only from *_test.go files, like
// the rest of adaptertest.
//
// The skeleton proves, for any adapter implementing the collaboration
// reader interface:
//
//   - identity stability: two independent parses produce byte-identical
//     invocation and delegation identity material (the V1 conformance
//     floor; resume/compaction and partial-write fixtures extend the same
//     assertions when they exist);
//   - contract validity: collaboration.Validate finds no issues;
//   - declared expectations: root shape, minimum children, backing-Session
//     rules.
//
// Failure messages name the missing contract requirement so an adapter
// author can act without reading the suite source.

// CollaborationReader is the structural optional interface for reading the
// normalized collaboration graph of one root Session (matches the
// production reader.CollaborationReader).
type CollaborationReader interface {
	ReadCollaboration(ctx context.Context, root model.Session) (collaboration.CollaborationGraph, error)
}

// RequireCollaborationReader type-asserts collaboration read support.
func RequireCollaborationReader(t *testing.T, r Reader) CollaborationReader {
	t.Helper()
	cr, ok := r.(CollaborationReader)
	if !ok {
		t.Fatalf("reader %T does not implement ReadCollaboration; the collaboration contract "+
			"requires adapters declaring subtasks=exact to expose the normalized graph", r)
	}
	return cr
}

// CollaborationExpect declares what one fixture's graph must satisfy.
type CollaborationExpect struct {
	// RootSession is the root Session the graph is read for.
	RootSession model.Session
	// MinChildren is the minimum number of non-root invocations.
	MinChildren int
	// RequireBackingSession requires every non-root invocation to carry a
	// BackingSessionRef (standalone-child archetype).
	RequireBackingSession bool
	// ForbidBackingSession requires no non-root invocation to carry a
	// BackingSessionRef (embedded / lifecycle-only archetypes).
	ForbidBackingSession bool
}

// CheckCollaborationGraph returns one message per unmet contract
// requirement. An empty result means the graph satisfies the contract.
// Messages name the requirement, not just the symptom.
func CheckCollaborationGraph(g collaboration.CollaborationGraph) []string {
	var problems []string
	v := collaboration.Validate(&g)
	for _, issue := range v.Issues {
		problems = append(problems, fmt.Sprintf(
			"collaboration contract violation [%s]: %s", issue.Code, issue.Detail))
	}
	rootWant := collaboration.RootInvocationID(g.RootAgentType, g.RootSessionID)
	if v.RootID == "" {
		problems = append(problems, fmt.Sprintf(
			"collaboration contract requires exactly one deterministic root invocation %q", rootWant))
	}
	return problems
}

// CheckCollaborationExpect returns one message per unmet declared
// expectation for an already-valid graph.
func CheckCollaborationExpect(g collaboration.CollaborationGraph, exp CollaborationExpect) []string {
	var problems []string
	children := 0
	for _, inv := range g.Invocations {
		if inv.ID == collaboration.RootInvocationID(g.RootAgentType, g.RootSessionID) {
			continue
		}
		children++
		if exp.RequireBackingSession && inv.BackingSession == nil {
			problems = append(problems, fmt.Sprintf(
				"collaboration expectation: invocation %q is a standalone child and must carry a BackingSessionRef", inv.ID))
		}
		if exp.ForbidBackingSession && inv.BackingSession != nil {
			problems = append(problems, fmt.Sprintf(
				"collaboration expectation: invocation %q is embedded or lifecycle-only and must not carry a BackingSessionRef", inv.ID))
		}
	}
	if children < exp.MinChildren {
		problems = append(problems, fmt.Sprintf(
			"collaboration expectation: fixture declares at least %d child invocations, graph has %d",
			exp.MinChildren, children))
	}
	return problems
}

// identitySignature captures the identity material that must be stable
// across two independent parses.
func identitySignature(g collaboration.CollaborationGraph) string {
	var b strings.Builder
	invIDs := make([]string, 0, len(g.Invocations))
	for _, inv := range g.Invocations {
		invIDs = append(invIDs, inv.ID+"\x00"+inv.SourceIdentity.Kind+"\x00"+inv.SourceIdentity.NativeID)
	}
	sort.Strings(invIDs)
	for _, id := range invIDs {
		b.WriteString(id)
		b.WriteString("\x01")
	}
	delIDs := make([]string, 0, len(g.Delegations))
	for _, d := range g.Delegations {
		delIDs = append(delIDs, d.ID+"\x00"+d.ParentInvocationID+"\x00"+d.ChildInvocationID)
	}
	sort.Strings(delIDs)
	for _, id := range delIDs {
		b.WriteString(id)
		b.WriteString("\x02")
	}
	return b.String()
}

// AssertCollaborationGraph fails the test on any unmet contract
// requirement, with messages that name the requirement.
func AssertCollaborationGraph(t *testing.T, g collaboration.CollaborationGraph) {
	t.Helper()
	for _, p := range CheckCollaborationGraph(g) {
		t.Error(p)
	}
}

// AssertCollaborationTwoParseStability reads the graph twice through
// independent calls and requires identical identity material: invocation
// IDs, source identities, delegation IDs, and endpoints. This is the V1
// conformance floor; adapters remain responsible for ordering stability of
// their own lists (checked here via full graph equality).
func AssertCollaborationTwoParseStability(t *testing.T, cr CollaborationReader, root model.Session) {
	t.Helper()
	first, err := cr.ReadCollaboration(context.Background(), root)
	if err != nil {
		t.Fatalf("ReadCollaboration first parse: %v", err)
	}
	second, err := cr.ReadCollaboration(context.Background(), root)
	if err != nil {
		t.Fatalf("ReadCollaboration second parse: %v", err)
	}
	if identitySignature(first) != identitySignature(second) {
		t.Fatalf("collaboration identity stability (two-parse) violated: invocation or delegation "+
			"identity material changed between parses\nfirst:  %s\nsecond: %s",
			identitySignature(first), identitySignature(second))
	}
	if !reflect.DeepEqual(first.Invocations, second.Invocations) ||
		!reflect.DeepEqual(first.Delegations, second.Delegations) {
		t.Fatal("collaboration two-parse stability violated: invocations or delegations differ " +
			"between parses beyond identity material (ordering and content must be deterministic)")
	}
}

// RunCollaboration is the shared collaboration conformance entry point: two
// parses for identity stability, full contract validation on the resulting
// graph, and the caller-declared expectations. Call it from the adapter's
// conformance test with a sanitized fixture that covers its subtasks
// declaration.
func RunCollaboration(t *testing.T, r Reader, exp CollaborationExpect) {
	t.Helper()
	cr := RequireCollaborationReader(t, r)
	AssertCollaborationTwoParseStability(t, cr, exp.RootSession)
	g, err := cr.ReadCollaboration(context.Background(), exp.RootSession)
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	AssertCollaborationGraph(t, g)
	for _, p := range CheckCollaborationExpect(g, exp) {
		t.Error(p)
	}
}
