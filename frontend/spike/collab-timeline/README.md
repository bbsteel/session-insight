# Collaboration Timeline Renderer Spike

Isolated, reproducible benchmark harness for the Phase 0 renderer decision in
`session-insight-docs/agent-adapters/agent-collaboration-timeline-design.md`
(§11, §12.4, §12.5). It compares the design's first candidate (one SVG
viewport + DOM labels + pure TypeScript layout) against the practical fallback
(a single viewport Canvas with a spatial-index hit test), driven by the same
pure layout engine and the same DOM label/tooltip/keyboard chrome.

This spike does **not** integrate with `ReplayView`, `TerminalPanel`, or any
production code. It adds no dependencies. The spike-only input shapes in
`src/types.ts` are **non-authoritative**: the gate coordinator freezes the
production contract, not this spike.

## Layout of this directory

```text
src/types.ts          non-authoritative spike input model
src/prng.ts           mulberry32 seeded PRNG + FNV-1a fixture hash
src/generator.ts      deterministic datasets: typical 30 lanes / large 100 /
                      stress 200 lanes, 2000 segments, 500 relations; depth-4
                      chain; running/waiting/failed/orphaned/unknown; missing
                      and estimated boundaries; hours-long root with
                      seconds-long children; dense parallel starts; en+zh labels
src/layout.ts         pure TS layout engine -> RenderPrimitives (no DOM/React)
src/candidate.ts      shared renderer candidate interface
src/render-svg.ts     SVG candidate (one <svg>, viewport-culled children)
src/render-canvas.ts  Canvas candidate (sticky viewport canvas, redrawn per frame)
src/harness.ts        harness page + window.__spike measurement API
harness.html          token subset (light/dark), dock + terminal stub
scripts/lib.mjs       esbuild bundle + static server + stats + env info
scripts/run-bench.mjs       benchmark runner (Playwright, median + p95)
scripts/run-assertions.mjs  structural interaction assertions (exit 1 on fail)
scripts/capture-screenshots.mjs  screenshot matrix -> /tmp (disposable)
report/results.json   raw measurements from the reference run
report/BENCHMARK-REPORT.md  concise committed report
```

## Reproduction

Requires `npm ci` in `frontend/` and Playwright Chromium
(`npm --prefix frontend exec -- playwright install chromium`).

```bash
# benchmark (full run; --quick for a fast smoke)
node frontend/spike/collab-timeline/scripts/run-bench.mjs

# structural assertions (both candidates, typical + stress, light + dark)
node frontend/spike/collab-timeline/scripts/run-assertions.mjs

# screenshot matrix -> /tmp/session-insight-ui/collab-spike (disposable)
node frontend/spike/collab-timeline/scripts/capture-screenshots.mjs

# type check
frontend/node_modules/.bin/tsc -p frontend/spike/collab-timeline/tsconfig.json
```

The interactive harness can also be opened manually: serve this directory
statically and open `harness.html?renderer=svg&dataset=typical&theme=dark&lang=en`
(params: `renderer=svg|canvas`, `dataset=typical|large|stress`,
`theme=light|dark`, `lang=en|zh`, `dock=expanded|collapsed`).

These scripts are intentionally **not** wired into the frontend `npm test`
aggregator: the spike is a local, environment-dependent benchmark, not CI
regression coverage.

## What is measured

Per candidate x dataset: pure layout time, first visible render after data
(remount + layout + paint, double rAF), mounted node/primitive count,
vertical scroll frames, horizontal pan frames, zoom frames, hover latency,
select latency, active-lane (live) update cost, and JS heap over 20 dataset
switches (Chromium `--enable-precise-memory-info` + `--expose-gc`).

Equivalence rules: both candidates consume identical `RenderPrimitives` from
the same layout call, share the DOM label column, tooltip portal, roving-
tabindex keyboard model, and theme tokens. "Mounted primitives" counts the
same primitive set on both sides (intervals + edges + markers + hit regions):
DOM nodes for SVG, consumed/drawn primitives for Canvas.

## Known simplifications (documented, not hidden)

- Vanilla DOM harness instead of React. React adds the same constant to both
  candidates; the layout-to-primitives boundary being compared is unchanged.
- No ECharts candidate: the Canvas path measures the same rasterization
  technology without chart-abstraction overhead; see the report for rationale.
- System font stack instead of self-hosted Inter (font metrics do not affect
  the renderer comparison).
- The terminal below the dock is an inert stub; terminal integration is out of
  scope for this spike by design.
