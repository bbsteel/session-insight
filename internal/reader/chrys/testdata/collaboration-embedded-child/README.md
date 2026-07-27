# Fixture: Chrys embedded child transcript (collaboration archetype 2)

Provenance: synthetic and sanitized. Hand-authored for the collaboration data
evidence task; structurally derived from the real Chrys session layout as
mirrored by the adapter's existing inline test fixtures (`fixtureMain` /
`fixtureSub` in `chrys_test.go`). It is not a captured real session and
contains no real usernames, paths, prompts, or identifiers.

Structure mirrors `~/.chrys/sessions/<session-dir>/`:

- `28491d6d491e/session.json` — root session. One assistant `function_call`
  (`call_id: "call_sub_1"`, name `explore_agent`) delegates to a child agent;
  the matching `function_result` with the same `call_id` returns its summary.
- `28491d6d491e/sub_agents/sessions/explore_agent_e9a4ee5e36db.json` — embedded
  child transcript (`record_type: "sub_agent_session"`). Its
  `meta.parent_provider_call_id: "call_sub_1"` is the exact join key to the
  parent's `function_call`. `meta.status` and `meta.invocation_id` exist in
  the source but are currently unused by the adapter.

Facts this fixture evidences:

- the parent-child join is exact and two-sided: the child sidecar's
  `parent_provider_call_id` equals the parent `function_call.call_id`;
- the child transcript is embedded under the parent session directory and is
  spliced into the parent's render events at `Depth+1`, bracketed by
  `subagent_started` / `subagent_summary` markers, before the summary
  `ToolResult` whose `ParentEventID` is `call-<call_id>`;
- the spliced event sequence is identical across two independent parses;
- child records are structurally excluded from `ListSessions` (they live one
  level below the scanned session directories), so no child can appear as a
  root row;
- current gaps (recorded, not fixed here): child `status`, `created_at`, and
  `invocation_id` are parsed-or-present but unused, so running vs orphaned
  children cannot be distinguished from adapter output today.
