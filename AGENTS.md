# Agent Instructions

- After every functional code or runtime configuration change, provide a verifiable local address or command so the user can validate the result directly.
- If the application depends on a backend API, the verifiable address must be the full working app with backend connected. Do not provide a frontend-only dev-server URL unless the backend/API path is also verified.
- After completing a modification that affects functionality or runtime behavior, first check the orchestration environment's active-agent record and follow **Worktree Development and Runtime Isolation** below, then run `./run.sh all` in the correct checkout so the complete local app is restarted and ready for validation.
- Documentation, comments, repository metadata, and agent-instruction-only changes do not require starting or restarting the application; validate them with an appropriate diff, lint, or text-check command instead.
- Write Git commit messages in English, including both the subject and body.

## Branch and PR Workflow (required)

All code and docs changes land on `main` **only via pull request**. Do not commit product work directly onto `main`, and do not `git push origin main` for feature or fix delivery.

### Always update `main` before creating a feature branch (required)

**Before** `git switch -c` / `git checkout -b` for any new `feat/*`, `fix/*`, `docs/*`, or `chore/*` branch:

1. `git fetch origin` so remote refs are current.
2. Confirm the base is latest remote main — prefer creating from `origin/main` directly (no stale local `main` required).
3. Only then create the feature branch.

Do **not** branch off an outdated local `main`, an old feature branch, or the previous task’s HEAD without fetching first. Stale bases cause avoidable conflicts with open PRs and may fail GitHub’s “require branch up to date before merging” protection when the branch is still behind the target at merge time.

Recommended one-liner (preferred — does not require checking out local `main`):

```bash
git fetch origin && git switch -c my-branch origin/main
```

If you intentionally keep a local `main` checkout in sync first:

```bash
git fetch origin && git switch main && git pull --ff-only origin main && git switch -c my-branch
```

For **every** change set (including small fixes and agent-instruction edits):

1. **Update remote knowledge, then branch from latest `main`**: `git fetch origin`, then base the branch on **`origin/main`** (or local `main` only after it is fast-forwarded to match remote). Prefer `git switch -c <branch> origin/main` so the new branch tracks current remote history without a local merge step. Never skip the fetch when starting a new task or a new branch.
2. **Create a dedicated branch** with a clear prefix, e.g. `feat/…`, `fix/…`, `docs/…`, `chore/…`. Prefer one logical change per branch.
3. **Commit only on that branch.** Do not leave finished work solely as commits on `main`.
4. **Open a PR into `main`** (`gh pr create` or equivalent). Create the PR as **ready for review** (non-draft); CodeRabbit does not review draft PRs. Default to **not** merging unless the user explicitly asks to merge.
5. **Push the branch**, not `main`, when publishing the change for review.
6. After merge (by user or explicit request), continue the next task from a **new** branch off updated **`origin/main`** (fetch first). Do not merge the feature branch into local `main` as a substitute for the remote PR merge. Do not reuse the previous feature branch as the base for the next task without rebasing onto fresh `origin/main`.
7. **Delete the remote feature branch after the PR is merged** (e.g. `git push origin --delete <branch>`) to keep the repository tidy.

### GitHub authentication from sandboxed agents

- A sandboxed command may be unable to access the host keyring even when the user has a valid `gh` login. If `gh auth status` fails inside the sandbox, do not immediately conclude that the stored credential is invalid or ask the user to log in again.
- Re-run the minimal `gh auth status` check with the environment's controlled sandbox-escalation mechanism. If the host login is valid, use similarly scoped, approved sandbox-external `gh` commands for the required PR operation.
- Escalate only the GitHub command needed for the workflow; do not use authentication troubleshooting as permission to run unrelated commands outside the sandbox. Never print or request the user's token.

### Local `main` is remote-only (required)

Local `main` is a **mirror of `origin/main`**, not a place to integrate feature work.

**Forbidden** (unless the user explicitly orders that exact exception for that action):

- `git merge <feature-branch>` (or `git merge --no-ff`, squash-merge, or cherry-pick of a whole feature) **into local `main`**
- `git rebase <feature-branch>` onto local `main` as a way to “land” work
- Any workflow that lands feature/docs commits on local `main` and then `git push origin main` to publish them
- Treating “merge locally then push `main`” as an alternative to a PR

**Required** when local `main` must move forward:

- `git fetch origin`
- Update local `main` **only** by fast-forward from the remote, e.g. `git switch main && git pull --ff-only origin main`, or `git switch main && git reset --hard origin/main` when a hard reset is appropriate and the user has not forbidden it for that checkout
- If fast-forward is impossible, **stop** and fix the situation (do not invent a local merge of feature branches onto `main` to “catch up”)

**Allowed** integration path: open/merge a PR on the remote; then refresh local `main` from `origin/main` as above. Merging a PR with `gh pr merge` (or the GitHub UI) updates **remote** `main`; that is not a local merge into `main`.

After a remote merge, **do not** automatically `git switch main && git pull` unless the user asked to sync local `main` or the next step clearly requires an up-to-date local `main` checkout. Prefer basing the next branch on `origin/main` after `git fetch`.

Exceptions (must still prefer a branch when practical):

- User explicitly orders a direct commit or direct push to `main` for that action.
- Emergency hotfix the user scopes as “commit on main now” — still prefer a branch + fast PR when time allows.
- User explicitly orders a one-off local merge into `main` — still do not `git push origin main` unless they also ordered that push.

When the working tree already has uncommitted or local-only work on `main` that belongs to the current task, **move it onto a feature branch before committing** (e.g. create branch from current HEAD, or `git switch -c` then commit). Do not “finish on main and PR later” as the default path.

## Worktree Development and Runtime Isolation

- Perform concurrent development and validation in separate linked Git worktrees. Do not run multiple development instances from the same checkout.
- Before starting or restarting for validation, first use the orchestration environment's active-agent list or equivalent explicit record to determine whether other agents are working concurrently. The filesystem test in `run.sh` identifies only the checkout type: the primary checkout has a `.git` directory, while a linked worktree has a `.git` file; it does not by itself detect multi-agent activity. Do not infer either condition from an occupied port.
- If multiple agents are active and the current checkout is the primary checkout, do not start validation or continue concurrent writes there. Pause and coordinate the agents, preserve the combined dirty state as a checkpoint if necessary, then resume each task in a dedicated linked worktree. If no other agent is active, validation may run from the primary checkout.
- The primary checkout owns port 8080 and the default `~/.session-insight` database. In a linked worktree, `./run.sh all` uses an OS-assigned random loopback port on the first run and reuses the same port on subsequent restarts (persisted to `.runtime/session-insight.port`), with its database, PID, log, and discovered URL stored under that worktree's `.runtime/` directory. A non-empty `PORT` env var overrides the persisted port.
- Treat the current worktree's `.runtime/session-insight.pid`, `.runtime/session-insight.log`, and `.runtime/session-insight.url` files as the authoritative record of a linked-worktree instance. Check and reuse those records when determining whether that multi-agent instance is already running.
- Treat the post-bind `Ready:` URL printed by `run.sh` as the authoritative address. Do not guess a port or use a URL printed before the backend has successfully bound its socket.
- Multiple worktree instances may read the same live agent session roots, but only the primary instance may perform destructive operations against them, including deleting or stopping sessions. Use fixtures or snapshots when validation requires deterministic session counts or content.
- Stop only the process recorded by the current worktree's PID file. Never use broad process-name matching or kill an unrelated listener merely because it occupies an expected port.
- Keep runtime artifacts owned by a worktree inside that worktree. `PORT` and `SI_DATA_DIR` are escape hatches for explicit validation needs, not required per-instance setup.

## Terminal Interaction Positioning

- Keep terminal hit-testing and hover rendering anchored to xterm APIs. Clickable terminal affordances should use the established matcher + buffer scan + xterm MouseService + marker/decoration pattern.
- Do not introduce independent DOM overlay coordinate math for terminal rows. Hand-rolled `getBoundingClientRect()`/`cellHeight` calculations can drift from xterm by 1-2 rows because xterm accounts for screen padding, renderService CSS cell dimensions, ceil/clamp behavior, and viewport state internally.
- Use xterm marker/decoration rendering for hover feedback so visual positioning shares xterm's own viewport math.

## Localization completeness

- All new or changed user-facing copy must be added to every locale in `frontend/src/i18n.tsx` and rendered through `t(...)`; do not add display text directly in components.
- Run `npm --prefix frontend run test:i18n` after changing translations. This test enforces locale-key parity and representative interpolation behavior.
- `npm --prefix frontend run test:i18n-source` is a CI ratchet against Chinese literals outside the translation catalog. Do not update its baseline to admit new UI copy. Run `npm --prefix frontend run test:i18n-source:update` only when removing or intentionally migrating legacy literals, and review the baseline diff with the code change.
- For rendered UI changes, validate both `en` and `zh-CN` in the live Playwright check. Assertions must cover the changed labels, tooltips, dialogs, and success/error states that the flow can exercise.

## Frontend Validation (Playwright)

Frontend unit scripts under `frontend/scripts/` (pure Node + `tsc` of isolated modules) remain the default for **logic-only** modules with no DOM or layout surface (`scrollSync`, `sidebarRows`, outline parsers, etc.). They do **not** replace browser checks when the user-visible UI can break.

### What PR CI already runs

`.github/workflows/ci.yml` runs `npm test` in `frontend/` on every pull request (after `npm ci`, lint, and `tsc --noEmit`). That script is an **explicit aggregator** in `frontend/package.json` (`"test": "npm run test:scroll && …"`). CI does **not** auto-discover scripts by filename.

### How to make a new frontend test run on PRs

For any **durable, headless unit script** meant as regression coverage:

1. Add `frontend/scripts/test-<name>.mjs` (or extend an existing script).
2. Add a matching npm script, e.g. `"test:<name>": "… tsc … && node scripts/test-<name>.mjs"`.
3. **Add** `npm run test:<name>` to the `"test"` aggregator in `frontend/package.json` (keep `test:*` keys and the aggregator chain in alphabetical order so the block stays scannable). Omitting this step means local `npm run test:<name>` works but **PR CI never runs it**.
4. Do not put live-app Playwright scripts into `"test"` unless they are fully headless, self-contained (fixtures/mocks, no dependency on the developer’s local sessions or a manually started `./run.sh`), and finish reliably on `ubuntu-latest` without interactive display setup beyond what CI already has.

Optional: if a future browser e2e suite is added for CI, wire it as a **separate** npm script (e.g. `test:e2e`) and a dedicated CI step (install Chromium, start the app against fixtures, run Playwright). Do not silently fold flaky live-session scripts into the existing `npm test` chain.

### Live Playwright validation (agent / local, not PR CI by default)

For changes that affect **rendered UI, layout, CSS, interactions, or multi-component wiring** (components under `frontend/src/components/`, app shell, terminal/xterm UI, minimap chrome, modals, filters, panels, theme):

1. **Run the full app** first (`./run.sh all` in the correct checkout; use the post-bind `Ready:` URL / worktree `.runtime/session-insight.url`). Do not validate against a frontend-only Vite URL unless the API path is also verified.
2. **Write or extend a Playwright script** that exercises the changed path against that running instance. Prefer a focused throwaway or `frontend/scripts/` script over claiming “looks fine” without automation. Playwright is already a `frontend` devDependency; Chromium: `npm --prefix frontend exec -- playwright install chromium`.
3. **Assert first (works for every agent, with or without vision).** Prefer checks that do not require looking at pixels: visible text/roles, counts, selected state, URL/hash, enabled/disabled, `getBoundingClientRect()` / box model (overlap, zero size, off-screen, order), computed styles when relevant, console errors, network failures. Encode the acceptance criteria in the script so a text-only agent can still get a hard pass/fail.
4. **Screenshot when visual judgment is still needed** — layout shifts, spacing, overflow, theme, terminal/minimap alignment, panel chrome, or defects that structural assertions cannot prove. Save under a disposable path (e.g. worktree `.runtime/` or `/tmp/session-insight-ui/`).
   - **Multimodal agents** (can open images via the file/image reader): open the PNG and decide pass/fail from pixels; do not stop at “screenshot written”.
   - **Text-only / non-vision agents**: still capture screenshots and print their absolute paths, but **do not claim visual QA passed**. State clearly that pixel review is deferred; hand the paths to the user (or a vision-capable agent) and limit your own pass/fail claim to the assertion results in step 3. Optionally dump accessibility tree or key bounding boxes next to the screenshot for a stronger text report.
5. **Do not** treat README capture (`npm --prefix frontend run capture:screenshots`) as product regression coverage; that workflow is only for publishing sanitized marketing screenshots (see `assets/screenshots/README.md`).
6. Pure logic refactors with existing `npm --prefix frontend run test*` coverage and no UI surface still need those unit scripts (wired into `"test"` as above); live Playwright is not mandatory for them.

Agent-instruction or docs-only edits do not require Playwright.

## Product Scope

- Do not optimize new UI work for mobile. Design and verify the application as a desktop/local developer tool unless the user explicitly requests mobile support.
