# Collaboration Timeline Renderer Spike — Benchmark Report

> Phase 0 renderer decision evidence for
> `session-insight-docs/agent-adapters/agent-collaboration-timeline-design.md`
> (§11 renderer decision, §12.4 budgets, §12.5 SVG-to-Canvas gate).
> Raw measurements: `report/results.json` in this directory (same run as every
> table below).
> Reproduction: see `README.md` in this directory.

Date: 2026-07-28 (post-review rerun; first run 2026-07-27)
Branch: `chore/collaboration-renderer-spike`

## Verdict

**SVG passes every §12.4 budget at all three scales and is recommended as the
production renderer**, with the pure `RenderPrimitives` layout boundary
preserved so Canvas remains a drop-in fallback. Canvas also passes every
budget; it is a viable fallback, not the recommendation, because SVG gives
crisp vector output, DOM-native hit regions, and simpler inspection at equal
performance, while Canvas adds a spatial index and redraw-per-frame ownership
for no measured benefit at the design's target scale.

Per §12.5, SVG is kept only because the stress fixture passes; this decision
should be revisited if real data produces materially more than 200 lanes /
2,000 segments per root Session, or if production React integration reveals
costs this vanilla-DOM harness does not measure (see Deviations).

## Environment (reference machine)

| Item | Value |
|---|---|
| Machine | x64, AMD Custom APU 0405 (Steam Deck-class APU) |
| OS | Linux 6.16.12-valve24.4-1-neptune-616-gfe145653a794 x86_64 |
| Browser | Chromium 149.0.7827.55 (headless, Playwright 1.61.1) |
| Node / npm | v22.23.1 / 10.9.8 |
| Viewport | 1440×900, dock height min(40vh, 420px), row height 28px, overscan 3 |
| Flags | `--enable-precise-memory-info --js-flags=--expose-gc` |
| Fixture hashes | typical `fce01b31`, large `bcd53a4e`, stress `a85e16ea` (FNV-1a of canonical dataset JSON; identical for both candidates, stable across all runs) |
| Runs | layout 25 (5 warm-up), first render 12 (2 warm-up), scroll 120 steps, pan 60, zoom 30, hover 40, select 30, live update 50, 20 dataset switches |
| Repetitions | full benchmark executed three times on the same machine (twice before review fixes, once after); layout medians agreed within 0.15 ms across runs, frame medians within 3 ms |

## Results (median / p95, ms)

| Candidate | Dataset | Layout | First visible | Scroll frame | Pan frame | Hover | Select | Live update | Mounted | Heap first→last |
|---|---|---|---|---|---|---|---|---|---|---|
| svg | typical | 0.20/0.50 | 33.10/33.20 | 8.70/9.90 | 11.80/12.60 | 13.30/15.50 | 11.10/12.10 | 11.50/12.70 | 264 | 1.7→1.6 MB |
| canvas | typical | 0.15/0.30 | 33.25/33.40 | 11.40/12.40 | 13.60/13.90 | 14.30/14.70 | 12.90/13.70 | 13.40/14.00 | 247 | 1.6→1.6 MB |
| svg | large | 0.30/0.50 | 33.15/33.30 | 7.60/9.40 | 11.05/12.10 | 13.75/14.30 | 12.00/12.80 | 12.30/13.00 | 299 | 1.7→1.8 MB |
| canvas | large | 0.20/0.30 | 33.20/33.40 | 10.70/11.40 | 13.80/14.20 | 14.20/14.80 | 13.50/14.10 | 13.65/14.20 | 282 | 1.7→1.7 MB |
| svg | stress | 0.35/1.10 | 32.85/33.20 | 4.50/8.40 | 10.55/12.60 | 13.00/15.60 | 10.40/12.60 | 10.60/12.40 | 608 | 1.9→1.8 MB |
| canvas | stress | 0.25/0.70 | 33.25/33.40 | 9.80/10.40 | 13.70/14.20 | 14.40/14.90 | 13.80/14.30 | 13.80/14.30 | 591 | 1.8→1.7 MB |

"Mounted" is reported honestly per technology: mounted DOM nodes for SVG
(including per-row transparent hit rects — the 17-node difference vs Canvas
equals the visible row count) versus real draw calls per frame for Canvas
(row feedback, edges, intervals, markers; Canvas hit regions feed only its
spatial index and are never drawn).

Synchronous mount (layout + DOM/canvas update, no paint) median/p95, from the
same committed run:

| Candidate | typical | large | stress |
|---|---|---|---|
| svg | 2.65/3.20 | 2.80/3.50 | 4.15/6.40 |
| canvas | 2.30/2.70 | 2.65/3.20 | 3.00/3.90 |

Reading the numbers:

- "First visible" is measured data-ready → after two `requestAnimationFrame`
  callbacks, so ~33 ms is the headless 60 Hz double-frame floor plus ≤ 5 ms of
  real work; it is identical across scales because mounted primitives are
  viewport-bounded.
- Frame metrics (scroll/pan/zoom/hover/select/live) measure event → post-rAF
  and sit near the same floor; SVG and Canvas trade 1–3 ms within run-to-run
  noise, with no consistent winner at any scale.
- Zoom medians/p95s match pan within noise and are omitted from the table for
  readability; see `results.json`.
- Memory is flat across 20 dataset switches for both candidates (±0.1 MB, no
  growth trend).

## Budget evaluation (§12.4)

| Gate | Result |
|---|---|
| Typical: layout + first render < 100 ms | PASS both (≤ 33.45 ms median, pairwise-summed samples) |
| Typical: hover/select < 50 ms | PASS both (≤ 14.3 ms median) |
| Stress: pure layout < 50 ms | PASS both (≤ 0.35 ms median) |
| Stress: visible result < 250 ms | PASS both (≤ 33.25 ms median) |
| Stress: most direct-manipulation frames < 20 ms | PASS both (100% of scroll and pan frames) |
| Mounted primitives bounded by viewport + LOD | PASS both (≤ 608 mounted at 200 lanes/2,000 segments; 484 of 500 edges culled at overview) |

## Equivalence and LOD evidence

- Both candidates consume identical `RenderPrimitives` and one CSS
  custom-property token set (the Canvas palette is read from the same
  variables via `getComputedStyle`, so the renderers cannot drift).
- LOD merging reduces visible-lane intervals (stress: 830 input segments in
  visible lanes → 541 mounted intervals); result edges render only on the
  selected/hovered path (484/500 edges culled at overview zoom).
- Pixel review of the screenshot matrix confirms visually identical output
  between candidates, including the selected causal path, running/failure
  markers, light/dark, en/zh, and 1280×720 / 1440×900.

## Accessibility and interaction

- Keyboard lives on the shared DOM label column (`role="tree"`/`treeitem`
  with `aria-expanded`) in both candidates: roving `tabindex`, ArrowUp/Down
  lane movement with viewport follow, ArrowRight/Left expand/collapse branches
  (no focusable control nested inside treeitems), Enter selects, tooltip opens
  on hover **and** focus. Canvas adoption therefore does not reduce keyboard
  access (structural assertions cover this).
- Status is never color-only: shape markers per state (✕ failed, ◆ orphaned,
  open caps for missing completion, dashed circle for missing start, filled
  dot for running/waiting) plus localized status text in labels/tooltip.
- `prefers-reduced-motion: reduce` disables the running pulse (asserted via
  computed style).
- Fixed 28 px row height holds for Chinese labels and long English labels
  (ellipsis), asserted structurally.
- Regression assertions pin the review fixes: drag-pan and ctrl+wheel zoom
  must trigger a repaint (render counter increases).

## Deviations from the design budget / scope

1. **Vanilla DOM harness instead of React.** The spike isolates the
   SVG-vs-Canvas boundary; React adds the same constant to both candidates.
   Consequence: production integration must re-verify that React reconciliation
   of the label column stays off the terminal scroll path (§12.6), and the
   §12.4 "no application-wide React render during pan" target was not
   exercised here.
2. **No ECharts custom-series candidate.** Canvas already measures the same
   rasterization technology; ECharts would add chart-abstraction overhead
   (axis/series model fighting hierarchical lanes, per design §11.1 risk) with
   no expected throughput gain. Consequence: if production later needs ECharts
   for consistency with Analytics, this spike provides no measurement of that
   path.
3. **Terminal integration not implemented** (out of scope by the spike brief);
   the harness renders an inert terminal stub as a flex sibling below the dock.
4. **System font stack** instead of self-hosted Inter/JetBrains Mono; font
   metrics do not affect the renderer comparison.
5. **Frame timings are floored by the 60 Hz rAF cadence** in headless
   Chromium; sub-frame costs are reported separately via the synchronous mount
   table above.

## Proposed production boundaries (supported by the spike)

- Keep the pure layout engine contract exactly as spiked: viewport dimensions,
  row height, visible row range, time domain, zoom, normalized invocations and
  relations in → immutable primitives out. No React/DOM/theme imports.
- One `<svg>` spanning the virtual scroll height with viewport-culled children
  is sufficient; no windowed SVG re-parenting needed at 200 lanes.
- LOD thresholds from the spike hold: merge segments below 3 px, result edges
  only on selected/hovered path, row-level hit regions with 28 px effective
  height.
- Budget headroom at stress scale is large (layout 0.35 ms vs 50 ms), so the
  layout engine may additionally support a full-domain overview pass (e.g. a
  mini overview strip) without endangering the gates.
