package claude

import "github.com/bbsteel/session-insight/internal/reader/capability"

// Capabilities returns the static Agent capability declaration for Claude Code.
//
// Evidence (implementation + tests; not product guesswork):
//   - discovery/replay: registry + BaseSessionReader, ListSessions/GetSession/Render
//   - realtime: LiveRevision (stat mtime+size of transcript)
//   - tokens: structured usage on assistant messages → TokenUsage / RenderTokenUsage
//   - tool_results: ToolInvocation↔ToolResult pairing (incl. subagent dual queue)
//   - diff: Edit / str_replace_editor tool input → ExtractEditCalls
//   - subtasks: ParseClaudeRenderEventsWithSubagents splices agent-*.jsonl;
//     ReadCollaboration normalizes Agent/Task + TaskOutput + sidecar
//     meta.toolUseId / agentId into the shared collaboration graph
//     (collaboration.go + fixtures + suite)
//   - resume: session UUID is the CLI --resume argument; declaration below
//     owns the executable and safe/unsafe argument templates
//   - delete: SessionDeleter.DeleteSession + delete tests
//   - terminate: SessionProcessFinder via ~/.claude/sessions heartbeat PID
func Capabilities() capability.AgentCapabilities {
	return capability.AgentCapabilities{
		AgentType:       "claude",
		DisplayName:     "Claude Code",
		AdapterRevision: 4,
		ResumeCommand: &capability.ResumeCommandDeclaration{
			Executable:   "claude",
			StandardArgs: []string{"--resume", "{id}"},
			UnsafeArgs:   []string{"--dangerously-skip-permissions", "--resume", "{id}"},
			ModelFlag:    "--model",
		},
		Capabilities: map[capability.CapabilityID]capability.CapabilityDeclaration{
			capability.CapabilityDiscovery:   capability.Exact(),
			capability.CapabilityReplay:      capability.Exact(),
			capability.CapabilityRealtime:    capability.Exact(),
			capability.CapabilityTokens:      capability.Exact(),
			capability.CapabilityToolResults: capability.Exact(),
			capability.CapabilityDiff:        capability.Exact(),
			capability.CapabilitySubtasks:    capability.Exact(),
			capability.CapabilityResume:      capability.Exact(),
			capability.CapabilityDelete:      capability.Exact(),
			capability.CapabilityTerminate:   capability.Exact(),
		},
	}
}
