# Fixture: Codex standalone child Session (collaboration archetype 1)

Provenance: synthetic and sanitized. Hand-authored for the collaboration data
evidence task; structurally derived from the real Codex rollout layout as
mirrored by the adapter's existing inline test fixtures
(`writeCodexRichFixture` in `evidence_test.go`). It is not a captured real
rollout and contains no real usernames, paths, prompts, or identifiers.

Structure under `sessions/` mirrors `~/.codex/sessions/YYYY/MM/DD/`:

- `2026/01/02/rollout-2026-01-02T00-00-00-019f0000-0000-7000-8000-0000000000aa.jsonl`
  Root rollout. `session_meta.payload.id` is the root's native thread UUID.
- `2026/01/02/rollout-2026-01-02T00-00-01-019f0000-0000-7000-8000-0000000000bb.jsonl`
  Child rollout. Its `session_meta.payload` carries the collaboration lineage:
  `id` (the child's own native UUID), `session_id` and `parent_thread_id`
  (both pointing at the root's native UUID), `thread_source: "subagent"`,
  and `agent_path: "/root/audit"`.

Facts this fixture evidences:

- the child is a standalone rollout file parsed by the same reader path as any
  session, with its own stable identity (rollout file stem as `Session.ID`,
  native `payload.id` as `ResumeID`);
- `thread_source == "subagent"` gates `IsSubagent`, `ParentSessionID`
  (`parent_thread_id`), and `AgentPath` in `readSessionMeta`;
- the child's lineage is identical across two independent parses;
- the child transcript is independently retrievable via `GetSession` and
  `GetRenderEvents` (backing-session content availability);
- the reader's own `ListSessions` returns the child unfiltered; hiding child
  rollouts from the root Session list is a server-layer concern
  (`internal/server/handlers.go`), not a reader concern.
