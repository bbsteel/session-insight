package adaptertest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/model"
)

// fakeCollabReader implements Reader plus CollaborationReader for skeleton
// tests.
type fakeCollabReader struct {
	graph      collaboration.CollaborationGraph
	alternate  *collaboration.CollaborationGraph // returned on the second read when set
	reads      int
	noCollabIF bool // unused marker for documentation
}

func (f *fakeCollabReader) AgentType() string   { return "fake" }
func (f *fakeCollabReader) DisplayName() string { return "Fake" }
func (f *fakeCollabReader) ListSessions() ([]model.Session, error) {
	return nil, nil
}
func (f *fakeCollabReader) GetSession(id string) (*model.SessionDetail, error) {
	return nil, nil
}
func (f *fakeCollabReader) RenderANSI(id string, cols int) (string, error) { return "", nil }
func (f *fakeCollabReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	return nil, nil
}
func (f *fakeCollabReader) ReadCollaboration(ctx context.Context, root model.Session) (collaboration.CollaborationGraph, error) {
	f.reads++
	if f.reads == 2 && f.alternate != nil {
		return *f.alternate, nil
	}
	return f.graph, nil
}

func fakeGraph() collaboration.CollaborationGraph {
	started := time.Date(2026, 1, 2, 0, 0, 1, 0, time.UTC)
	root := collaboration.RootInvocationID("fake", "root-1")
	child := collaboration.ChildInvocationID("fake", "root-1", "native-1")
	return collaboration.CollaborationGraph{
		RootAgentType: "fake",
		RootSessionID: "root-1",
		Revision:      1,
		Completeness:  collaboration.ExactFact(),
		Invocations: []collaboration.AgentInvocation{
			{
				ID:               root,
				DisplayName:      "fake main agent",
				AgentType:        "fake",
				Status:           collaboration.StatusCompleted,
				TimePrecision:    collaboration.ExactFact(),
				ContentPrecision: collaboration.ExactFact(),
				SourceIdentity:   collaboration.SourceIdentity{Kind: collaboration.IdentityRootSession, NativeID: "root-1"},
			},
			{
				ID:               child,
				DisplayName:      "fake child",
				AgentType:        "fake",
				Status:           collaboration.StatusUnknown,
				StartedAt:        &started,
				TimePrecision:    collaboration.FactEvidence{State: collaboration.EvidenceEstimated, ReasonCode: collaboration.ReasonCompletionNotRecorded},
				ContentPrecision: collaboration.ExactFact(),
				SourceIdentity:   collaboration.SourceIdentity{Kind: collaboration.IdentityPayloadID, NativeID: "native-1"},
			},
		},
		Delegations: []collaboration.Delegation{
			{
				ID:                 collaboration.DelegationIDFor(root, child),
				ParentInvocationID: root,
				ChildInvocationID:  child,
				ExecutionMode:      collaboration.ExecutionUnknown,
				Evidence: collaboration.DelegationEvidence{
					Trigger: collaboration.FactEvidence{State: collaboration.EvidenceMissing, ReasonCode: collaboration.ReasonSourceNotRecorded},
					Timing:  collaboration.FactEvidence{State: collaboration.EvidenceEstimated, ReasonCode: collaboration.ReasonCompletionNotRecorded},
					Task:    collaboration.FactEvidence{State: collaboration.EvidenceMissing, ReasonCode: collaboration.ReasonSourceNotRecorded},
					Result:  collaboration.FactEvidence{State: collaboration.EvidenceMissing, ReasonCode: collaboration.ReasonSourceNotRecorded},
				},
			},
		},
	}
}

func TestCheckCollaborationGraphValid(t *testing.T) {
	if problems := CheckCollaborationGraph(fakeGraph()); len(problems) != 0 {
		t.Fatalf("valid graph reported problems: %v", problems)
	}
}

func TestCheckCollaborationGraphNamesMissingRequirement(t *testing.T) {
	g := fakeGraph()
	g.Invocations = g.Invocations[:1] // only the root; delegation dangles
	problems := CheckCollaborationGraph(g)
	if len(problems) == 0 {
		t.Fatal("dangling delegation must be reported")
	}
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, string(collaboration.IssueUnknownChild)) {
		t.Errorf("message must identify the violated requirement code: %v", problems)
	}

	noRoot := fakeGraph()
	noRoot.Invocations = noRoot.Invocations[1:]
	problems = CheckCollaborationGraph(noRoot)
	joined = strings.Join(problems, "\n")
	if !strings.Contains(joined, "exactly one deterministic root invocation") {
		t.Errorf("message must name the root requirement: %v", problems)
	}
}

func TestCheckCollaborationExpect(t *testing.T) {
	g := fakeGraph()

	if problems := CheckCollaborationExpect(g, CollaborationExpect{MinChildren: 1}); len(problems) != 0 {
		t.Fatalf("MinChildren satisfied, got: %v", problems)
	}
	if problems := CheckCollaborationExpect(g, CollaborationExpect{MinChildren: 2}); len(problems) != 1 {
		t.Fatalf("MinChildren gap must produce exactly one message: %v", problems)
	}
	if problems := CheckCollaborationExpect(g, CollaborationExpect{RequireBackingSession: true}); len(problems) != 1 {
		t.Fatalf("missing backing session must be flagged: %v", problems)
	}
	g.Invocations[1].BackingSession = &collaboration.BackingSessionRef{AgentType: "fake", SessionID: "child-1"}
	if problems := CheckCollaborationExpect(g, CollaborationExpect{RequireBackingSession: true, MinChildren: 1}); len(problems) != 0 {
		t.Fatalf("backing session present, got: %v", problems)
	}
	if problems := CheckCollaborationExpect(g, CollaborationExpect{ForbidBackingSession: true}); len(problems) != 1 {
		t.Fatalf("forbidden backing session must be flagged: %v", problems)
	}
}

func TestTwoParseStabilityDetectsIdentityDrift(t *testing.T) {
	g := fakeGraph()
	drifted := fakeGraph()
	drifted.Invocations[1].ID = collaboration.ChildInvocationID("fake", "root-1", "native-1-run2")
	r := &fakeCollabReader{graph: g, alternate: &drifted}

	first, _ := r.ReadCollaboration(context.Background(), model.Session{ID: "root-1"})
	second, _ := r.ReadCollaboration(context.Background(), model.Session{ID: "root-1"})
	if identitySignature(first) == identitySignature(second) {
		t.Fatal("test setup broken: drifted identities must differ")
	}
}

func TestIdentitySignatureOrderIndependent(t *testing.T) {
	a := fakeGraph()
	b := fakeGraph()
	b.Invocations[0], b.Invocations[1] = b.Invocations[1], b.Invocations[0]
	if identitySignature(a) != identitySignature(b) {
		t.Fatal("identity signature must be independent of list order")
	}
}
