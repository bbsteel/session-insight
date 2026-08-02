/**
 * Focused browser harness for the production CollaborationTimeline core.
 *
 * Mounts the real production component (React + production i18n + production
 * app.css) against deterministic contract-shaped datasets and exposes a
 * measurement/assertion API on window.__collab for the benchmark, assertion,
 * and screenshot scripts. Terminal integration is out of scope; the stub
 * below the component only proves the component composes as a flex sibling.
 */

import { createRoot, type Root } from 'react-dom/client'
import { flushSync } from 'react-dom'
import { I18nProvider, saveLocale, type Locale } from '../../../src/i18n.js'
import CollaborationTimeline from '../../../src/components/CollaborationTimeline.js'
import '../../../src/app.css'
import {
  DATASET_SPECS,
  datasetHash,
  generateDataset,
  type DatasetSpec,
  type SyntheticDataset,
} from './datasets.js'
import { layoutTimeline } from '../../../src/collaboration/layoutTimeline.js'
import { selectedPathIds } from '../../../src/collaboration/normalizeTimelineModel.js'
import { createElement as h } from 'react'

const ROW_H = 28
const OVERSCAN = 3
const MIN_SEGMENT_PX = 3
const LABEL_W = 220

type Lang = 'en' | 'zh'

interface HarnessParams {
  spec: DatasetSpec
  theme: 'light' | 'dark'
  lang: Lang
}

function parseParams(): HarnessParams {
  const q = new URLSearchParams(location.search)
  return {
    spec: DATASET_SPECS[q.get('dataset') ?? ''] ?? DATASET_SPECS.typical,
    theme: q.get('theme') === 'light' ? 'light' : 'dark',
    lang: q.get('lang') === 'zh' ? 'zh' : 'en',
  }
}

const params = parseParams()

const state = {
  dataset: generateDataset(params.spec) as SyntheticDataset,
  seedOffset: 0,
  root: null as Root | null,
  nowBumpMs: 0,
  renderCount: 0,
  calls: {
    select: [] as (string | null)[],
    openChild: [] as string[],
    jumpLaunch: [] as (string | null)[],
    jumpResult: [] as string[],
  },
}

const mountEl = document.getElementById('mount') as HTMLDivElement
const termStubEl = document.getElementById('term-stub') as HTMLDivElement

const afterFrame = (): Promise<void> => new Promise((r) => requestAnimationFrame(() => r()))

const observer = new MutationObserver(() => {
  state.renderCount += 1
})

function componentHeight(): number {
  return Math.max(160, Math.min(Math.round(window.innerHeight * 0.4), 420))
}

function render(): void {
  if (!state.root) state.root = createRoot(mountEl)
  state.root.render(
    h(
      I18nProvider,
      null,
      h(CollaborationTimeline, {
        graph: state.dataset.graph,
        nowMs: state.dataset.nowMs + state.nowBumpMs,
        heightPx: componentHeight(),
        liveIntervalMs: 0,
        onSelect: (id: string | null) => state.calls.select.push(id),
        onOpenChildContent: (id: string) => state.calls.openChild.push(id),
        onJumpToLaunch: (id: string) => state.calls.jumpLaunch.push(id),
        onJumpToResult: (id: string) => state.calls.jumpResult.push(id),
      }),
    ),
  )
}

function boot(): void {
  saveLocale(params.lang === 'zh' ? 'zh-CN' : 'en')
  document.documentElement.lang = params.lang === 'zh' ? 'zh-CN' : 'en'
  document.documentElement.classList.toggle('dark', params.theme === 'dark')
  flushSync(() => render())
  const svg = mountEl.querySelector('svg')
  if (svg) observer.observe(mountEl, { subtree: true, childList: true, attributes: true })
  termStubEl.textContent =
    params.lang === 'zh'
      ? '$ session-insight replay —— xterm 占位（不属于本组件）\n终端保持在下方挂载，只有容器高度会变化。'
      : '$ session-insight replay — xterm stub (not part of the component)\nThe terminal stays mounted below; only its container height changes.'
}

function scrollEl(): HTMLDivElement {
  return mountEl.querySelector('.ct-scroll') as HTMLDivElement
}

function svgEl(): SVGSVGElement {
  return mountEl.querySelector('svg.ct-svg') as unknown as SVGSVGElement
}

function currentStats() {
  const scroll = scrollEl()
  return layoutTimeline(state.dataset.model, new Set(), {
    widthPx: Math.max(1, scroll.clientWidth - LABEL_W),
    viewportHeightPx: scroll.clientHeight,
    rowHeightPx: ROW_H,
    scrollTopPx: scroll.scrollTop,
    overscanRows: OVERSCAN,
    domainStartMs: state.dataset.model.domainStartMs,
    domainEndMs: state.dataset.model.domainEndMs,
    nowMs: state.dataset.nowMs,
    minSegmentPx: MIN_SEGMENT_PX,
    selectedPathIds: new Set<string>(),
    hoverId: null,
  }).stats
}

function hitRectForRow(rowIndex: number): Element | null {
  return mountEl.querySelector(`.ct-hit-region[data-invocation][y="${rowIndex * ROW_H}"]`)
}

const collab = {
  ready: false,
  datasetName: params.spec.name,
  rowHeight: ROW_H,
  overscan: OVERSCAN,
  hash: () => datasetHash(state.dataset),
  renderCount: () => state.renderCount,
  calls: state.calls,

  counts() {
    const svg = svgEl()
    return {
      mounted: svg ? svg.querySelectorAll('*').length : 0,
      labelRows: mountEl.querySelectorAll('.ct-label').length,
      stats: currentStats(),
    }
  },

  visibleRowRange() {
    const stats = currentStats()
    return { first: stats.firstVisibleRow, last: stats.lastVisibleRow, total: stats.totalRows }
  },

  measureLayout(runs: number): number[] {
    const times: number[] = []
    const paramsForLayout = {
      widthPx: 1100,
      viewportHeightPx: 380,
      rowHeightPx: ROW_H,
      scrollTopPx: 0,
      overscanRows: OVERSCAN,
      domainStartMs: state.dataset.model.domainStartMs,
      domainEndMs: state.dataset.model.domainEndMs,
      nowMs: state.dataset.nowMs,
      minSegmentPx: MIN_SEGMENT_PX,
      selectedPathIds: new Set<string>(),
      hoverId: null,
    }
    for (let i = 0; i < runs; i++) {
      const t0 = performance.now()
      layoutTimeline(state.dataset.model, new Set(), paramsForLayout)
      times.push(performance.now() - t0)
    }
    return times
  },

  async measureFirstRender(runs: number): Promise<{ mount: number[]; visible: number[] }> {
    const mount: number[] = []
    const visible: number[] = []
    for (let i = 0; i < runs; i++) {
      state.root?.unmount()
      state.root = null
      mountEl.textContent = ''
      const t0 = performance.now()
      flushSync(() => render())
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
    const scroll = scrollEl()
    const max = scroll.scrollHeight - scroll.clientHeight
    const t0 = performance.now()
    scroll.scrollTop = Math.max(0, Math.min(max, scroll.scrollTop + deltaPx))
    scroll.dispatchEvent(new Event('scroll'))
    const t1 = performance.now()
    await afterFrame()
    const t2 = performance.now()
    return { sync: t1 - t0, frame: t2 - t0 }
  },

  async panStep(dxPx: number): Promise<{ sync: number; frame: number }> {
    const svg = svgEl()
    const rect = svg.getBoundingClientRect()
    const x = rect.left + rect.width / 2
    const y = rect.top + 40
    const t0 = performance.now()
    svg.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, button: 0, pointerId: 7, clientX: x, clientY: y }))
    svg.dispatchEvent(new PointerEvent('pointermove', { bubbles: true, pointerId: 7, clientX: x + dxPx, clientY: y }))
    svg.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, pointerId: 7, clientX: x + dxPx, clientY: y }))
    const t1 = performance.now()
    await afterFrame()
    const t2 = performance.now()
    return { sync: t1 - t0, frame: t2 - t0 }
  },

  async zoomStep(factor: number): Promise<{ sync: number; frame: number }> {
    const graph = mountEl.querySelector('.ct-graph') as HTMLDivElement
    const rect = graph.getBoundingClientRect()
    const t0 = performance.now()
    graph.dispatchEvent(
      new WheelEvent('wheel', { bubbles: true, cancelable: true, ctrlKey: true, deltaY: factor > 1 ? 100 : -100, clientX: rect.left + rect.width / 2, clientY: rect.top + 60 }),
    )
    const t1 = performance.now()
    await afterFrame()
    const t2 = performance.now()
    return { sync: t1 - t0, frame: t2 - t0 }
  },

  async hoverRow(rowIndex: number): Promise<{ sync: number; frame: number; id: string | null }> {
    const hit = hitRectForRow(rowIndex)
    if (!hit) return { sync: 0, frame: 0, id: null }
    const svgRect = svgEl().getBoundingClientRect()
    const t0 = performance.now()
    hit.dispatchEvent(
      new PointerEvent('pointermove', { bubbles: true, clientX: svgRect.left + svgRect.width / 2, clientY: svgRect.top + rowIndex * ROW_H + ROW_H / 2 - scrollEl().scrollTop }),
    )
    const t1 = performance.now()
    await afterFrame()
    const t2 = performance.now()
    return { sync: t1 - t0, frame: t2 - t0, id: hit.getAttribute('data-invocation') }
  },

  async selectRow(rowIndex: number): Promise<{ sync: number; frame: number; id: string | null }> {
    const hit = hitRectForRow(rowIndex)
    if (!hit) return { sync: 0, frame: 0, id: null }
    const svgRect = svgEl().getBoundingClientRect()
    const x = svgRect.left + svgRect.width / 2
    const y = svgRect.top + rowIndex * ROW_H + ROW_H / 2 - scrollEl().scrollTop
    const t0 = performance.now()
    hit.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, button: 0, pointerId: 9, clientX: x, clientY: y }))
    hit.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, pointerId: 9, clientX: x, clientY: y }))
    const t1 = performance.now()
    await afterFrame()
    const t2 = performance.now()
    return { sync: t1 - t0, frame: t2 - t0, id: hit.getAttribute('data-invocation') }
  },

  /** One live-geometry refresh (nowMs advance through props, cadence path). */
  async liveUpdateStep(): Promise<{ sync: number; frame: number }> {
    state.nowBumpMs += 1000
    const t0 = performance.now()
    flushSync(() => render())
    const t1 = performance.now()
    await afterFrame()
    const t2 = performance.now()
    return { sync: t1 - t0, frame: t2 - t0 }
  },

  switchDataset(): void {
    state.seedOffset += 1
    state.dataset = generateDataset(params.spec, state.seedOffset)
    state.nowBumpMs = 0
    flushSync(() => render())
  },

  gcHeap(): number | null {
    const w = window as unknown as { gc?: () => void }
    if (w.gc) w.gc()
    const mem = (performance as unknown as { memory?: { usedJSHeapSize: number } }).memory
    return mem ? mem.usedJSHeapSize : null
  },

  laneClientPoint(rowIndex: number): { x: number; y: number } {
    const svgRect = svgEl().getBoundingClientRect()
    return {
      x: svgRect.left + svgRect.width / 2,
      y: svgRect.top + rowIndex * ROW_H + ROW_H / 2 - scrollEl().scrollTop,
    }
  },

  scrollToRow(rowIndex: number): void {
    const scroll = scrollEl()
    scroll.scrollTop = Math.max(0, rowIndex * ROW_H - scroll.clientHeight / 2)
    scroll.dispatchEvent(new Event('scroll'))
  },

  collapseFirstBranch(): string | null {
    const toggle = mountEl.querySelector('[data-toggle]') as HTMLElement | null
    if (!toggle) return null
    toggle.click()
    return toggle.getAttribute('data-toggle')
  },

  selectedPathSize(id: string): number {
    return selectedPathIds(state.dataset.model, id).size
  },

  setTheme(theme: 'light' | 'dark'): void {
    params.theme = theme
    document.documentElement.classList.toggle('dark', theme === 'dark')
  },

  setLang(lang: Lang): void {
    params.lang = lang
    saveLocale(lang === 'zh' ? 'zh-CN' : ('en' as Locale))
    document.documentElement.lang = lang === 'zh' ? 'zh-CN' : 'en'
    state.root?.unmount()
    state.root = null
    mountEl.textContent = ''
    boot()
  },
}

boot()
collab.ready = true
;(window as unknown as { __collab: typeof collab }).__collab = collab
