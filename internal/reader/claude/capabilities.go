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
//   - subtasks: ParseClaudeRenderEventsWithSubagents splices agent-*.jsonl
//   - resume: session UUID is the CLI --resume argument (frontend uses id)
//   - delete: SessionDeleter.DeleteSession + delete tests
//   - terminate: SessionProcessFinder via ~/.claude/sessions heartbeat PID
func Capabilities() capability.AgentCapabilities {
	return capability.AgentCapabilities{
		AgentType:       "claude",
		DisplayName:     "Claude Code",
		AdapterRevision: 1,
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
