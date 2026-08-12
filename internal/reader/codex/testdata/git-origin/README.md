# Codex authoritative-envelope fixtures

These JSONL fixtures are synthetic and sanitized. They preserve the current
`session_meta.git` shapes observed in Codex rollouts without containing a real
username, home directory, repository remote, commit, prompt, or business data.

- `complete.jsonl`: complete structured origin metadata plus a completed turn.
- `open-turn.jsonl`: complete origin metadata with a task left open at EOF.
- `partial-invalid.jsonl`: partial metadata with invalid SHA, relative cwd, and
  a credential/query-shaped URL. Tests prove those raw values never escape.
- `absent.jsonl`: no Git object and no session/turn finalization signal.

Codex does not record start dirty state or a session-level finalized marker in
these formats. Tests must therefore keep dirty state and session finalization
non-exact even when `task_complete` exists.
