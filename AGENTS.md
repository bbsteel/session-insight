# Agent Instructions

- After every functional code or runtime configuration change, provide a verifiable local address or command so the user can validate the result directly.
- If the application depends on a backend API, the verifiable address must be the full working app with backend connected. Do not provide a frontend-only dev-server URL unless the backend/API path is also verified.
- After completing a modification that affects functionality or runtime behavior, first check the orchestration environment's active-agent record and follow **Worktree Development and Runtime Isolation** below, then run `./run.sh all` in the correct checkout so the complete local app is restarted and ready for validation.
- Documentation, comments, repository metadata, and agent-instruction-only changes do not require starting or restarting the application; validate them with an appropriate diff, lint, or text-check command instead.
- Write Git commit messages in English, including both the subject and body.

## Branch and PR Workflow (required)

All code and docs changes land on `main` **only via pull request**. Do not commit product work directly onto `main`, and do not `git push origin main` for feature or fix delivery.

For **every** change set (including small fixes and agent-instruction edits):

1. **Start from up-to-date `main`**: `git fetch origin` and base the branch on `origin/main` (or the primary checkout’s `main` after it matches remote when that is the agreed base).
2. **Create a dedicated branch** with a clear prefix, e.g. `feat/…`, `fix/…`, `docs/…`, `chore/…`. Prefer one logical change per branch.
3. **Commit only on that branch.** Do not leave finished work solely as commits on `main`.
4. **Open a PR into `main`** (`gh pr create` or equivalent). Default to **not** merging unless the user explicitly asks to merge.
5. **Push the branch**, not `main`, when publishing the change for review.
6. After merge (by user or explicit request), continue the next task from a **new** branch off updated `main`.

Exceptions (must still prefer a branch when practical):

- User explicitly orders a direct commit or direct push to `main` for that action.
- Emergency hotfix the user scopes as “commit on main now” — still prefer a branch + fast PR when time allows.

When the working tree already has uncommitted or local-only work on `main` that belongs to the current task, **move it onto a feature branch before committing** (e.g. create branch from current HEAD, or `git switch -c` then commit). Do not “finish on main and PR later” as the default path.

## Worktree Development and Runtime Isolation

- Perform concurrent development and validation in separate linked Git worktrees. Do not run multiple development instances from the same checkout.
- Before starting or restarting for validation, first use the orchestration environment's active-agent list or equivalent explicit record to determine whether other agents are working concurrently. The filesystem test in `run.sh` identifies only the checkout type: the primary checkout has a `.git` directory, while a linked worktree has a `.git` file; it does not by itself detect multi-agent activity. Do not infer either condition from an occupied port.
- If multiple agents are active and the current checkout is the primary checkout, do not start validation or continue concurrent writes there. Pause and coordinate the agents, preserve the combined dirty state as a checkpoint if necessary, then resume each task in a dedicated linked worktree. If no other agent is active, validation may run from the primary checkout.
- The primary checkout owns port 8080 and the default `~/.session-insight` database. In a linked worktree, `./run.sh all` automatically uses an OS-assigned random loopback port and stores its database, PID, log, and discovered URL under that worktree's `.runtime/` directory.
- Treat the current worktree's `.runtime/session-insight.pid`, `.runtime/session-insight.log`, and `.runtime/session-insight.url` files as the authoritative record of a linked-worktree instance. Check and reuse those records when determining whether that multi-agent instance is already running.
- Treat the post-bind `Ready:` URL printed by `run.sh` as the authoritative address. Do not guess a port or use a URL printed before the backend has successfully bound its socket.
- Multiple worktree instances may read the same live agent session roots, but only the primary instance may perform destructive operations against them, including deleting or stopping sessions. Use fixtures or snapshots when validation requires deterministic session counts or content.
- Stop only the process recorded by the current worktree's PID file. Never use broad process-name matching or kill an unrelated listener merely because it occupies an expected port.
- Keep runtime artifacts owned by a worktree inside that worktree. `PORT` and `SI_DATA_DIR` are escape hatches for explicit validation needs, not required per-instance setup.

## Terminal Interaction Positioning

- Keep terminal hit-testing and hover rendering anchored to xterm APIs. Clickable terminal affordances should use the established matcher + buffer scan + xterm MouseService + marker/decoration pattern.
- Do not introduce independent DOM overlay coordinate math for terminal rows. Hand-rolled `getBoundingClientRect()`/`cellHeight` calculations can drift from xterm by 1-2 rows because xterm accounts for screen padding, renderService CSS cell dimensions, ceil/clamp behavior, and viewport state internally.
- Use xterm marker/decoration rendering for hover feedback so visual positioning shares xterm's own viewport math.

## Product Scope

- Do not optimize new UI work for mobile. Design and verify the application as a desktop/local developer tool unless the user explicitly requests mobile support.
