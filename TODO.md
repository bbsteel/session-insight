# Roadmap

This roadmap lists pending product work without assigning features to release versions.

## Agent instruction provenance

- Add one-click installation and verification of agent-native lifecycle hooks that record rule-loading provenance without changing agent behavior.
- For supported agents, capture the rule sources actually loaded for a session, their precedence order, content hashes, capture time, and evidence level (`captured`, `agent-reported`, `inferred`, or `unknown`).
- Show rule provenance in the session view, including the exact rules snapshot when retained and a clear explanation when SI cannot verify a rule set.
- Define adapter capabilities and conformance fixtures for rule provenance so unsupported agents fail visibly rather than producing guessed history.

## Engineering principles workspace

- Provide a workspace for authoring and versioning personal or team engineering principles.
- Let users review and explicitly publish those principles through each agent's recognized global instruction mechanism, such as `AGENTS.md`, Rules, or always-on memory; do not rely on private, per-session prompt injection.
- Preview the agent-specific target files, precedence, and resulting content before applying a change.
- Connect published principle versions to subsequent session provenance records, so users can verify that a session actually loaded the intended rules.

## Session navigation

- Investigate remaining MiniMap drag jank. Current implementation uses pixel-based scrolling and requestAnimationFrame batching, but real use still feels stuck or stepped.
- Re-evaluate whether the current MiniMap should remain a primary navigation surface. The dense token bars, tiny markers, and drag viewport may be hard to use in real sessions.
- Consider replacing the current MiniMap with a simpler session outline:
  - user prompt anchors
  - anomalies and compaction points
  - search result markers
  - jump buttons and keyboard navigation
- If a MiniMap remains, treat it as a passive overview first and a precision drag control only if it can be made clearly smoother than native scrolling.

## Investigate when reproducible

- Codex session's final assistant message may be missing from the interaction-message navigation panel (reported 2026-07-27; no longer reproducible because the live source changed).
  - Session: `rollout-2026-07-26T22-16-57-019f9ec9-434a-7f53-b2c0-0935eccf855f` (was still being written when reported).
  - Reproduction: open a Codex session while it is live and verify that the final navigation entry follows the final `agent_message` in the source file; capture any non-404 failure from `fetchLiveRevision`.
  - Investigate whether a transient `fetchLiveRevision` failure ends the polling loop, whether the issue is a live-indexing delay, or whether Codex has not yet written the final streamed `agent_message`.
