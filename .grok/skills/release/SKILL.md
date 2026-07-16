---
name: release
description: >
  Cut a GitHub Release for session-insight: propose a semver bump from
  commits since the last tag, refresh README.md and README_ZH.md, author
  bilingual structured release notes (Highlights / Features / Bug Fixes),
  create the GitHub Release with those notes, and push the v* tag so CI
  attaches multi-platform binaries. Use when the user says "release",
  "cut a release", "ship", "publish release", "bump version", "tag and
  release", or runs /release. Do not use for ordinary commits or PRs.
metadata:
  short-description: "Release to GitHub with bilingual notes"
---

# /release — GitHub Release

Publish a versioned GitHub Release for **session-insight**. Prefer this skill
over raw tag pushes so notes stay high-quality. The CI workflow
(`.github/workflows/release.yml`) **uploads binaries to a pre-created
Release** when one already exists; only fall back to auto-grouped commit
notes when no Release is present.

## Triggers

- User says: release, cut a release, ship this, publish release, make a
  release, bump and release, tag release
- Slash: `/release` or `/release vX.Y.Z`

## Preconditions (abort if unmet)

1. Repo root is `session-insight` with remote `origin` on GitHub.
2. `gh` is authenticated (`gh auth status`).
3. Working tree is clean, **or** only intentional uncommitted release edits
   you are about to include (README / notes). Never release with unrelated
   dirty files.
4. On the release branch (default: `main`), fully up to date with
   `origin`:
   ```bash
   git fetch origin --tags --prune
   git status -sb
   git rev-parse --abbrev-ref HEAD
   ```
5. Latest CI on the commit you will tag is green, or the user explicitly
   waives that check.

If anything is wrong, report it and stop. Do not force-push or rewrite
published tags.

## Step 1 — Resolve version

### If the user supplied a version

Accept forms: `v0.3.0`, `0.3.0`. Normalize to a **`v`-prefixed** tag
(`v0.3.0`). Validate `vMAJOR.MINOR.PATCH` (optional pre-release suffix only
if the user asked for one).

### If the user did **not** supply a version

1. Last tag:
   ```bash
   PREV_TAG=$(git describe --tags --abbrev=0 2>/dev/null || true)
   ```
2. If no previous tag, propose `v0.1.0` and explain.
3. Collect changes since `PREV_TAG`:
   ```bash
   git log "${PREV_TAG}..HEAD" --pretty=format:'%h %s' --no-merges
   git diff --stat "${PREV_TAG}..HEAD"
   ```
4. Propose the **smallest** semver bump that fits the changes:
   | Signal in commits / diff | Bump |
   |--------------------------|------|
   | Breaking API/UX, `BREAKING CHANGE`, major removals | **major** |
   | New user-visible features (`feat`, new agents, new panels) | **minor** |
   | Fixes, polish, docs, chore, CI only | **patch** |
5. Prefer conventional-commit prefixes (`feat:`, `fix:`, `feat(scope):`).
   When mixed, use the highest applicable bump.
6. Show the user a short rationale (prev tag → proposed tag + 3–8 bullet
   reasons) and **wait for explicit approval** of the version before any
   commit, tag, push, or `gh release` call.

Never invent a version higher than the evidence supports, and never skip
approval when the version was auto-proposed.

## Step 2 — Draft structured release notes (EN then ZH)

Write notes from the actual diff and commit subjects since `PREV_TAG`.
Group and rewrite into product language — do not dump raw commit subjects
as the final notes.

### Required structure (English first, then Chinese)

Mirror the style of existing releases (see `gh release view v0.2.0`):

```markdown
## Highlights
- **Short title**: one-line impact for the 3–5 biggest user-facing wins

## Features
- **area**: what shipped (user-facing; skip pure refactors unless visible)

## Bug Fixes
- **area**: what was broken and is now fixed

## Other
- optional: docs, CI, internal notes worth mentioning

---

## 亮点
- **短标题**：与英文 Highlights 一一对应

## 新功能
- **领域**：与英文 Features 对应

## 修复
- **领域**：与英文 Bug Fixes 对应

## 其他
- 可选

**Full Changelog**: https://github.com/<owner>/<repo>/compare/<PREV_TAG>...<NEW_TAG>
```

Rules:

- **Highlights** are curated marketing/impact bullets, not a full feature list.
- **Features** / **Bug Fixes** use a bold **area** prefix (`sidebar`, `grok`,
  `terminal`, `analytics`, …) then a concise clause.
- Omit empty sections (no empty `## Other`).
- Chinese sections must match English substance; natural Chinese, not
  machine-calque word salad.
- End with the compare link when `PREV_TAG` exists; otherwise link commits
  for the new tag.
- Save the full notes body to a temp file, e.g.
  `/tmp/session-insight-release-notes-<NEW_TAG>.md` (never under `$HOME`
  cleanup paths).

Show the draft notes to the user with the version. Incorporate feedback
before publishing.

## Step 3 — Refresh README

Update product docs so the tagged commit matches what users will install.

1. Read current `README.md` and `README_ZH.md`.
2. From the same change set as the release, update when needed:
   - **Highlights** / **功能亮点** bullets (add/retire capabilities)
   - **Supported Agents** table (new/removed agents or paths)
   - Getting started / config / privacy if behavior changed
   - Screenshot captions only if UI meaning changed
3. Keep EN and ZH in sync (same facts, idiomatic prose each language).
4. Screenshots (`assets/screenshots/`):
   - If the release changes primary UI surfaces (replay, analytics, code
     reader), offer to recapture per `assets/screenshots/README.md`.
   - Do **not** recapture by default unless the user agrees or screenshots
     are clearly stale relative to shipped UI.
   - After any capture: full-resolution privacy check before commit.
5. Do not claim unreleased or unshipped features.
6. If README needs edits, commit them on the release branch **before**
   tagging (English commit subject + body per project rules), e.g.:

   ```text
   docs: refresh README for vX.Y.Z

   Align highlights and agent table with the release notes.
   ```

If nothing in the README is stale, say so and continue without a docs
commit.

## Step 4 — Final approval gate

Present a single checklist and **wait for explicit go-ahead**:

- Tag: `NEW_TAG` (from `PREV_TAG`)
- Commit to tag: short SHA + subject
- README commit: yes/no
- Notes: full draft (or path to notes file)
- Actions that will run: push commits (if any) → create GitHub Release with
  notes → push/create `v*` tag so Release workflow builds assets

Do not proceed until the user approves (or amends version/notes).

## Step 5 — Publish

Use this order so CI attaches binaries to **your** notes (not the fallback):

1. Ensure release-branch commits are on `origin` (push README commit if you
   made one). Confirm with user before `git push` if project rules require
   it; for an explicit `/release` approval that includes push, proceed.
2. Create the GitHub Release **with notes**, targeting the release commit.
   Prefer creating the tag via `gh` so the Release exists before/with the
   tag event:

   ```bash
   NEW_TAG=vX.Y.Z
   NOTES=/tmp/session-insight-release-notes-${NEW_TAG}.md
   TITLE="Session Insight ${NEW_TAG}"

   gh release create "${NEW_TAG}" \
     --title "${TITLE}" \
     --notes-file "${NOTES}" \
     --target "$(git rev-parse HEAD)"
   ```

   This creates the tag on GitHub at the chosen commit and publishes the
   Release body. The `push: tags: v*` workflow then builds matrices and
   **uploads** archives into the existing Release.

3. Fetch tags locally so the workspace matches remote:

   ```bash
   git fetch origin --tags
   ```

4. Verify:

   ```bash
   gh release view "${NEW_TAG}"
   gh run list --workflow=release.yml --limit 5
   ```

5. Tell the user:
   - Release URL
   - Tag name
   - That multi-platform binaries + `checksums.txt` arrive when the Release
     workflow finishes
   - How to watch: `gh run watch` or the Actions link

### Failure / recovery

- If `gh release create` fails because the tag already exists without a
  Release, create notes-only:
  ```bash
  gh release create "${NEW_TAG}" --title "${TITLE}" --notes-file "${NOTES}"
  ```
  (or `gh release edit` if the Release exists but notes are wrong **and**
  the user asked to fix them).
- If the Release exists and CI already wrote fallback notes, replace notes
  only with user approval:
  ```bash
  gh release edit "${NEW_TAG}" --notes-file "${NOTES}" --title "${TITLE}"
  ```
- **Never** delete a published tag or Release without explicit user request.
- **Never** `--generate-notes` as the primary path; this skill authors notes.

## Step 6 — Post-release hygiene

- Leave the working tree clean.
- Do not bump `frontend/package.json` version unless the repo already
  treats it as the product version (today product version is the git tag).
- Summarize: tag, URL, README changes, workflow status.

## Notes style examples

**Good Highlight**

- **Provider-aware model filters**: expand provider submenus, sort by model
  id, and group unknowns under Other

**Bad**

- `62e152e feat(sessions): add provider-aware model filters`

**Good Feature line**

- **sidebar**: agent filter as an icon bar with overflow dropdown

**Good 亮点**

- **Provider 感知的模型筛选**：按提供商展开子菜单，按模型 id 排序，未知归入 Other

## Out of scope

- Hotfix cherry-picks onto old majors (ask for a dedicated plan)
- Publishing to npm, Homebrew, or non-GitHub channels
- Editing past Releases unless the user explicitly asks to correct notes
- Running destructive git commands (`reset --hard`, force-push to main)
