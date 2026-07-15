# Agent Instructions

- After every code or configuration change, provide a verifiable local address or command so the user can validate the result directly.
- If the application depends on a backend API, the verifiable address must be the full working app with backend connected. Do not provide a frontend-only dev-server URL unless the backend/API path is also verified.
- After completing a full modification, run `./run.sh all` so the complete local app is restarted and ready for validation.
- Write Git commit messages in English, including both the subject and body.

## Worktree Development and Runtime Isolation

- Perform concurrent development and validation in separate linked Git worktrees. Do not run multiple development instances from the same checkout.
- The primary checkout owns port 8080 and the default `~/.session-insight` database. In a linked worktree, `./run.sh all` automatically uses an OS-assigned random loopback port and stores its database, PID, log, and discovered URL under that worktree's `.runtime/` directory.
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
