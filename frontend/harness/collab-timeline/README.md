# Collaboration Timeline Production Harness

Focused, self-contained browser harness for the production collaboration
timeline core (`src/collaboration/*` + `src/components/CollaborationTimeline.tsx`).
It mounts the real production component — React, production i18n, and
`src/app.css` — against deterministic contract-shaped datasets and exposes a
measurement/assertion API on `window.__collab`.

This harness does **not** mount the component in `ReplayView`, fetch the real
API, or touch the terminal; the stub below the component only proves the
component composes as a flex sibling. The later dock-integration task owns
those boundaries.

## Layout of this directory

```text
src/datasets.ts     deterministic production-test datasets (mulberry32 seeded):
                    contract-shaped CollaborationGraphDTO at typical 30 /
                    large 100 / stress 200 lanes; depth-4 chain; running/
                    waiting/failed/orphaned/unknown; missing+estimated
                    boundaries; bursts; en+zh labels; withActivitySpans
                    reproduces the spike segment counts for the LOD path
src/harness.tsx     page entry; mounts I18nProvider + CollaborationTimeline;
                    window.__collab measurement/assertion API
harness.html        harness chrome (header tag + inert terminal stub)
scripts/lib.mjs     esbuild bundle + static server + stats + env info
scripts/run-bench.mjs       performance gates runner (writes report/results.json)
scripts/run-assertions.mjs  structural browser assertions (exit 1 on fail)
scripts/capture-screenshots.mjs  screenshot matrix -> /tmp (disposable)
report/             benchmark output (results.json)
```

## Reproduction

Requires `npm ci` in `frontend/` and Playwright Chromium
(`npm --prefix frontend exec -- playwright install chromium`).

```bash
# benchmark against the accepted gates (full run; --quick for a fast smoke)
node frontend/harness/collab-timeline/scripts/run-bench.mjs

# structural assertions (typical + stress, light + dark, en + zh)
node frontend/harness/collab-timeline/scripts/run-assertions.mjs

# screenshot matrix -> /tmp/session-insight-ui/collab-timeline (disposable)
node frontend/harness/collab-timeline/scripts/capture-screenshots.mjs

# type check
frontend/node_modules/.bin/tsc -p frontend/harness/collab-timeline/tsconfig.json
```

The interactive harness can also be opened manually: serve this directory
statically and open `harness.html?dataset=typical&theme=dark&lang=en`
(params: `dataset=typical|large|stress`, `theme=light|dark`, `lang=en|zh`).

These scripts are intentionally **not** wired into the frontend `npm test`
aggregator: the benchmark is environment-dependent, and the browser
assertions need a local Chromium. The durable headless logic tests live in
`frontend/scripts/test-collaboration-*.mjs` (wired into `npm test`).

## What is measured

Per dataset: pure layout time, first visible render after data (React remount
+ layout + paint, double rAF), mounted SVG node count, vertical scroll frames,
drag-pan frames, ctrl+wheel zoom frames, hover latency, select latency,
live-geometry refresh (nowMs advance through props), and JS heap over 20
dataset switches. Frame metrics measure event -> post-rAF and sit near the
headless 60 Hz floor; sub-frame costs are visible in the synchronous mount
column.
