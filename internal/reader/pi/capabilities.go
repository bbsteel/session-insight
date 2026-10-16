package pi

import (
	"os"
	"path/filepath"

	"github.com/bbsteel/session-insight/internal/reader/capability"
)

// Capabilities returns the static Agent capability declaration for Pi.
//
// Evidence (implementation + tests; not product guesswork):
//   - discovery/replay: registry + JSONL session walk/parse/render
//   - realtime: LiveRevision (mtime+size of the session JSONL)
//   - tokens: assistant and usage-entry usage → exclusive SessionBilling
//   - tool_results: toolCall blocks paired to toolResult messages by toolCallId
//   - diff: native edit tool path + edits[{oldText,newText}] via ExtractEditCalls
//   - subtasks: not applicable — Pi does not persist sub-agents (fork/clone
//     create independent sessions, not a nested invocation graph)
//   - resume: header session id is the native `pi --session {id}` identity
//   - delete: SessionDeleter removes every JSONL whose filename encodes the id
//   - terminate: SessionProcessFinder via fd holders of the open JSONL
func Capabilities() capability.AgentCapabilities {
	return capability.AgentCapabilities{
		AgentType:       agentType,
		DisplayName:     displayName,
		AdapterRevision: 2,
		ResumeCommand: &capability.ResumeCommandDeclaration{
			Executable:   "pi",
			StandardArgs: []string{"--session", "{id}"},
			ModelFlag:    "--model",
		},
		Capabilities: map[capability.CapabilityID]capability.CapabilityDeclaration{
			capability.CapabilityDiscovery:   capability.Exact(),
			capability.CapabilityReplay:      capability.Exact(),
			capability.CapabilityRealtime:    capability.Exact(),
			capability.CapabilityTokens:      capability.Exact(),
			capability.CapabilityToolResults: capability.Exact(),
			capability.CapabilityDiff:        capability.Exact(),
			capability.CapabilitySubtasks:    capability.NotApplicable("no_native_subagents"),
			capability.CapabilityResume:      capability.Exact(),
			capability.CapabilityDelete:      capability.Exact(),
			capability.CapabilityTerminate:   capability.Exact(),
		},
	}
}

// ResolveSessionsDir follows Pi's documented overrides, then the default
// ~/.pi/agent/sessions layout. PI_CODING_AGENT_SESSION_DIR wins over
// PI_CODING_AGENT_DIR/sessions, which wins over the home default.
func ResolveSessionsDir() (string, bool) {
	if path := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); path != "" {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path, true
		}
	}
	if path := os.Getenv("PI_CODING_AGENT_DIR"); path != "" {
		sessions := filepath.Join(path, "sessions")
		if info, err := os.Stat(sessions); err == nil && info.IsDir() {
			return sessions, true
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	sessions := filepath.Join(home, ".pi", "agent", "sessions")
	if info, err := os.Stat(sessions); err == nil && info.IsDir() {
		return sessions, true
	}
	return "", false
}
