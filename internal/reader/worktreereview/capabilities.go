package worktreereview

import "github.com/bbsteel/session-insight/internal/reader/capability"

// Capabilities returns the static Agent capability declaration for Worktree
// Review sessions.
//
// Evidence (implementation + tests; not product guesswork):
//   - discovery/replay: journal directory walk + event/result mapping,
//     conformance and mapping tests
//   - realtime: LiveRevision stats the journal files; SessionLive uses the
//     journal heartbeat and terminal state (no process scanning)
//   - tokens: provider_call.completed payloads carry input/output tokens
//     when the writer records them (blocked fixture)
//   - tool_results: stage/dimension/provider invocation↔result pairing by
//     ToolCallID, including failure evidence
//   - diff: Worktree Review never edits the reviewed code; the concept does
//     not exist for this Agent
//   - subtasks: provider_call.completed may carry child_agent_type /
//     child_session_id when a local-cli provider reliably reports them;
//     absent that evidence the linkage stays empty and is never guessed
//   - resume: a review attempt is a one-shot pipeline run; retry creates a
//     new attempt, there is no native resume identity
//   - delete: the journal is the authoritative review record owned by
//     Worktree Review; Session Insight must not delete it
//   - terminate: attempts are in-process pipeline runs of the review
//     service, not agent processes Session Insight manages
func Capabilities() capability.AgentCapabilities {
	return capability.AgentCapabilities{
		AgentType:       AgentType,
		DisplayName:     "Worktree Review",
		AdapterRevision: adapterRevision,
		ResumeCommand:   nil,
		Capabilities: map[capability.CapabilityID]capability.CapabilityDeclaration{
			capability.CapabilityDiscovery:   capability.Exact(),
			capability.CapabilityReplay:      capability.Exact(),
			capability.CapabilityRealtime:    capability.Exact(),
			capability.CapabilityTokens:      capability.Exact(),
			capability.CapabilityToolResults: capability.Exact(),
			capability.CapabilityDiff:        capability.NotApplicable("review_only_no_edits"),
			capability.CapabilitySubtasks:    capability.Estimated("child_ids_only_when_provider_reports"),
			capability.CapabilityResume:      capability.NotApplicable("one_shot_attempts_retry_creates_new"),
			capability.CapabilityDelete:      capability.Unsupported("journal_owned_by_worktree_review"),
			capability.CapabilityTerminate:   capability.NotApplicable("no_agent_process"),
		},
	}
}
