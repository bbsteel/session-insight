package codex

import (
	"path/filepath"
	"testing"

	"github.com/bbsteel/session-insight/internal/changeevidence"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
)

func TestCodexCreationTranscriptPairsInvocationAndResultByIdentity(t *testing.T) {
	r := New(filepath.Join("testdata", "pr-creation"))
	envelope := adaptertest.AssertIndexSnapshotEnvelope(t, r, adaptertest.IndexSnapshotEnvelopeExpect{
		SessionID: "created",
	})

	var invocation, result *model.RenderEvent
	for i := range envelope.RenderEvents {
		event := &envelope.RenderEvents[i]
		if event.Type == "ToolInvocation" && event.ToolCallID == "call-create" {
			invocation = event
		}
		if event.Type == "ToolResult" && event.ToolCallID == "call-create" {
			result = event
		}
	}
	if invocation == nil || result == nil {
		t.Fatalf("missing paired creation events: events=%+v", envelope.RenderEvents)
	}
	if invocation.EventID == "" || result.ParentEventID != invocation.EventID {
		t.Fatalf("creation events were not joined by identity: invoke=%+v result=%+v", invocation, result)
	}
	command, _ := invocation.ToolInput["command"].(string)
	if command != "gh pr create --base main --head feat/sanitized-branch --title sanitized --body sanitized" {
		t.Fatalf("creation command = %q", command)
	}
	if result.ExitCode != 0 || result.Stdout == "" {
		t.Fatalf("creation result is not a successful stdout payload: %+v", result)
	}

	evidence := changeevidence.ExtractCreationEvidence(envelope.RenderEvents, envelope.SourceRevision)
	var cliCreationEvidence *model.ChangeRequestCreationEvidence
	for evidenceIndex := range evidence {
		candidateEvidence := &evidence[evidenceIndex]
		if candidateEvidence.CommandKind != changeevidence.CommandGitHubCLI {
			continue
		}
		if cliCreationEvidence != nil {
			t.Fatalf("multiple GitHub CLI creation evidence records: %+v", evidence)
		}
		cliCreationEvidence = candidateEvidence
	}
	if cliCreationEvidence == nil {
		t.Fatalf("missing GitHub CLI creation evidence: %+v", evidence)
	}
	got := *cliCreationEvidence
	if got.EventID != invocation.EventID || got.Reference.NormalizedURL != "https://github.com/acme/widgets/pull/42" ||
		got.CommandKind != changeevidence.CommandGitHubCLI || got.Assessment.State != model.GitEvidenceExact ||
		got.SourceRevision != envelope.SourceRevision {
		t.Fatalf("unexpected extracted evidence: %+v", got)
	}
}
