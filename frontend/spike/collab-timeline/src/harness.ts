/**
 * Self-contained spike harness page. Drives both renderer candidates with the
 * same data, layout engine, DOM label column, tooltip, and keyboard model so
 * the benchmark compares renderers under equivalent visual content.
 *
 * The page intentionally uses vanilla DOM instead of React: the spike isolates
 * the SVG-vs-Canvas decision. Production controls/labels will be React, which
 * adds the same constant to both candidates and does not change the comparison.
 */

import type { TimelineCandidate } from './candidate'
import { DATASET_SPECS, datasetHash, generateDataset, type DatasetSpec } from './generator'
import { layoutTimeline, selectedPath, type LayoutParams, type RenderPrimitives } from './layout'
import { createCanvasCandidate } from './render-canvas'
import { createSvgCandidate } from './render-svg'
import type { SpikeDataset } from './types'

const ROW_H = 28
const OVERSCAN = 3
const MIN_SEGMENT_PX = 3

const STR = {
  en: {
    children: 'children',
    active: 'active',
    problems: 'problems',
    expandTimeline: 'Expand timeline',
    collapseTimeline: 'Collapse timeline',
    duration: 'Duration',
    precision: 'Precision',
    status: 'Status',
    started: 'Started',
    terminalStub:
      '$ session-insight replay — xterm stub (not part of this spike)\n' +
      'The terminal stays mounted below the dock; only its container height changes.\n\n' +
      'turn 7  · tool: read_file  · ok\nturn 8  · assistant reply …',
    statuses: {
      pending: 'pending', running: 'running', waiting: 'waiting', completed: 'completed',
      failed: 'failed', cancelled: 'cancelled', orphaned: 'orphaned', unknown: 'unknown',
    } as Record<string, string>,
  },
  zh: {
    children: '个子代理',
    active: '个活跃',
    problems: '个异常',
    expandTimeline: '展开时间线',
    collapseTimeline: '折叠时间线',
    duration: '时长',
    precision: '精度',
    status: '状态',
    started: '开始于',
    terminalStub:
      '$ session-insight replay —— xterm 占位（不属于本 spike）\n' +
      '终端保持在 dock 下方挂载，只有容器高度会变化。\n\n' +
      'turn 7  · tool: read_file  · ok\nturn 8  · assistant reply …',
    statuses: {
      pending: '等待中', running: '运行中', waiting: '等待结果', completed: '已完成',
      failed: '失败', cancelled: '已取消', orphaned: '孤儿', unknown: '未知',
    } as Record<string, string>,
  },
}

type Lang = keyof typeof STR

interface HarnessParams {
  renderer: 'svg' | 'canvas'
  spec: DatasetSpec
  theme: 'light' | 'dark'
  lang: Lang
  dock: 'expanded' | 'collapsed'
}

function parseParams(): HarnessParams {
  const q = new URLSearchParams(location.search)
  const renderer = q.get('renderer') === 'canvas' ? 'canvas' : 'svg'
  const spec = DATASET_SPECS[q.get('dataset') ?? ''] ?? DATASET_SPECS.typical
  const theme = q.get('theme') === 'light' ? 'light' : 'dark'
  const lang: Lang = q.get('lang') === 'zh' ? 'zh' : 'en'
  const dock = q.get('dock') === 'collapsed' ? 'collapsed' : 'expanded'
  return { renderer, spec, theme, lang, dock }
}

interface HarnessState {
  params: HarnessParams
  dataset: SpikeDataset
  seedOffset: number
  collapsedIds: Set<string>
  selectedId: string | null
  hoverId: string | null
  domainStartMs: number
  domainEndMs: number
  candidate: TimelineCandidate
  lastPrims: RenderPrimitives | null
  lastAppliedScroll: number
  updateScheduled: boolean
  /** Roving tabindex anchor for keyboard navigation. */
  activeRowIndex: number
  /** Monotonic render counter, exposed for repaint assertions. */
  renderCount: number
}

const afterFrame = (): Promise<void> => new Promise((r) => requestAnimationFrame(() => r()))

const scrollEl = document.getElementById('scroll') as HTMLDivElement
const gridEl = document.getElementById('grid') as HTMLDivElement
const labelsEl = document.getElementById('labels') as HTMLDivElement
const graphEl = document.getElementById('graph') as HTMLDivElement
const tooltipEl = document.getElementById('tooltip') as HTMLDivElement
const summaryEl = document.getElementById('dock-summary') as HTMLDivElement
const dockEl = document.getElementById('dock') as HTMLDivElement
const termStubEl = document.getElementById('term-stub') as HTMLDivElement

function createCandidate(name: 'svg' | 'canvas'): TimelineCandidate {
  return name === 'svg' ? createSvgCandidate(ROW_H) : createCanvasCandidate(ROW_H)
}

const params = parseParams()
const state: HarnessState = {
  params,
  dataset: generateDataset(params.spec),
  seedOffset: 0,
  collapsedIds: new Set(),
  selectedId: null,
  hoverId: null,
  domainStartMs: 0,
  domainEndMs: 0,
  candidate: createCandidate(params.renderer),
  lastPrims: null,
  lastAppliedScroll: -1,
  updateScheduled: false,
  activeRowIndex: 0,
  renderCount: 0,
}
state.domainStartMs = state.dataset.domainStartMs
state.domainEndMs = state.dataset.domainEndMs

document.documentElement.dataset.theme = params.theme
document.documentElement.lang = params.lang === 'zh' ? 'zh-CN' : 'en'

function t() {
  return STR[state.params.lang]
}

function layoutParams(): LayoutParams {
  return {
    widthPx: Math.max(1, graphEl.clientWidth),
    viewportHeightPx: scrollEl.clientHeight,
    rowHeightPx: ROW_H,
    scrollTopPx: scrollEl.scrollTop,
    overscanRows: OVERSCAN,
    domainStartMs: state.domainStartMs,
    domainEndMs: state.domainEndMs,
    nowMs: state.dataset.nowMs,
    minSegmentPx: MIN_SEGMENT_PX,
    selectedPathIds: selectedPath(state.dataset, state.selectedId),
    hoverId: state.hoverId,
  }
}

const STATUS_GLYPH: Record<string, string> = {
  pending: '○', running: '●', waiting: '◐', completed: '✓',
  failed: '✕', cancelled: '⊘', orphaned: '◆', unknown: '?',
}

function renderLabels(prims: RenderPrimitives): void {
  // Preserve keyboard focus across re-renders: the visible window is rebuilt
  // on every scroll frame, so the focused row must be re-focused by id.
  const focusedInv = labelsEl.contains(document.activeElement)
    ? (document.activeElement as HTMLElement).dataset.invocation
    : null
  labelsEl.textContent = ''
  const frag = document.createDocumentFragment()
  for (const lane of prims.lanes) {
    const row = document.createElement('div')
    row.className = 'tl-label'
    row.style.top = `${lane.y}px`
    row.style.height = `${ROW_H}px`
    row.style.paddingLeft = `${8 + lane.depth * 14}px`
    row.setAttribute('role', 'treeitem')
    row.setAttribute('aria-level', String(lane.depth + 1))
    if (lane.hasChildren) row.setAttribute('aria-expanded', lane.collapsed ? 'false' : 'true')
    row.setAttribute('aria-selected', state.selectedId === lane.invocationId ? 'true' : 'false')
    row.setAttribute('tabindex', lane.rowIndex === state.activeRowIndex ? '0' : '-1')
    row.dataset.invocation = lane.invocationId
    row.dataset.status = lane.status
    row.dataset.row = String(lane.rowIndex)
    const statusText = t().statuses[lane.status] ?? lane.status
    row.setAttribute('aria-label', `${lane.label}, ${statusText}`)

    const glyph = document.createElement('span')
    glyph.className = `tl-status st-${lane.status}`
    glyph.textContent = STATUS_GLYPH[lane.status] ?? '?'
    glyph.setAttribute('aria-hidden', 'true')
    row.appendChild(glyph)

    const text = document.createElement('span')
    text.className = 'tl-label-text'
    text.textContent = lane.label
    row.appendChild(text)

    if (lane.hasChildren) {
      // Non-interactive affordance: branch collapse is driven by the row's
      // click target and ArrowLeft/ArrowRight, so no focusable control is
      // nested inside the treeitem.
      const toggle = document.createElement('span')
      toggle.className = 'tl-collapse'
      toggle.textContent = lane.collapsed ? '▸' : '▾'
      toggle.setAttribute('aria-hidden', 'true')
      toggle.dataset.toggle = lane.invocationId
      row.appendChild(toggle)
    }
    frag.appendChild(row)
  }
  labelsEl.appendChild(frag)
  if (focusedInv) {
    const el = labelsEl.querySelector(`[data-invocation="${CSS.escape(focusedInv)}"]`) as HTMLElement | null
    el?.focus()
  }
}

function renderSummary(): void {
  const invs = state.dataset.invocations
  const children = invs.length - 1
  const active = invs.filter((i) => i.status === 'running' || i.status === 'waiting' || i.status === 'pending').length
  const problems = invs.filter((i) => i.status === 'failed' || i.status === 'orphaned').length
  summaryEl.textContent = ''
  const dot = document.createElement('span')
  dot.className = `tl-status ${active > 0 ? 'st-running' : problems > 0 ? 'st-failed' : 'st-completed'}`
  dot.textContent = active > 0 ? '●' : problems > 0 ? '✕' : '✓'
  summaryEl.appendChild(dot)
  const text = document.createElement('span')
  text.textContent = ` ⑂ ${children} ${t().children} · ${active} ${t().active} · ${problems} ${t().problems}`
  summaryEl.appendChild(text)
}

let applying = false
function applyViewport(): void {
  if (applying) return // re-entrant call from focus/scroll events during re-render
  applying = true
  try {
    state.renderCount += 1
    state.lastAppliedScroll = scrollEl.scrollTop
    const prims = layoutTimeline(state.dataset, state.collapsedIds, layoutParams())
    state.lastPrims = prims
    gridEl.style.height = `${prims.totalHeightPx}px`
    state.candidate.update(prims, {
      selectedId: state.selectedId,
      selectedPathIds: selectedPath(state.dataset, state.selectedId),
      hoverId: state.hoverId,
      scrollTopPx: scrollEl.scrollTop,
      viewportHeightPx: scrollEl.clientHeight,
      rowHeightPx: ROW_H,
      theme: state.params.theme,
    })
    renderLabels(prims)
  } finally {
    applying = false
  }
}

function scheduleUpdate(): void {
  if (state.updateScheduled) return
  state.updateScheduled = true
  requestAnimationFrame(() => {
    state.updateScheduled = false
    // Pan and zoom mutate the time domain without touching scrollTop, so the
    // frame must always re-render, not only on scroll changes.
    applyViewport()
  })
}

function setHover(id: string | null, clientX?: number, clientY?: number): void {
  state.hoverId = id
  applyViewport()
  if (id && clientX !== undefined && clientY !== undefined) {
    showTooltip(id, clientX, clientY)
  } else {
    tooltipEl.hidden = true
  }
}

function showTooltip(id: string, clientX: number, clientY: number): void {
  const inv = state.dataset.invocations.find((i) => i.id === id)
  if (!inv) return
  const s = t()
  const statusText = s.statuses[inv.status] ?? inv.status
  const start = inv.startedAtMs
  const end = inv.endedAtMs ?? (inv.status === 'running' || inv.status === 'waiting' ? state.dataset.nowMs : null)
  const durSec = start !== null && end !== null ? Math.round((end - start) / 1000) : null
  const lines = [
    inv.label,
    `${s.status}: ${statusText}`,
    `${s.duration}: ${durSec === null ? '—' : durSec >= 120 ? `${Math.round(durSec / 60)} min` : `${durSec} s`}`,
    `${s.precision}: ${inv.timePrecision}`,
  ]
  tooltipEl.textContent = lines.join('\n')
  tooltipEl.hidden = false
  const pad = 12
  const rect = tooltipEl.getBoundingClientRect()
  tooltipEl.style.left = `${Math.min(clientX + pad, window.innerWidth - rect.width - 8)}px`
  tooltipEl.style.top = `${Math.min(clientY + pad, window.innerHeight - rect.height - 8)}px`
}

function setSelected(id: string | null): void {
  state.selectedId = id
  applyViewport()
}

// ---- Event wiring -------------------------------------------------------

scrollEl.addEventListener('scroll', scheduleUpdate, { passive: true })

graphEl.addEventListener('pointermove', (e) => {
  const rect = state.candidate.element.getBoundingClientRect()
  const id = state.candidate.hitTest(e.clientX - rect.left, e.clientY - rect.top)
  if (id !== state.hoverId) setHover(id, e.clientX, e.clientY)
  else if (id) showTooltip(id, e.clientX, e.clientY)
})
graphEl.addEventListener('pointerleave', () => setHover(null))
graphEl.addEventListener('click', (e) => {
  const rect = state.candidate.element.getBoundingClientRect()
  const id = state.candidate.hitTest(e.clientX - rect.left, e.clientY - rect.top)
  setSelected(id === state.selectedId ? null : id)
})

// Horizontal pan by dragging the graph background.
let panState: { pointerId: number; lastX: number } | null = null
graphEl.addEventListener('pointerdown', (e) => {
  if (e.button !== 0) return
  graphEl.setPointerCapture(e.pointerId)
  panState = { pointerId: e.pointerId, lastX: e.clientX }
})
graphEl.addEventListener('pointermove', (e) => {
  if (!panState || panState.pointerId !== e.pointerId) return
  const dx = e.clientX - panState.lastX
  panState.lastX = e.clientX
  const span = state.domainEndMs - state.domainStartMs
  const dxMs = (-dx / Math.max(1, graphEl.clientWidth)) * span
  shiftDomain(dxMs)
  scheduleUpdate()
})
const endPan = (e: PointerEvent) => {
  if (panState?.pointerId !== e.pointerId) return
  if (graphEl.hasPointerCapture(e.pointerId)) graphEl.releasePointerCapture(e.pointerId)
  panState = null
}
graphEl.addEventListener('pointerup', endPan)
graphEl.addEventListener('pointercancel', endPan)

function shiftDomain(dxMs: number): void {
  const span = state.domainEndMs - state.domainStartMs
  let start = state.domainStartMs + dxMs
  const minStart = state.dataset.domainStartMs - span * 0.1
  const maxStart = state.dataset.domainEndMs + span * 0.1 - span
  start = Math.max(minStart, Math.min(maxStart, start))
  state.domainStartMs = start
  state.domainEndMs = start + span
}

function zoomDomain(factor: number, centerMs?: number): void {
  const center = centerMs ?? (state.domainStartMs + state.domainEndMs) / 2
  const span = (state.domainEndMs - state.domainStartMs) * factor
  const clamped = Math.max(60_000, Math.min(state.dataset.domainEndMs - state.dataset.domainStartMs, span))
  const ratio = (center - state.domainStartMs) / (state.domainEndMs - state.domainStartMs)
  state.domainStartMs = center - ratio * clamped
  state.domainEndMs = state.domainStartMs + clamped
  shiftDomain(0)
}

graphEl.addEventListener(
  'wheel',
  (e) => {
    if (!e.ctrlKey && !e.metaKey) return
    e.preventDefault()
    const rect = graphEl.getBoundingClientRect()
    const frac = (e.clientX - rect.left) / Math.max(1, rect.width)
    const center = state.domainStartMs + frac * (state.domainEndMs - state.domainStartMs)
    zoomDomain(e.deltaY > 0 ? 1.25 : 0.8, center)
    scheduleUpdate()
  },
  { passive: false },
)

// Label-column interactions: collapse toggles, selection, keyboard.
labelsEl.addEventListener('click', (e) => {
  const target = e.target as HTMLElement
  const toggle = target.closest('[data-toggle]') as HTMLElement | null
  if (toggle) {
    toggleBranch(toggle.dataset.toggle ?? '')
    return
  }
  const row = target.closest('[data-invocation]') as HTMLElement | null
  if (row) setSelected(row.dataset.invocation ?? null)
})

function focusRow(rowIndex: number): void {
  const total = state.lastPrims?.stats.totalRows ?? 0
  const next = Math.max(0, Math.min(total - 1, rowIndex))
  state.activeRowIndex = next
  const first = state.lastPrims?.stats.firstVisibleRow ?? 0
  const last = state.lastPrims?.stats.lastVisibleRow ?? 0
  if (next <= first) scrollEl.scrollTop = Math.max(0, (next - 1) * ROW_H)
  else if (next >= last) scrollEl.scrollTop = (next + 2) * ROW_H - scrollEl.clientHeight
  applyViewport()
  const el = labelsEl.querySelector(`[data-row="${next}"]`) as HTMLElement | null
  el?.focus()
}

function toggleBranch(id: string): void {
  if (state.collapsedIds.has(id)) state.collapsedIds.delete(id)
  else state.collapsedIds.add(id)
  applyViewport()
}

labelsEl.addEventListener('keydown', (e) => {
  const row = (e.target as HTMLElement).closest('[data-row]') as HTMLElement | null
  if (!row) return
  const rowIndex = Number(row.dataset.row)
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault()
    focusRow(rowIndex + (e.key === 'ArrowDown' ? 1 : -1))
  } else if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
    const lane = state.lastPrims?.lanes.find((l) => l.rowIndex === rowIndex)
    if (!lane) return
    e.preventDefault()
    if (e.key === 'ArrowRight') {
      // Expand a collapsed branch, otherwise move to the first child row.
      if (lane.hasChildren && lane.collapsed) toggleBranch(lane.invocationId)
      else if (lane.hasChildren) focusRow(rowIndex + 1)
    } else {
      // Collapse an expanded branch, otherwise move focus to the parent row.
      if (lane.hasChildren && !lane.collapsed) {
        toggleBranch(lane.invocationId)
      } else {
        const parentId = state.dataset.invocations.find((i) => i.id === lane.invocationId)?.parentId
        const parentLane = state.lastPrims?.lanes.find((l) => l.invocationId === parentId)
        if (parentLane) focusRow(parentLane.rowIndex)
      }
    }
  } else if (e.key === 'Enter' || e.key === ' ') {
    e.preventDefault()
    setSelected(row.dataset.invocation ?? null)
  }
})

labelsEl.addEventListener('focusin', (e) => {
  const row = (e.target as HTMLElement).closest('[data-invocation]') as HTMLElement | null
  if (!row) return
  const id = row.dataset.invocation ?? null
  state.activeRowIndex = Number(row.dataset.row)
  if (state.hoverId !== id) {
    state.hoverId = id
    applyViewport()
  }
  if (!id) return
  const fresh = labelsEl.querySelector(`[data-invocation="${id}"]`) as HTMLElement | null
  const rect = (fresh ?? row).getBoundingClientRect()
  showTooltip(id, rect.right, rect.top)
})
labelsEl.addEventListener('focusout', (e) => {
  // Ignore intra-list moves, and defer: the visible window rebuild removes the
  // focused row and immediately re-focuses its replacement, which would
  // otherwise read as a transient blur and recurse.
  const next = e.relatedTarget as HTMLElement | null
  if (next && labelsEl.contains(next)) return
  requestAnimationFrame(() => {
    if (labelsEl.contains(document.activeElement)) return
    if (state.hoverId !== null) {
      state.hoverId = null
      applyViewport()
    }
    tooltipEl.hidden = true
  })
})

function setDock(mode: 'expanded' | 'collapsed'): void {
  state.params.dock = mode
  dockEl.classList.toggle('collapsed', mode === 'collapsed')
  dockEl.classList.toggle('expanded', mode === 'expanded')
  if (mode === 'expanded') applyViewport()
}

function setTheme(theme: 'light' | 'dark'): void {
  state.params.theme = theme
  document.documentElement.dataset.theme = theme
  applyViewport()
}

function setLang(lang: Lang): void {
  state.params.lang = lang
  document.documentElement.lang = lang === 'zh' ? 'zh-CN' : 'en'
  renderSummary()
  termStubEl.textContent = t().terminalStub
  applyViewport()
}

// ---- Live-update simulation ---------------------------------------------

function liveUpdate(): void {
  const running = state.dataset.invocations.find((i) => i.status === 'running')
  const target = running ?? state.dataset.invocations[1] ?? state.dataset.invocations[0]
  if (!target) return
  const now = state.dataset.nowMs
  target.segments.push({ startMs: now - 30_000, endMs: now })
  if (!running) target.status = 'running'
  applyViewport()
}

// ---- Measurement API (window.__spike) ------------------------------------

function currentUiScroll(): number {
  return scrollEl.scrollTop
}

const spike = {
  ready: false,
  renderer: params.renderer,
  datasetName: params.spec.name,
  hash: () => datasetHash(state.dataset),
  rowHeight: ROW_H,
  overscan: OVERSCAN,
  renderCount: () => state.renderCount,

  counts() {
    return {
      mounted: state.candidate.mountedPrimitiveCount(),
      stats: state.lastPrims?.stats ?? null,
      labelRows: labelsEl.childElementCount,
    }
  },

  visibleRowRange() {
    return {
      first: state.lastPrims?.stats.firstVisibleRow ?? 0,
      last: state.lastPrims?.stats.lastVisibleRow ?? 0,
      total: state.lastPrims?.stats.totalRows ?? 0,
    }
  },

  measureLayout(runs: number): number[] {
    const times: number[] = []
    const p = layoutParams()
    for (let i = 0; i < runs; i++) {
      const t0 = performance.now()
      layoutTimeline(state.dataset, state.collapsedIds, p)
      times.push(performance.now() - t0)
    }
    return times
  },

  async measureFirstRender(runs: number): Promise<{ mount: number[]; visible: number[] }> {
    const mount: number[] = []
    const visible: number[] = []
    for (let i = 0; i < runs; i++) {
      state.candidate.dispose()
      graphEl.textContent = ''
      state.candidate = createCandidate(state.params.renderer)
      graphEl.appendChild(state.candidate.element)
      const t0 = performance.now()
      applyViewport()
      const t1 = performance.now()
      await afterFrame()
      await afterFrame()
      const t2 = performance.now()
      mount.push(t1 - t0)
      visible.push(t2 - t0)
    }
    return { mount, visible }
  },

  async scrollStep(deltaPx: number): Promise<{ sync: number; frame: number }> {
    const max = scrollEl.scrollHeight - scrollEl.clientHeight
    scrollEl.scrollTop = Math.max(0, Math.min(max, scrollEl.scrollTop + deltaPx))
    const t0 = performance.now()
    applyViewport()
    const t1 = performance.now()
    await afterFrame()
    const t2 = performance.now()
    return { sync: t1 - t0, frame: t2 - t0 }
  },

  async panStep(dxMs: number): Promise<{ sync: number; frame: number }> {
    shiftDomain(dxMs)
    const t0 = performance.now()
    applyViewport()
    const t1 = performance.now()
    await afterFrame()
    const t2 = performance.now()
    return { sync: t1 - t0, frame: t2 - t0 }
  },

  async zoomStep(factor: number): Promise<{ sync: number; frame: number }> {
    zoomDomain(factor)
    const t0 = performance.now()
    applyViewport()
    const t1 = performance.now()
    await afterFrame()
    const t2 = performance.now()
    return { sync: t1 - t0, frame: t2 - t0 }
  },

  async hoverRow(rowIndex: number): Promise<{ sync: number; frame: number; id: string | null }> {
    const rect = state.candidate.element.getBoundingClientRect()
    const isSvg = state.params.renderer === 'svg'
    const y = isSvg ? rowIndex * ROW_H + ROW_H / 2 : rowIndex * ROW_H + ROW_H / 2 - currentUiScroll()
    const x = rect.width / 2
    const t0 = performance.now()
    const id = state.candidate.hitTest(x, y)
    setHover(id, rect.left + x, isSvg ? rect.top + y : rect.top + y)
    const t1 = performance.now()
    await afterFrame()
    const t2 = performance.now()
    return { sync: t1 - t0, frame: t2 - t0, id }
  },

  async selectRow(rowIndex: number): Promise<{ sync: number; frame: number; id: string | null }> {
    const rect = state.candidate.element.getBoundingClientRect()
    const isSvg = state.params.renderer === 'svg'
    const y = isSvg ? rowIndex * ROW_H + ROW_H / 2 : rowIndex * ROW_H + ROW_H / 2 - currentUiScroll()
    const x = rect.width / 2
    const t0 = performance.now()
    const id = state.candidate.hitTest(x, y)
    setSelected(id)
    const t1 = performance.now()
    await afterFrame()
    const t2 = performance.now()
    return { sync: t1 - t0, frame: t2 - t0, id }
  },

  async liveUpdateStep(): Promise<{ sync: number; frame: number }> {
    const t0 = performance.now()
    liveUpdate()
    const t1 = performance.now()
    await afterFrame()
    const t2 = performance.now()
    return { sync: t1 - t0, frame: t2 - t0 }
  },

  switchDataset(): void {
    state.seedOffset += 1
    state.dataset = generateDataset(state.params.spec, state.seedOffset)
    state.domainStartMs = state.dataset.domainStartMs
    state.domainEndMs = state.dataset.domainEndMs
    state.selectedId = null
    state.hoverId = null
    state.collapsedIds = new Set()
    scrollEl.scrollTop = 0
    renderSummary()
    applyViewport()
  },

  gcHeap(): number | null {
    const w = window as unknown as { gc?: () => void }
    if (w.gc) w.gc()
    const mem = (performance as unknown as { memory?: { usedJSHeapSize: number } }).memory
    return mem ? mem.usedJSHeapSize : null
  },

  laneClientPoint(rowIndex: number): { x: number; y: number } {
    const rect = state.candidate.element.getBoundingClientRect()
    const isSvg = state.params.renderer === 'svg'
    const y = isSvg
      ? rect.top + rowIndex * ROW_H + ROW_H / 2
      : rect.top + rowIndex * ROW_H + ROW_H / 2 - currentUiScroll()
    return { x: rect.left + rect.width / 2, y }
  },

  selectDeep(): string | null {
    const deep = state.dataset.invocations.reduce((a, b) => (b.depth > a.depth ? b : a))
    setSelected(deep.id)
    return deep.id
  },

  /** Zoom the time domain around a lane's effective window (with padding). */
  zoomToLane(id: string): void {
    const inv = state.dataset.invocations.find((i) => i.id === id)
    if (!inv) return
    const start = inv.startedAtMs ?? inv.segments[0]?.startMs ?? state.dataset.domainStartMs
    const end = inv.endedAtMs ?? inv.segments[inv.segments.length - 1]?.endMs ?? start + 60_000
    const span = Math.max(60_000, end - start)
    state.domainStartMs = start - span * 0.35
    state.domainEndMs = end + span * 0.35
    shiftDomain(0)
    applyViewport()
  },

  collapseFirstBranch(): string | null {
    const counts = new Map<string, number>()
    for (const inv of state.dataset.invocations) {
      if (inv.parentId) counts.set(inv.parentId, (counts.get(inv.parentId) ?? 0) + 1)
    }
    const parent = state.dataset.invocations.find((i) => (counts.get(i.id) ?? 0) >= 2 && i.depth === 0)
    const target = parent ?? state.dataset.invocations.find((i) => (counts.get(i.id) ?? 0) > 0)
    if (!target) return null
    state.collapsedIds.add(target.id)
    applyViewport()
    return target.id
  },

  setTheme,
  setLang,
  setDock,
  zoomDomain: (f: number) => {
    zoomDomain(f)
    applyViewport()
  },
}

// ---- Boot ---------------------------------------------------------------

graphEl.appendChild(state.candidate.element)
renderSummary()
termStubEl.textContent = t().terminalStub
setDock(params.dock)
applyViewport()
spike.ready = true
;(window as unknown as { __spike: typeof spike }).__spike = spike
