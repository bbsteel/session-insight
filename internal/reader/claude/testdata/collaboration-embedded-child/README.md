# Fixture: Claude Code embedded child transcript (collaboration archetype)

Provenance: synthetic and sanitized. Hand-authored for the Claude
`ReadCollaboration` implementation. Field shapes match Claude Code v2.1
async Agent + TaskOutput records; no real usernames, paths, prompts, or
identifiers.

Layout mirrors `~/.claude/projects/<project-key>/`:

- `-tmp-collab/cccccccc-dddd-eeee-ffff-000000000001.jsonl` — root session. One `Agent`
  tool_use (`toolu_explore_1`, `subagent_type: Explore`) launches a child;
  the immediate `toolUseResult` is `{isAsync, status: async_launched,
  agentId}`. A later `TaskOutput` waits on the same `task_id` and its
  result records `task.status: completed`.
- `-tmp-collab/cccccccc-dddd-eeee-ffff-000000000001/subagents/agent-a111explore0001.jsonl`
  — embedded child transcript.
- `-tmp-collab/cccccccc-dddd-eeee-ffff-000000000001/subagents/agent-a111explore0001.meta.json`
  — `toolUseId`, `agentType`, and `description` join keys.

Facts this fixture evidences:

- identity is the native `agentId` (`a111explore0001`), not a positional
  synthesis or the tool_use id;
- the parent-child join is exact: `meta.toolUseId == Agent tool_use.id`;
- completion is exact via `TaskOutput.task.task_id == agentId`;
- the child transcript is embedded and spliced into the parent render
  stream at `Depth+1` with `InvocationID` set;
- sidecar files are not listed as root Sessions;
- no `BackingSessionRef` (embedded, not standalone).
