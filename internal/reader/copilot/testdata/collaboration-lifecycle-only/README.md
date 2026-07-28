# Fixture: Copilot lifecycle-only child invocation (collaboration archetype 3)

Provenance: synthetic and sanitized. Hand-authored for the collaboration data
evidence task; structurally derived from the real Copilot session-state layout
as mirrored by the adapter's existing inline test fixtures
(`TestInsightEvidenceAttribution` in `copilot_insight_test.go`,
`writeCopilotBasicFixture` in `conformance_test.go`). It is not a captured
real session and contains no real usernames, paths, prompts, or identifiers.

Structure mirrors `~/.copilot/session-state/<session-id>/`:

- `collab-copilot-1/workspace.yaml` — session metadata.
- `collab-copilot-1/events.jsonl` — the single parent event stream. It
  contains two delegated subagents:
  - `call-task-A`: full lifecycle — `tool.execution_start` (task tool with
    delegation arguments) → `subagent.started` → two attributed
    `assistant.message` responses → `subagent.completed` →
    `tool.execution_complete` with tool telemetry.
  - `call-task-B`: orphaned lifecycle — `tool.execution_start` →
    `subagent.started`, and never a `subagent.completed`.

Facts this fixture evidences:

- the child invocation identity is the parent task call's `toolCallId`;
  `subagent_id`/`agentDisplayName` are not identity keys for the insight
  parser;
- launch is exactly anchored: `subagent.started.toolCallId` joins the task
  `tool.execution_start` with its delegation arguments (description, name,
  model, mode, prompt);
- start/end timestamps on the lifecycle events are exact; a
  started-without-completed invocation is distinguishable (emitted with
  `StartedAt` set and empty `CompletedAt`) and is excluded from response
  attribution;
- child content is a reconstructed time window of parent-stream events
  yielding aggregate counts only (request count, output tokens) — there is no
  independent child transcript, resume, or delete;
- parsed evidence is identical across two independent parses;
- the collaboration reader (`collaboration.go`) normalizes both lifecycle
  events: completed vs orphaned (started-without-completed, root no longer
  live) children are distinguished in the normalized graph, and
  `subagent_started` render markers carry the child invocation ID;
- remaining gap (recorded, not fixed here): the render and TurnVM paths
  consume only `subagent.started`, so an orphaned child still looks identical
  to a finished one in replay; only `started`/`completed` exist, with no
  explicit failed/cancelled vocabulary.
