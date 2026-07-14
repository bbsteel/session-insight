# Agent Instructions

- After every code or configuration change, provide a verifiable local address or command so the user can validate the result directly.
- If the application depends on a backend API, the verifiable address must be the full working app with backend connected. Do not provide a frontend-only dev-server URL unless the backend/API path is also verified.
- After completing a full modification, run `./run.sh all` so the complete local app is restarted and ready for validation.

## Port Allocation

- The main instance owns port 8080. Extra instances (worktree validation, parallel debugging) take ports in +10 steps: 8090, then 8100, and so on. Never use 8081 — a vite dev server often sits there proxying `/api` back to the stale 8080 binary, so curl "works" while validating old code.
- Before starting an instance, confirm the port is free (`ss -tlnp | grep <port>`); after starting, check the log for `bind: address already in use` — run.sh prints the URL before binding, so a successful-looking startup message does not mean the server is actually up.

## Terminal Interaction Positioning

- Keep terminal hit-testing and hover rendering anchored to xterm APIs. Clickable terminal affordances should use the established matcher + buffer scan + xterm MouseService + marker/decoration pattern.
- Do not introduce independent DOM overlay coordinate math for terminal rows. Hand-rolled `getBoundingClientRect()`/`cellHeight` calculations can drift from xterm by 1-2 rows because xterm accounts for screen padding, renderService CSS cell dimensions, ceil/clamp behavior, and viewport state internally.
- Use xterm marker/decoration rendering for hover feedback so visual positioning shares xterm's own viewport math.

## Product Scope

- Do not optimize new UI work for mobile. Design and verify the application as a desktop/local developer tool unless the user explicitly requests mobile support.
