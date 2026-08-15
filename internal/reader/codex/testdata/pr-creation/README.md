# Codex PR/MR creation fixtures

These JSONL fixtures are synthetic and sanitized. They preserve the closed
creation-evidence chain observed in real Codex rollouts without containing a
real username, home directory, repository remote, commit, prompt, or business
data.

- `created.jsonl`: a successful `gh pr create` invocation paired by call
  identity with a completed result that returns one GitHub PR URL. The
  transcript also contains a prose URL mention and an `echo gh pr create`
  command, which must not become creation evidence.

Invocation and result identities can be inspected independently from the
rendered `EventID` / `ParentEventID` pair. The returned URL is a public
`github.com` fixture, not a hosted snapshot of any real pull request.
