package dsh

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
)

func collaborationFixture(t *testing.T, legacy, compressed bool) *Reader {
	t.Helper()
	root := t.TempDir()
	generation := 4
	if legacy {
		generation = 3
	}
	write := func(id, parent string, records string) {
		content := fmt.Sprintf("{\"type\":\"session\",\"version\":%d,\"id\":%q,\"parentSession\":%q,\"createdAt\":1760000000000}\n%s", generation, id, parent, records)
		filename := fmt.Sprintf("session.v%d.jsonl", generation)
		if compressed {
			filename += ".zstd"
			content = string(compressFrame(t, []byte(content)))
		}
		adaptertest.MustWrite(t, filepath.Join(root, "workspace", id, filename), content)
	}
	write(exampleID, "", `{"type":"turn/start","seq":1,"time":1760000000010,"data":{"turn":1}}
{"type":"subagent/catalog","seq":2,"time":1760000000020,"data":{"childId":"child-a","label":"Inspect configuration","mode":"continuable"}}
{"type":"subagent/catalog","seq":3,"time":1760000000040,"data":{"childId":"child-b","label":"Inspect logs","mode":"continuable"}}
{"type":"user/message","seq":4,"time":1760000000090,"data":{"source":{"kind":"subagent-settled","senderSessionId":"child-b"},"content":[{"type":"text","text":"The child finished."}]}}
{"type":"turn/end","seq":5,"time":1760000000210,"data":{"turn":1,"reason":{"kind":"completed"}}}
`)
	write("child-a", exampleID, `{"type":"turn/start","seq":1,"time":1760000000021,"data":{"turn":1}}
{"type":"turn/end","seq":2,"time":1760000000070,"data":{"turn":1,"reason":{"kind":"completed"}}}
{"type":"turn/start","seq":3,"time":1760000000180,"data":{"turn":2}}
{"type":"turn/end","seq":4,"time":1760000000200,"data":{"turn":2,"reason":{"kind":"completed"}}}
`)
	write("child-b", exampleID, `{"type":"turn/start","seq":1,"time":1760000000041,"data":{"turn":1}}
{"type":"turn/end","seq":2,"time":1760000000080,"data":{"turn":1,"reason":{"kind":"completed"}}}
{"type":"turn/start","seq":3,"time":1760000000190,"data":{"turn":2}}
`)
	return New(root)
}

func assertCollaborationFixture(t *testing.T, reader adaptertest.Reader) {
	t.Helper()
	adaptertest.AssertSubtasks(t, reader, adaptertest.SubtaskExpect{SessionID: exampleID, RequireChildIDs: true})
	root := model.Session{ID: exampleID, AgentType: agentType}
	source := adaptertest.RequireCollaborationReader(t, reader)
	adaptertest.AssertCollaborationTwoParseStability(t, source, root)
	graph, err := source.ReadCollaboration(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	adaptertest.AssertCollaborationGraph(t, graph)
	if len(graph.Invocations) != 3 || len(graph.Delegations) != 2 {
		t.Fatalf("expected root and two child lanes: %+v", graph)
	}
	childA, childB := graph.Invocations[1], graph.Invocations[2]
	if childA.BackingSession == nil || childA.BackingSession.SessionID != "child-a" ||
		len(childA.Activity) != 2 || childA.Activity[1].StartedAt.Sub(childA.Activity[0].EndedAt) != 110*time.Millisecond {
		t.Fatalf("continuable child lost the gap between its turns: %+v", childA)
	}
	if childB.EndedAt != nil || len(childB.Activity) != 1 || childB.Status != collaboration.StatusUnknown {
		t.Fatalf("unfinished turn acquired a completion: %+v", childB)
	}
	delegation := graph.Delegations[1]
	if delegation.ExecutionMode != collaboration.ExecutionBackground || delegation.Result == nil ||
		delegation.Result.EventID != exampleID+":4" {
		t.Fatalf("missing exact result anchor: %+v", delegation)
	}
}
