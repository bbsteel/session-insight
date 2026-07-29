package server

// Disposable Wave 2 gate verification: real adapter fixtures through the
// full reader -> Validate -> DB -> API -> render-route chain. Proposed as
// durable cross-module coverage by the Wave 2 gate (see the gate report).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/reader/chrys"
	"github.com/bbsteel/session-insight/internal/reader/codex"
	"github.com/bbsteel/session-insight/internal/reader/copilot"
)

type gateArchetype struct {
	name       string
	fixtureDir string
	rootID     string
	newReader  func(dir string) reader.BaseSessionReader
}

func gateArchetypes() []gateArchetype {
	return []gateArchetype{
		{
			name:       "codex",
			fixtureDir: "../reader/codex/testdata/collaboration-standalone-child/sessions",
			rootID:     "rollout-2026-01-02T00-00-00-019f0000-0000-7000-8000-0000000000aa",
			newReader:  func(dir string) reader.BaseSessionReader { return codex.New(dir) },
		},
		{
			name:       "chrys",
			fixtureDir: "../reader/chrys/testdata/collaboration-embedded-child",
			rootID:     "28491d6d491e",
			newReader:  func(dir string) reader.BaseSessionReader { return chrys.New(dir) },
		},
		{
			name:       "copilot",
			fixtureDir: "../reader/copilot/testdata/collaboration-lifecycle-only",
			rootID:     "collab-copilot-1",
			newReader:  func(dir string) reader.BaseSessionReader { return copilot.New(dir) },
		},
	}
}

func gateRootSession(t *testing.T, r reader.BaseSessionReader, rootID string) model.Session {
	t.Helper()
	list, err := r.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	for _, s := range list {
		if s.ID == rootID {
			return s
		}
	}
	t.Fatalf("root %s not listed", rootID)
	return model.Session{}
}

func gateFindInvocation(t *testing.T, g collaboration.CollaborationGraph, id string) collaboration.AgentInvocation {
	t.Helper()
	for _, inv := range g.Invocations {
		if inv.ID == id {
			return inv
		}
	}
	t.Fatalf("invocation %q missing", id)
	return collaboration.AgentInvocation{}
}

func gateFindDelegation(t *testing.T, g collaboration.CollaborationGraph, id string) collaboration.Delegation {
	t.Helper()
	for _, d := range g.Delegations {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("delegation %q missing", id)
	return collaboration.Delegation{}
}

func gateCompareAnchor(t *testing.T, label string, want, got *collaboration.SourceAnchor) {
	t.Helper()
	if (want == nil) != (got == nil) {
		t.Fatalf("%s anchor presence mismatch: want %v got %v", label, want != nil, got != nil)
	}
	if want == nil {
		return
	}
	if want.AgentType != got.AgentType || want.SessionID != got.SessionID ||
		want.EventID != got.EventID || want.ToolCallID != got.ToolCallID ||
		want.Precision != got.Precision {
		t.Errorf("%s anchor mismatch: want %+v got %+v", label, *want, *got)
	}
	if (want.TurnIndex == nil) != (got.TurnIndex == nil) ||
		(want.TurnIndex != nil && *want.TurnIndex != *got.TurnIndex) {
		t.Errorf("%s anchor turn index mismatch: want %v got %v", label, want.TurnIndex, got.TurnIndex)
	}
	if (want.Timestamp == nil) != (got.Timestamp == nil) ||
		(want.Timestamp != nil && !want.Timestamp.Equal(*got.Timestamp)) {
		t.Errorf("%s anchor timestamp mismatch: want %v got %v", label, want.Timestamp, got.Timestamp)
	}
}

func gateCompareGraphs(t *testing.T, want, got collaboration.CollaborationGraph) {
	t.Helper()
	if want.RootAgentType != got.RootAgentType || want.RootSessionID != got.RootSessionID {
		t.Fatalf("root coordinates: want %s/%s got %s/%s",
			want.RootAgentType, want.RootSessionID, got.RootAgentType, got.RootSessionID)
	}
	if want.Revision != got.Revision {
		t.Errorf("revision: want %d got %d", want.Revision, got.Revision)
	}
	if want.Completeness != got.Completeness {
		t.Errorf("completeness: want %+v got %+v", want.Completeness, got.Completeness)
	}
	if len(want.Invocations) != len(got.Invocations) {
		t.Fatalf("invocation count: want %d got %d", len(want.Invocations), len(got.Invocations))
	}
	if len(want.Delegations) != len(got.Delegations) {
		t.Fatalf("delegation count: want %d got %d", len(want.Delegations), len(got.Delegations))
	}
	for _, w := range want.Invocations {
		g := gateFindInvocation(t, got, w.ID)
		if w.ID != g.ID { // byte-for-byte identity, redundant but explicit
			t.Errorf("invocation ID bytes: %q vs %q", w.ID, g.ID)
		}
		if w.Status != g.Status {
			t.Errorf("invocation %s status: want %q got %q", w.ID, w.Status, g.Status)
		}
		if w.TimePrecision != g.TimePrecision || w.ContentPrecision != g.ContentPrecision {
			t.Errorf("invocation %s precision: want %v/%v got %v/%v",
				w.ID, w.TimePrecision, w.ContentPrecision, g.TimePrecision, g.ContentPrecision)
		}
		if w.DisplayName != g.DisplayName || w.RoleLabel != g.RoleLabel {
			t.Errorf("invocation %s labels: want %q/%q got %q/%q", w.ID, w.DisplayName, w.RoleLabel, g.DisplayName, g.RoleLabel)
		}
		if (w.StartedAt == nil) != (g.StartedAt == nil) || (w.StartedAt != nil && !w.StartedAt.Equal(*g.StartedAt)) {
			t.Errorf("invocation %s started_at: want %v got %v", w.ID, w.StartedAt, g.StartedAt)
		}
		if (w.EndedAt == nil) != (g.EndedAt == nil) || (w.EndedAt != nil && !w.EndedAt.Equal(*g.EndedAt)) {
			t.Errorf("invocation %s ended_at: want %v got %v", w.ID, w.EndedAt, g.EndedAt)
		}
		if !reflect.DeepEqual(w.BackingSession, g.BackingSession) {
			t.Errorf("invocation %s backing: want %+v got %+v", w.ID, w.BackingSession, g.BackingSession)
		}
		if w.SourceIdentity.Kind != g.SourceIdentity.Kind || w.SourceIdentity.NativeID != g.SourceIdentity.NativeID {
			t.Errorf("invocation %s source identity: want %+v got %+v", w.ID, w.SourceIdentity, g.SourceIdentity)
		}
	}
	for _, w := range want.Delegations {
		g := gateFindDelegation(t, got, w.ID)
		if w.ParentInvocationID != g.ParentInvocationID || w.ChildInvocationID != g.ChildInvocationID {
			t.Errorf("delegation %s endpoints: want %s->%s got %s->%s",
				w.ID, w.ParentInvocationID, w.ChildInvocationID, g.ParentInvocationID, g.ChildInvocationID)
		}
		if w.ExecutionMode != g.ExecutionMode {
			t.Errorf("delegation %s mode: want %q got %q", w.ID, w.ExecutionMode, g.ExecutionMode)
		}
		if w.TaskSummary != g.TaskSummary {
			t.Errorf("delegation %s task summary: want %q got %q", w.ID, w.TaskSummary, g.TaskSummary)
		}
		gateCompareAnchor(t, w.ID+" trigger", w.Trigger, g.Trigger)
		gateCompareAnchor(t, w.ID+" result", w.Result, g.Result)
		if w.Evidence != g.Evidence {
			t.Errorf("delegation %s evidence: want %+v got %+v", w.ID, w.Evidence, g.Evidence)
		}
	}
}

// TestWave2GateArchetypeRoundTrip drives each real archetype reader through
// two-parse stability, contract validation, DB persistence, the collaboration
// detail API, and the invocation render routes, asserting precision, anchors,
// and identity survive every hop byte-for-byte.
func TestWave2GateArchetypeRoundTrip(t *testing.T) {
	for _, arc := range gateArchetypes() {
		t.Run(arc.name, func(t *testing.T) {
			r := arc.newReader(arc.fixtureDir)
			root := gateRootSession(t, r, arc.rootID)
			cr, ok := r.(reader.CollaborationReader)
			if !ok {
				t.Fatalf("%s reader does not implement CollaborationReader", arc.name)
			}

			graph1, err := cr.ReadCollaboration(context.Background(), root)
			if err != nil {
				t.Fatalf("ReadCollaboration: %v", err)
			}
			graph2, err := cr.ReadCollaboration(context.Background(), root)
			if err != nil {
				t.Fatalf("ReadCollaboration (second parse): %v", err)
			}
			if !reflect.DeepEqual(graph1, graph2) {
				t.Fatal("two-parse full-graph equality failed")
			}
			fatal := map[collaboration.IssueCode]bool{
				collaboration.IssueMissingField:         true,
				collaboration.IssueInvalidStatus:        true,
				collaboration.IssueInvalidExecutionMode: true,
				collaboration.IssueInvalidEvidence:      true,
				collaboration.IssueNoRoot:               true,
				collaboration.IssueDuplicateInvocation:  true,
			}
			v := collaboration.Validate(&graph1)
			for _, issue := range v.Issues {
				if fatal[issue.Code] {
					t.Fatalf("Validate fatal finding: %+v", issue)
				}
			}

			database := openCollabAPIDB(t)
			seedCollabSession(t, database, arc.name, arc.rootID, false)
			if err := database.ReplaceCollaborationGraph(graph1); err != nil {
				t.Fatalf("ReplaceCollaborationGraph: %v", err)
			}

			stored, err := database.GetCollaboration(arc.name, arc.rootID)
			if err != nil || stored == nil {
				t.Fatalf("GetCollaboration: %v (stored=%v)", err, stored != nil)
			}
			gateCompareGraphs(t, graph1, stored.Graph)

			srv := New(database, []reader.BaseSessionReader{r})
			w, body := getJSON(t, srv, "/api/sessions/"+arc.rootID+"/collaboration?agent="+arc.name, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("detail: %d %s", w.Code, w.Body.String())
			}
			raw := w.Body.Bytes()
			if strings.Contains(string(raw), "user_message") || strings.Contains(string(raw), "assistant_message") {
				t.Fatal("detail payload contains transcript bodies")
			}
			if body["root_agent_type"] != arc.name || body["root_session_id"] != arc.rootID {
				t.Fatalf("API root coordinates: %v", body)
			}
			apiInvs, _ := body["invocations"].([]any)
			if len(apiInvs) != len(graph1.Invocations) {
				t.Fatalf("API invocation count: want %d got %d", len(graph1.Invocations), len(apiInvs))
			}
			for _, wInv := range graph1.Invocations {
				found := false
				for _, ai := range apiInvs {
					m, _ := ai.(map[string]any)
					if m["id"] == wInv.ID {
						found = true
						if m["status"] != string(wInv.Status) {
							t.Errorf("API invocation %s status: want %q got %v", wInv.ID, wInv.Status, m["status"])
						}
						reasonOf := func(m map[string]any) collaboration.ReasonCode {
							if s, ok := m["reason_code"].(string); ok {
								return collaboration.ReasonCode(s)
							}
							return "" // omitempty drops empty reason codes
						}
						tp, _ := m["time_precision"].(map[string]any)
						if tp["state"] != string(wInv.TimePrecision.State) || reasonOf(tp) != wInv.TimePrecision.ReasonCode {
							t.Errorf("API invocation %s time precision: want %+v got %v", wInv.ID, wInv.TimePrecision, tp)
						}
						cp, _ := m["content_precision"].(map[string]any)
						if cp["state"] != string(wInv.ContentPrecision.State) || reasonOf(cp) != wInv.ContentPrecision.ReasonCode {
							t.Errorf("API invocation %s content precision: want %+v got %v", wInv.ID, wInv.ContentPrecision, cp)
						}
						backing, hasBacking := m["backing_session"].(map[string]any)
						if arc.name == "codex" && wInv.BackingSession != nil {
							if !hasBacking || backing["session_id"] != wInv.BackingSession.SessionID {
								t.Errorf("API codex backing ref lost: %v", m["backing_session"])
							}
						} else if hasBacking {
							t.Errorf("API invocation %s unexpectedly carries backing_session for %s", wInv.ID, arc.name)
						}
					}
				}
				if !found {
					t.Errorf("API payload missing invocation %q", wInv.ID)
				}
			}

			// Optionally persist the exact API payload so the frontend
			// normalize/layout stage can consume real handler output:
			//   SI_ROUNDTRIP_OUT=/tmp/dir go test -run TestWave2GateArchetypeRoundTrip ./internal/server/
			if out := os.Getenv("SI_ROUNDTRIP_OUT"); out != "" {
				if err := os.MkdirAll(out, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(out+"/gate-roundtrip-"+arc.name+".json", raw, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			// Render routing per archetype.
			var childID string
			for _, inv := range graph1.Invocations {
				if inv.ID != collaboration.RootInvocationID(arc.name, arc.rootID) {
					childID = inv.ID
					break
				}
			}
			renderURL := "/api/sessions/" + arc.rootID + "/render?agent=" + arc.name +
				"&invocation=" + url.QueryEscape(childID)
			req := httptest.NewRequest("GET", renderURL, nil)
			rw := httptest.NewRecorder()
			srv.Mux.ServeHTTP(rw, req)
			switch arc.name {
			case "codex":
				if rw.Code != http.StatusOK || rw.Body.Len() == 0 {
					t.Fatalf("codex backed child render: %d (%d bytes)", rw.Code, rw.Body.Len())
				}
			case "chrys":
				if rw.Code != http.StatusOK || rw.Body.Len() == 0 {
					t.Fatalf("chrys embedded child render: %d (%d bytes)", rw.Code, rw.Body.Len())
				}
			case "copilot":
				if rw.Code != http.StatusUnprocessableEntity {
					t.Fatalf("copilot lifecycle-only child render: %d, want 422 (%s)", rw.Code, rw.Body.String())
				}
				var errBody map[string]any
				if err := json.Unmarshal(rw.Body.Bytes(), &errBody); err != nil {
					t.Fatal(err)
				}
				if errBody["code"] != "invocation_content_unavailable" {
					t.Fatalf("copilot render error code: %v", errBody)
				}
			}
		})
	}
}
