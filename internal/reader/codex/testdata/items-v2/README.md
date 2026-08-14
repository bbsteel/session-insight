# Codex paginated-history fixtures (items v2)

These JSONL fixtures are synthetic and sanitized. They preserve the
`event_msg/item_completed` shapes observed in Codex CLI 0.147.0 "paginated"
history mode without containing a real username, home directory, repository
remote, prompt, or business data.

Per the adapter onboarding guide (format-change coverage), the same logical
session exists in both event styles:

- `sessions/.../rollout-2026-08-11T00-00-00-…0001.jsonl`: legacy style —
  text arrives via `event_msg/user_message` and `event_msg/agent_message`.
- `sessions/.../rollout-2026-08-11T00-00-00-…0002.jsonl`: paginated style —
  text arrives via `event_msg/item_completed` items (`UserMessage` /
  `AgentMessage`), plus `task_complete.last_agent_message`.

The paginated fixture covers:

- turn 1: user + assistant items, a `response_item` function_call pair, and a
  duplicate `CommandExecution` item that must NOT be counted as a second tool
  call;
- turn 2: a pure message turn (no tool calls) that the legacy-only parser
  dropped entirely via empty-turn filtering;
- turn 3: no AgentMessage item — the assistant text only exists as
  `task_complete.last_agent_message` (fallback path);
- a `FileChange` item that must produce neither text nor tool events.

Tests assert both styles produce equivalent user/assistant text and that
tool-call counts come only from `response_item` records.
