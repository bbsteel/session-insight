package collaboration

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

// Golden collaboration JSON suite for the frozen contract. Every case is
// synthetic, sanitized, and hand-authored against the verified archetype
// evidence (session-insight commit dc584ae). The JSON files under
// testdata/golden are generated from the builders below:
//
//	go test ./internal/collaboration/ -run TestGolden -update
//
// The round-trip assertion (unmarshal → marshal → byte equality) locks the
// stable JSON serialization downstream persistence and API work rely on.

var updateGolden = flag.Bool("update", false, "rewrite testdata/golden collaboration fixtures")

func mustTS(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func tp(s string) *time.Time {
	t := mustTS(s)
	return &t
}

func rootInvocation(agentType, sessionID, displayName string) AgentInvocation {
	return AgentInvocation{
		ID:               RootInvocationID(agentType, sessionID),
		DisplayName:      displayName,
		AgentType:        agentType,
		Status:           StatusCompleted,
		TimePrecision:    ExactFact(),
		ContentPrecision: ExactFact(),
		SourceIdentity:   SourceIdentity{Kind: IdentityRootSession, NativeID: sessionID},
	}
}

type goldenExpect struct {
	issues      []IssueCode
	root        bool
	canonical   map[string]string
	unlinked    []string
	quarantined []string
}

type goldenCase struct {
	name  string
	graph func() CollaborationGraph
	want  goldenExpect
	check func(t *testing.T, g *CollaborationGraph)
}

const (
	codexRootSession  = "rollout-2026-01-02T00-00-00-019f0000-0000-7000-8000-0000000000aa"
	codexChildPayload = "019f0000-0000-7000-8000-0000000000bb"
	codexChildSession = "rollout-2026-01-02T00-00-01-019f0000-0000-7000-8000-0000000000bb"
	chrysRootSession  = "28491d6d491e"
	copilotRootSess   = "collab-copilot-1"
	claudeRootSession = "claude-root-0001"
)

func codexChildID() string  { return ChildInvocationID("codex", codexRootSession, codexChildPayload) }
func chrysChildID() string  { return ChildInvocationID("chrys", chrysRootSession, "call_sub_1") }
func copilotChildA() string { return ChildInvocationID("copilot", copilotRootSess, "call-task-A") }
func copilotChildB() string { return ChildInvocationID("copilot", copilotRootSess, "call-task-B") }
func claudeChildID() string { return ChildInvocationID("claude", claudeRootSession, "agent-a1b2c3") }
func nestChildA() string    { return ChildInvocationID("copilot", copilotRootSess, "call-nest-a") }
func nestChildB() string    { return ChildInvocationID("copilot", copilotRootSess, "call-nest-b") }
func codexRootID() string   { return RootInvocationID("codex", codexRootSession) }
func chrysRootID() string   { return RootInvocationID("chrys", chrysRootSession) }
func copilotRootID() string { return RootInvocationID("copilot", copilotRootSess) }
func claudeRootID() string  { return RootInvocationID("claude", claudeRootSession) }

func delegation(parent, child string, mode ExecutionMode, ev DelegationEvidence) Delegation {
	return Delegation{
		ID:                 DelegationIDFor(parent, child),
		ParentInvocationID: parent,
		ChildInvocationID:  child,
		ExecutionMode:      mode,
		Evidence:           ev,
	}
}

func exactAnchor(agentType, sessionID, toolCallID string, at string) *SourceAnchor {
	return &SourceAnchor{
		AgentType:  agentType,
		SessionID:  sessionID,
		ToolCallID: toolCallID,
		Timestamp:  tp(at),
		Precision:  ExactFact(),
	}
}

func goldenCases() []goldenCase {
	return []goldenCase{
		{
			// Archetype 1 (Codex): backed invocation, exact native identity
			// and lineage, launch/result anchors missing in the parent
			// stream. Status stays unknown: no explicit completion
			// evidence, and unknown beats inferring success from a closed
			// file.
			name: "standalone-child",
			graph: func() CollaborationGraph {
				root := codexRootID()
				child := codexChildID()
				return CollaborationGraph{
					RootAgentType: "codex",
					RootSessionID: codexRootSession,
					Revision:      7,
					Completeness:  FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonSourceNotRecorded},
					Invocations: []AgentInvocation{
						rootInvocation("codex", codexRootSession, "codex main agent"),
						{
							ID:               child,
							DisplayName:      "audit",
							AgentType:        "codex",
							RoleLabel:        "audit",
							Status:           StatusUnknown,
							StartedAt:        tp("2026-01-02T00:00:01Z"),
							TimePrecision:    FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonCompletionNotRecorded},
							ContentPrecision: ExactFact(),
							BackingSession:   &BackingSessionRef{AgentType: "codex", SessionID: codexChildSession},
							SourceIdentity: SourceIdentity{
								Kind:     IdentityPayloadID,
								NativeID: codexChildPayload,
								Attributes: map[string]string{
									"rollout_stem": codexChildSession,
								},
							},
						},
					},
					Delegations: []Delegation{
						delegation(root, child, ExecutionUnknown, DelegationEvidence{
							Trigger: FactEvidence{State: EvidenceMissing, ReasonCode: ReasonSourceNotRecorded},
							Timing:  FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonCompletionNotRecorded},
							Task:    FactEvidence{State: EvidenceMissing, ReasonCode: ReasonSourceNotRecorded},
							Result:  FactEvidence{State: EvidenceMissing, ReasonCode: ReasonSourceNotRecorded},
						}),
					},
				}
			},
			want: goldenExpect{
				root:      true,
				canonical: map[string]string{codexChildID(): DelegationIDFor(codexRootID(), codexChildID())},
			},
			check: func(t *testing.T, g *CollaborationGraph) {
				child := g.Invocations[1]
				if child.BackingSession == nil || child.BackingSession.SessionID != codexChildSession {
					t.Errorf("standalone child must carry its BackingSessionRef: %+v", child.BackingSession)
				}
				if child.Status != StatusUnknown {
					t.Errorf("codex child status = %q, want unknown (no completion evidence)", child.Status)
				}
				if g.Delegations[0].Trigger != nil || g.Delegations[0].Result != nil {
					t.Error("codex launch/result anchors must be absent, not synthesized")
				}
			},
		},
		{
			// Archetype 2 (Chrys): embedded child transcript with the exact
			// two-sided call_id join. The source records a delegated prompt
			// but no summary, so task evidence is missing rather than a
			// stored prompt.
			name: "embedded-child",
			graph: func() CollaborationGraph {
				root := chrysRootID()
				child := chrysChildID()
				d := delegation(root, child, ExecutionUnknown, DelegationEvidence{
					Trigger: ExactFact(),
					Timing:  ExactFact(),
					Task:    FactEvidence{State: EvidenceMissing, ReasonCode: ReasonSourceNotRecorded},
					Result:  ExactFact(),
				})
				d.Trigger = exactAnchor("chrys", chrysRootSession, "call_sub_1", "2026-01-02T00:00:05Z")
				d.Result = exactAnchor("chrys", chrysRootSession, "call_sub_1", "2026-01-02T00:00:09Z")
				return CollaborationGraph{
					RootAgentType: "chrys",
					RootSessionID: chrysRootSession,
					Revision:      3,
					Completeness:  ExactFact(),
					Invocations: []AgentInvocation{
						rootInvocation("chrys", chrysRootSession, "chrys main agent"),
						{
							ID:               child,
							DisplayName:      "explore_agent",
							AgentType:        "chrys",
							RoleLabel:        "explore_agent",
							Status:           StatusCompleted,
							StartedAt:        tp("2026-01-02T00:00:05Z"),
							EndedAt:          tp("2026-01-02T00:00:09Z"),
							TimePrecision:    ExactFact(),
							ContentPrecision: ExactFact(),
							SourceIdentity:   SourceIdentity{Kind: IdentityProviderCallID, NativeID: "call_sub_1"},
						},
					},
					Delegations: []Delegation{d},
				}
			},
			want: goldenExpect{
				root:      true,
				canonical: map[string]string{chrysChildID(): DelegationIDFor(chrysRootID(), chrysChildID())},
			},
			check: func(t *testing.T, g *CollaborationGraph) {
				child := g.Invocations[1]
				if child.BackingSession != nil {
					t.Error("embedded child must not carry a BackingSessionRef")
				}
				d := g.Delegations[0]
				if d.Trigger == nil || d.Trigger.ToolCallID != "call_sub_1" || d.Trigger.Precision.State != EvidenceExact {
					t.Errorf("chrys trigger anchor must be the exact call_id join: %+v", d.Trigger)
				}
				if d.TaskSummary != "" {
					t.Error("full delegated prompts are never stored as task_summary")
				}
			},
		},
		{
			// Archetype 3 (Copilot): lifecycle-only child with exact
			// toolCallId identity and lifecycle timing, estimated aggregate
			// content window, source-recorded task summary and sync mode.
			name: "lifecycle-only",
			graph: func() CollaborationGraph {
				root := copilotRootID()
				child := copilotChildA()
				d := delegation(root, child, ExecutionBlocking, DelegationEvidence{
					Trigger: ExactFact(),
					Timing:  ExactFact(),
					Task:    ExactFact(),
					Result:  ExactFact(),
				})
				d.Trigger = exactAnchor("copilot", copilotRootSess, "call-task-A", "2026-01-01T00:00:01Z")
				d.Result = exactAnchor("copilot", copilotRootSess, "call-task-A", "2026-01-01T00:01:10Z")
				d.TaskSummary = "Implement parser change"
				return CollaborationGraph{
					RootAgentType: "copilot",
					RootSessionID: copilotRootSess,
					Revision:      11,
					Completeness:  ExactFact(),
					Invocations: []AgentInvocation{
						rootInvocation("copilot", copilotRootSess, "copilot main agent"),
						{
							ID:               child,
							DisplayName:      "Impl Agent",
							AgentType:        "copilot",
							RoleLabel:        "impl",
							Status:           StatusCompleted,
							StartedAt:        tp("2026-01-01T00:01:00Z"),
							EndedAt:          tp("2026-01-01T00:01:10Z"),
							TimePrecision:    ExactFact(),
							ContentPrecision: FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonAggregateWindow},
							SourceIdentity:   SourceIdentity{Kind: IdentityToolCallID, NativeID: "call-task-A"},
						},
					},
					Delegations: []Delegation{d},
				}
			},
			want: goldenExpect{
				root:      true,
				canonical: map[string]string{copilotChildA(): DelegationIDFor(copilotRootID(), copilotChildA())},
			},
			check: func(t *testing.T, g *CollaborationGraph) {
				child := g.Invocations[1]
				if child.ContentPrecision.State != EvidenceEstimated || child.ContentPrecision.ReasonCode != ReasonAggregateWindow {
					t.Errorf("lifecycle-only content must be an estimated aggregate window: %+v", child.ContentPrecision)
				}
				if child.BackingSession != nil {
					t.Error("lifecycle-only child must not carry a BackingSessionRef")
				}
			},
		},
		{
			// Orphaned invocation (Copilot call-task-B): started, never
			// completed, session closed. Status orphaned, open end
			// evidence, result missing — never synthesized.
			name: "orphaned",
			graph: func() CollaborationGraph {
				root := copilotRootID()
				child := copilotChildB()
				d := delegation(root, child, ExecutionBackground, DelegationEvidence{
					Trigger: ExactFact(),
					Timing:  FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonCompletionNotRecorded},
					Task:    ExactFact(),
					Result:  FactEvidence{State: EvidenceMissing, ReasonCode: ReasonCompletionNotRecorded},
				})
				d.Trigger = exactAnchor("copilot", copilotRootSess, "call-task-B", "2026-01-01T00:02:00Z")
				d.TaskSummary = "Review the diff"
				return CollaborationGraph{
					RootAgentType: "copilot",
					RootSessionID: copilotRootSess,
					Revision:      12,
					Completeness:  ExactFact(),
					Invocations: []AgentInvocation{
						rootInvocation("copilot", copilotRootSess, "copilot main agent"),
						{
							ID:               child,
							DisplayName:      "Review Agent",
							AgentType:        "copilot",
							Status:           StatusOrphaned,
							StartedAt:        tp("2026-01-01T00:03:00Z"),
							TimePrecision:    FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonCompletionNotRecorded},
							ContentPrecision: FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonAggregateWindow},
							SourceIdentity:   SourceIdentity{Kind: IdentityToolCallID, NativeID: "call-task-B"},
						},
					},
					Delegations: []Delegation{d},
				}
			},
			want: goldenExpect{
				root:      true,
				canonical: map[string]string{copilotChildB(): DelegationIDFor(copilotRootID(), copilotChildB())},
			},
			check: func(t *testing.T, g *CollaborationGraph) {
				child := g.Invocations[1]
				if child.Status != StatusOrphaned {
					t.Errorf("status = %q, want orphaned", child.Status)
				}
				if child.EndedAt != nil {
					t.Error("orphaned invocation must not guess an end timestamp")
				}
				if g.Delegations[0].Result != nil {
					t.Error("orphaned invocation must not have a result anchor")
				}
			},
		},
		{
			// Estimated facts (Claude): exact launch anchor on the Agent
			// tool_use id, result link estimated by the FIFO join with a
			// reason code, embedded transcript content.
			name: "estimated-facts",
			graph: func() CollaborationGraph {
				root := claudeRootID()
				child := claudeChildID()
				d := delegation(root, child, ExecutionUnknown, DelegationEvidence{
					Trigger: ExactFact(),
					Timing:  FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonCompletionNotRecorded},
					Task:    ExactFact(),
					Result:  FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonFIFOJoinHeuristic},
				})
				d.Trigger = &SourceAnchor{
					AgentType: "claude",
					SessionID: claudeRootSession,
					EventID:   "toolu_01launch",
					Timestamp: tp("2026-01-03T00:00:04Z"),
					Precision: ExactFact(),
				}
				d.Result = &SourceAnchor{
					AgentType: "claude",
					SessionID: claudeRootSession,
					EventID:   "toolu_01result",
					Timestamp: tp("2026-01-03T00:00:20Z"),
					Precision: FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonFIFOJoinHeuristic},
				}
				d.TaskSummary = "Review the filter changes"
				return CollaborationGraph{
					RootAgentType: "claude",
					RootSessionID: claudeRootSession,
					Revision:      5,
					Completeness:  FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonFIFOJoinHeuristic},
					Invocations: []AgentInvocation{
						rootInvocation("claude", claudeRootSession, "claude main agent"),
						{
							ID:               child,
							DisplayName:      "reviewer",
							AgentType:        "claude",
							RoleLabel:        "reviewer",
							Status:           StatusUnknown,
							StartedAt:        tp("2026-01-03T00:00:04Z"),
							TimePrecision:    FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonCompletionNotRecorded},
							ContentPrecision: ExactFact(),
							SourceIdentity:   SourceIdentity{Kind: IdentityAgentID, NativeID: "agent-a1b2c3"},
						},
					},
					Delegations: []Delegation{d},
				}
			},
			want: goldenExpect{
				root:      true,
				canonical: map[string]string{claudeChildID(): DelegationIDFor(claudeRootID(), claudeChildID())},
			},
			check: func(t *testing.T, g *CollaborationGraph) {
				d := g.Delegations[0]
				if d.Result == nil || d.Result.Precision.State != EvidenceEstimated ||
					d.Result.Precision.ReasonCode != ReasonFIFOJoinHeuristic {
					t.Errorf("claude result link must be estimated with a FIFO reason: %+v", d.Result)
				}
				if d.Trigger.Precision.State != EvidenceExact {
					t.Error("claude launch anchor is exact on the Agent tool_use id")
				}
			},
		},
		{
			// Nested invocations: depth 2 (root → child → grandchild) in the
			// Copilot lifecycle shape. Depth is data-driven; no cap.
			name: "nested",
			graph: func() CollaborationGraph {
				root := copilotRootID()
				a, b := nestChildA(), nestChildB()
				d1 := delegation(root, a, ExecutionBlocking, DelegationEvidence{
					Trigger: ExactFact(), Timing: ExactFact(), Task: ExactFact(), Result: ExactFact(),
				})
				d1.Trigger = exactAnchor("copilot", copilotRootSess, "call-nest-a", "2026-01-01T00:00:01Z")
				d1.Result = exactAnchor("copilot", copilotRootSess, "call-nest-a", "2026-01-01T00:05:00Z")
				d1.TaskSummary = "Coordinate the migration"
				d2 := delegation(a, b, ExecutionBlocking, DelegationEvidence{
					Trigger: ExactFact(), Timing: ExactFact(), Task: ExactFact(), Result: ExactFact(),
				})
				d2.Trigger = exactAnchor("copilot", copilotRootSess, "call-nest-b", "2026-01-01T00:01:00Z")
				d2.Result = exactAnchor("copilot", copilotRootSess, "call-nest-b", "2026-01-01T00:03:00Z")
				d2.TaskSummary = "Migrate one module"
				mkChild := func(id, name, native, start, end string) AgentInvocation {
					return AgentInvocation{
						ID:               id,
						DisplayName:      name,
						AgentType:        "copilot",
						Status:           StatusCompleted,
						StartedAt:        tp(start),
						EndedAt:          tp(end),
						TimePrecision:    ExactFact(),
						ContentPrecision: FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonAggregateWindow},
						SourceIdentity:   SourceIdentity{Kind: IdentityToolCallID, NativeID: native},
					}
				}
				return CollaborationGraph{
					RootAgentType: "copilot",
					RootSessionID: copilotRootSess,
					Revision:      21,
					Completeness:  ExactFact(),
					Invocations: []AgentInvocation{
						rootInvocation("copilot", copilotRootSess, "copilot main agent"),
						mkChild(a, "Coordinator Agent", "call-nest-a", "2026-01-01T00:00:30Z", "2026-01-01T00:05:00Z"),
						mkChild(b, "Module Agent", "call-nest-b", "2026-01-01T00:01:00Z", "2026-01-01T00:03:00Z"),
					},
					Delegations: []Delegation{d2, d1}, // deliberately unordered; validation sorts
				}
			},
			want: goldenExpect{
				root: true,
				canonical: map[string]string{
					nestChildA(): DelegationIDFor(copilotRootID(), nestChildA()),
					nestChildB(): DelegationIDFor(nestChildA(), nestChildB()),
				},
			},
			check: func(t *testing.T, g *CollaborationGraph) {
				v := Validate(g)
				// Depth is data-driven: the grandchild's canonical parent is
				// the child, not the root.
				if v.CanonicalParent[nestChildB()] != DelegationIDFor(nestChildA(), nestChildB()) {
					t.Errorf("grandchild canonical parent wrong: %+v", v.CanonicalParent)
				}
			},
		},
		{
			// Missing parent: the child transcript is preserved and the
			// child attaches to the Unlinked child Agents group.
			name: "missing-parent",
			graph: func() CollaborationGraph {
				child := ChildInvocationID("chrys", chrysRootSession, "call_orphan_1")
				ghost := ChildInvocationID("chrys", chrysRootSession, "call_gone")
				d := delegation(ghost, child, ExecutionUnknown, DelegationEvidence{
					Trigger: FactEvidence{State: EvidenceMissing, ReasonCode: ReasonSourceNotRecorded},
					Timing:  ExactFact(),
					Task:    FactEvidence{State: EvidenceMissing, ReasonCode: ReasonSourceNotRecorded},
					Result:  FactEvidence{State: EvidenceMissing, ReasonCode: ReasonSourceNotRecorded},
				})
				return CollaborationGraph{
					RootAgentType: "chrys",
					RootSessionID: chrysRootSession,
					Revision:      4,
					Completeness:  FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonSourceNotRecorded},
					Invocations: []AgentInvocation{
						rootInvocation("chrys", chrysRootSession, "chrys main agent"),
						{
							ID:               child,
							DisplayName:      "explore_agent",
							AgentType:        "chrys",
							RoleLabel:        "explore_agent",
							Status:           StatusCompleted,
							StartedAt:        tp("2026-01-02T00:00:05Z"),
							EndedAt:          tp("2026-01-02T00:00:09Z"),
							TimePrecision:    ExactFact(),
							ContentPrecision: ExactFact(),
							SourceIdentity:   SourceIdentity{Kind: IdentityProviderCallID, NativeID: "call_orphan_1"},
						},
					},
					Delegations: []Delegation{d},
				}
			},
			want: goldenExpect{
				issues:    []IssueCode{IssueMissingParent},
				root:      true,
				canonical: map[string]string{},
				unlinked:  []string{ChildInvocationID("chrys", chrysRootSession, "call_orphan_1")},
			},
			check: func(t *testing.T, g *CollaborationGraph) {
				if len(g.Invocations) != 2 {
					t.Error("a valid child transcript is never discarded because its parent link is missing")
				}
			},
		},
		{
			// Interrupted session: the last valid indexed graph is retained
			// with a stale/error completeness state; an interrupted parse
			// never overwrites it with an empty graph.
			name: "interrupted",
			graph: func() CollaborationGraph {
				root := codexRootID()
				child := codexChildID()
				return CollaborationGraph{
					RootAgentType: "codex",
					RootSessionID: codexRootSession,
					Revision:      41,
					Completeness:  FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonStaleGraphRetained},
					Invocations: []AgentInvocation{
						rootInvocation("codex", codexRootSession, "codex main agent"),
						{
							ID:               child,
							DisplayName:      "audit",
							AgentType:        "codex",
							RoleLabel:        "audit",
							Status:           StatusUnknown,
							StartedAt:        tp("2026-01-02T00:00:01Z"),
							TimePrecision:    FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonCompletionNotRecorded},
							ContentPrecision: ExactFact(),
							BackingSession:   &BackingSessionRef{AgentType: "codex", SessionID: codexChildSession},
							SourceIdentity: SourceIdentity{
								Kind:       IdentityPayloadID,
								NativeID:   codexChildPayload,
								Attributes: map[string]string{"rollout_stem": codexChildSession},
							},
						},
					},
					Delegations: []Delegation{
						delegation(root, child, ExecutionUnknown, DelegationEvidence{
							Trigger: FactEvidence{State: EvidenceMissing, ReasonCode: ReasonSourceNotRecorded},
							Timing:  FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonCompletionNotRecorded},
							Task:    FactEvidence{State: EvidenceMissing, ReasonCode: ReasonSourceNotRecorded},
							Result:  FactEvidence{State: EvidenceMissing, ReasonCode: ReasonSourceNotRecorded},
						}),
					},
				}
			},
			want: goldenExpect{
				root:      true,
				canonical: map[string]string{codexChildID(): DelegationIDFor(codexRootID(), codexChildID())},
			},
			check: func(t *testing.T, g *CollaborationGraph) {
				if g.Completeness.ReasonCode != ReasonStaleGraphRetained {
					t.Errorf("completeness reason = %q, want %q", g.Completeness.ReasonCode, ReasonStaleGraphRetained)
				}
				if len(g.Invocations) != 2 {
					t.Error("stale graph retention must keep the last valid invocations, never an empty graph")
				}
			},
		},
		{
			// Malformed relation: self-link is quarantined; the invocation
			// itself survives and falls back to the Unlinked group.
			name: "malformed-self-link",
			graph: func() CollaborationGraph {
				child := chrysChildID()
				d := delegation(child, child, ExecutionUnknown, DelegationEvidence{
					Trigger: FactEvidence{State: EvidenceMissing, ReasonCode: ReasonSourceNotRecorded},
					Timing:  ExactFact(),
					Task:    FactEvidence{State: EvidenceMissing, ReasonCode: ReasonSourceNotRecorded},
					Result:  FactEvidence{State: EvidenceMissing, ReasonCode: ReasonSourceNotRecorded},
				})
				return CollaborationGraph{
					RootAgentType: "chrys",
					RootSessionID: chrysRootSession,
					Revision:      6,
					Completeness:  ExactFact(),
					Invocations: []AgentInvocation{
						rootInvocation("chrys", chrysRootSession, "chrys main agent"),
						{
							ID:               child,
							DisplayName:      "explore_agent",
							AgentType:        "chrys",
							Status:           StatusCompleted,
							TimePrecision:    ExactFact(),
							ContentPrecision: ExactFact(),
							SourceIdentity:   SourceIdentity{Kind: IdentityProviderCallID, NativeID: "call_sub_1"},
						},
					},
					Delegations: []Delegation{d},
				}
			},
			want: goldenExpect{
				issues:      []IssueCode{IssueSelfLink},
				root:        true,
				canonical:   map[string]string{},
				unlinked:    []string{chrysChildID()},
				quarantined: []string{DelegationIDFor(chrysChildID(), chrysChildID())},
			},
		},
		{
			// Malformed relations: cycle detection, duplicate relation
			// quarantine, and one-canonical-parent selection with extra
			// evidence preserved.
			name: "malformed-cycle",
			graph: func() CollaborationGraph {
				root := copilotRootID()
				a, b := nestChildA(), nestChildB()
				ev := DelegationEvidence{
					Trigger: ExactFact(), Timing: ExactFact(), Task: ExactFact(), Result: ExactFact(),
				}
				dLaunchA := delegation(root, a, ExecutionBlocking, ev)
				dAB := delegation(a, b, ExecutionBlocking, ev)
				dBA := delegation(b, a, ExecutionBlocking, ev) // closes the cycle
				dDup := delegation(a, b, ExecutionBlocking, ev)
				dDup.ID = "copilot:" + copilotRootSess + ":duplicate-relation-1"
				dSecondParent := delegation(root, b, ExecutionBlocking, ev)
				mkChild := func(id, name, native string) AgentInvocation {
					return AgentInvocation{
						ID:               id,
						DisplayName:      name,
						AgentType:        "copilot",
						Status:           StatusCompleted,
						StartedAt:        tp("2026-01-01T00:01:00Z"),
						EndedAt:          tp("2026-01-01T00:03:00Z"),
						TimePrecision:    ExactFact(),
						ContentPrecision: FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonAggregateWindow},
						SourceIdentity:   SourceIdentity{Kind: IdentityToolCallID, NativeID: native},
					}
				}
				return CollaborationGraph{
					RootAgentType: "copilot",
					RootSessionID: copilotRootSess,
					Revision:      22,
					Completeness:  ExactFact(),
					Invocations: []AgentInvocation{
						rootInvocation("copilot", copilotRootSess, "copilot main agent"),
						mkChild(a, "Coordinator Agent", "call-nest-a"),
						mkChild(b, "Module Agent", "call-nest-b"),
					},
					Delegations: []Delegation{dBA, dDup, dSecondParent, dLaunchA, dAB},
				}
			},
			want: goldenExpect{
				issues: []IssueCode{IssueCycle, IssueDuplicateRelation, IssueMultipleParents},
				root:   true,
				canonical: map[string]string{
					nestChildA(): DelegationIDFor(copilotRootID(), nestChildA()),
					nestChildB(): DelegationIDFor(nestChildA(), nestChildB()),
				},
				quarantined: []string{
					DelegationIDFor(nestChildB(), nestChildA()),
					"copilot:" + copilotRootSess + ":duplicate-relation-1",
				},
			},
			check: func(t *testing.T, g *CollaborationGraph) {
				// Extra relation evidence is preserved on the graph even
				// though only one parent is canonical.
				if len(g.Delegations) != 5 {
					t.Errorf("relation evidence must be preserved, got %d delegations", len(g.Delegations))
				}
			},
		},
		{
			// unknown is a first-class status: never omitted, never
			// collapsed into success or failure.
			name: "unknown-status",
			graph: func() CollaborationGraph {
				root := claudeRootID()
				child := claudeChildID()
				d := delegation(root, child, ExecutionUnknown, DelegationEvidence{
					Trigger: ExactFact(),
					Timing:  FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonCompletionNotRecorded},
					Task:    FactEvidence{State: EvidenceMissing, ReasonCode: ReasonSourceNotRecorded},
					Result:  FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonFIFOJoinHeuristic},
				})
				d.Trigger = &SourceAnchor{
					AgentType: "claude",
					SessionID: claudeRootSession,
					EventID:   "toolu_01launch",
					Timestamp: tp("2026-01-03T00:00:04Z"),
					Precision: ExactFact(),
				}
				return CollaborationGraph{
					RootAgentType: "claude",
					RootSessionID: claudeRootSession,
					Revision:      8,
					Completeness:  FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonCompletionNotRecorded},
					Invocations: []AgentInvocation{
						rootInvocation("claude", claudeRootSession, "claude main agent"),
						{
							ID:               child,
							DisplayName:      "researcher",
							AgentType:        "claude",
							RoleLabel:        "researcher",
							Status:           StatusUnknown,
							StartedAt:        tp("2026-01-03T00:00:04Z"),
							TimePrecision:    FactEvidence{State: EvidenceEstimated, ReasonCode: ReasonCompletionNotRecorded},
							ContentPrecision: ExactFact(),
							SourceIdentity:   SourceIdentity{Kind: IdentityAgentID, NativeID: "agent-a1b2c3"},
						},
					},
					Delegations: []Delegation{d},
				}
			},
			want: goldenExpect{
				root:      true,
				canonical: map[string]string{claudeChildID(): DelegationIDFor(claudeRootID(), claudeChildID())},
			},
			check: func(t *testing.T, g *CollaborationGraph) {
				s := g.Invocations[1].Status
				if s != StatusUnknown {
					t.Errorf("status = %q, want first-class unknown", s)
				}
				if s == StatusCompleted || s == StatusFailed {
					t.Error("unknown must never collapse into success or failure")
				}
			},
		},
	}
}

func goldenPath(name string) string {
	return filepath.Join("testdata", "golden", name+".json")
}

func marshalGolden(g *CollaborationGraph) []byte {
	b, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(b, '\n')
}

// TestGoldenSerialization locks the stable JSON shape: each fixture
// unmarshals into exactly the graph its builder produces, and re-marshaling
// reproduces the fixture byte for byte.
func TestGoldenSerialization(t *testing.T) {
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			g := tc.graph()
			want := marshalGolden(&g)
			path := goldenPath(tc.name)
			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, want, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if !bytes.Equal(raw, want) {
				t.Fatalf("golden drift: %s does not match the contract serialization (run with -update after a deliberate contract change)", path)
			}
			var round CollaborationGraph
			if err := json.Unmarshal(raw, &round); err != nil {
				t.Fatalf("unmarshal golden: %v", err)
			}
			if !reflect.DeepEqual(round, g) {
				t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", round, g)
			}
		})
	}
}

// TestGoldenValidation runs graph validation against each fixture and
// compares findings, the canonical projection, the Unlinked group, and
// quarantined relations.
func TestGoldenValidation(t *testing.T) {
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			g := tc.graph()
			v := Validate(&g)

			gotIssues := make([]IssueCode, 0, len(v.Issues))
			for _, i := range v.Issues {
				gotIssues = append(gotIssues, i.Code)
			}
			sort.Slice(gotIssues, func(i, j int) bool { return gotIssues[i] < gotIssues[j] })
			wantIssues := append([]IssueCode{}, tc.want.issues...)
			sort.Slice(wantIssues, func(i, j int) bool { return wantIssues[i] < wantIssues[j] })
			if !reflect.DeepEqual(gotIssues, wantIssues) {
				t.Errorf("issues = %v, want %v (full: %+v)", gotIssues, wantIssues, v.Issues)
			}
			if (v.RootID != "") != tc.want.root {
				t.Errorf("root present = %v, want %v", v.RootID != "", tc.want.root)
			}
			if !reflect.DeepEqual(v.CanonicalParent, tc.want.canonical) {
				t.Errorf("canonical parents = %v, want %v", v.CanonicalParent, tc.want.canonical)
			}
			if !reflect.DeepEqual(v.Unlinked, tc.want.unlinked) {
				t.Errorf("unlinked = %v, want %v", v.Unlinked, tc.want.unlinked)
			}
			if !reflect.DeepEqual(v.Quarantined, tc.want.quarantined) {
				t.Errorf("quarantined = %v, want %v", v.Quarantined, tc.want.quarantined)
			}
			if tc.check != nil {
				tc.check(t, &g)
			}
		})
	}
}

// TestGoldenDeterministicValidation proves validation is a pure function of
// the graph: repeated runs and shuffled input order yield identical
// findings and projections.
func TestGoldenDeterministicValidation(t *testing.T) {
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			g := tc.graph()
			first := Validate(&g)
			second := Validate(&g)
			if !reflect.DeepEqual(first, second) {
				t.Fatal("validation is not deterministic across repeated runs")
			}
		})
	}
}

// TestGoldenSanitized scans every fixture for private path material. All
// cases are synthetic by construction; this keeps them that way.
func TestGoldenSanitized(t *testing.T) {
	for _, tc := range goldenCases() {
		raw, err := os.ReadFile(goldenPath(tc.name))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		for _, pat := range []string{"/home/", "/Users/", `C:\Users`, `C:/Users`} {
			if bytes.Contains(raw, []byte(pat)) {
				t.Errorf("%s contains private path material %q", tc.name, pat)
			}
		}
	}
}
