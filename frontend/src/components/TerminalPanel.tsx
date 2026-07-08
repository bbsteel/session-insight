import { useEffect, useRef } from 'react'
import { Terminal, type IDecoration, type IMarker } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { SearchAddon } from '@xterm/addon-search'
import { WebglAddon } from '@xterm/addon-webgl'
import { fetchRenderANSI } from '../api'
import { getBufferLineFromPointer, getBufferLineFromXtermCoords, getMarkerOffsetForBufferLine } from '../terminalInteractionGeometry'
import type { ScrollMetrics } from '../minimapGeometry'
import { createFrameBatcher } from '../scrollSync'
import { TERMINAL_LINE_HEIGHT, type TerminalContextMenuEvent, type TerminalControl, type TerminalLineMatcher } from '../terminalControl'
import { composeFoldView, type FoldRange, type FoldView } from '../terminalFolds'
import { onBannerColorChange, terminalTheme, useIsDark } from '../terminalTheme'

const TERMINAL_FONT_FAMILY = '"JetBrains Mono", "Menlo", monospace'
const TERMINAL_FONT_SIZE = 13
// Text the backend renders for a chrys turn still in progress; the frontend
// finds this row and overlays a spinning hourglass. Keep in sync with the
// formatter's "in_progress" case.
const PROGRESS_ROW_TEXT = '推理中…'

interface Props {
  sessionId: string
  agentType?: string
  folds?: FoldRange[]
  onFoldChange?: () => void
  onContextMenu?: (e: TerminalContextMenuEvent) => void
  onScrollMetrics?: (m: ScrollMetrics) => void
  onColsReady?: (cols: number) => void
  controlRef?: React.MutableRefObject<TerminalControl | null>
}

async function waitForTerminalFont() {
  if (!document.fonts?.load) return
  await document.fonts.load(`400 ${TERMINAL_FONT_SIZE}px ${TERMINAL_FONT_FAMILY}`)
  await document.fonts.ready
}

type InteractionEntry = { matcher: TerminalLineMatcher<unknown>; data: unknown; matchIndex: number }
type XtermCoreWithMouse = {
  screenElement?: HTMLElement
  _mouseService?: {
    getCoords?: (
      event: MouseEvent,
      element: HTMLElement,
      colCount: number,
      rowCount: number,
      isSelection?: boolean,
    ) => [number, number] | undefined
  }
}

export default function TerminalPanel({ sessionId, agentType, folds, onFoldChange, onContextMenu, onScrollMetrics, onColsReady, controlRef }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const onScrollMetricsRef = useRef(onScrollMetrics)
  onScrollMetricsRef.current = onScrollMetrics
  const onColsReadyRef = useRef(onColsReady)
  onColsReadyRef.current = onColsReady
  const isDark = useIsDark()
  const isDarkRef = useRef(isDark)
  isDarkRef.current = isDark
  const agentTypeRef = useRef(agentType)
  agentTypeRef.current = agentType
  const foldsRef = useRef<FoldRange[]>(folds ?? [])
  foldsRef.current = folds ?? []
  const onFoldChangeRef = useRef(onFoldChange)
  onFoldChangeRef.current = onFoldChange
  const onContextMenuRef = useRef(onContextMenu)
  onContextMenuRef.current = onContextMenu
  // Assigned inside the mount effect once the terminal is live; the folds
  // prop effect below routes updated fold ranges into that closure.
  const applyFoldsRef = useRef<((folds: FoldRange[]) => void) | null>(null)

  useEffect(() => {
    const container = containerRef.current
    if (!container) return

    const term = new Terminal({
      theme: terminalTheme(isDarkRef.current, agentTypeRef.current),
      fontFamily: TERMINAL_FONT_FAMILY,
      fontSize: TERMINAL_FONT_SIZE,
      allowProposedApi: true,
      scrollback: 20000,
      convertEol: true,
      disableStdin: true,
      screenReaderMode: false,
      drawBoldTextInBrightColors: false,
    })
    const fitAddon = new FitAddon()
    term.loadAddon(fitAddon)
    const searchAddon = new SearchAddon()
    term.loadAddon(searchAddon)
    termRef.current = term

    let disposeOnScroll: { dispose(): void } | null = null
    let observer: ResizeObserver | null = null
    let metricsBatcher: ReturnType<typeof createFrameBatcher<ScrollMetrics>> | null = null
    let disposed = false
    let currentCols = 0
    let resizeDebounce: ReturnType<typeof setTimeout> | null = null

    // Line interaction state
    let lineMatchers: TerminalLineMatcher<unknown>[] = []
    const interactionMap = new Map<number, InteractionEntry>()
    // Per-row async validation results ('pending' while in flight); cleared
    // on every buffer rescan since row numbers shift across rewrites.
    const rowValidity = new Map<number, 'pending' | boolean>()

    // Fold state: raw ANSI is kept so collapsed tool-group bodies can be
    // recomposed out of the buffer (xterm has no hide-rows primitive; a fold
    // toggle is a full reset+rewrite, same as the initial load path).
    let rawAnsi = ''
    let foldRanges: FoldRange[] = foldsRef.current
    const collapsedKeys = new Set<string>()
    let foldView: FoldView | null = null
    const toDisplayLine = (n: number) => (foldView ? foldView.toDisplay(n) : n)
    const toOriginalLine = (n: number) => (foldView ? foldView.toOriginal(n) : n)
    let tooltipEl: HTMLDivElement | null = null
    let hoverDecoration: IDecoration | null = null
    let hoverMarker: IMarker | null = null
    let activeHoverLine: number | null = null

    // Jump-flash state (hoisted so the effect cleanup can dispose it)
    let flashMarkers: IMarker[] = []
    let flashDecorations: IDecoration[] = []
    let flashTimer: ReturnType<typeof setTimeout> | null = null
    const clearFlash = () => {
      if (flashTimer) { clearTimeout(flashTimer); flashTimer = null }
      flashDecorations.forEach(d => d.dispose())
      flashMarkers.forEach(m => m.dispose())
      flashDecorations = []
      flashMarkers = []
    }

    // Spinning hourglass over a "turn in progress" row (chrys in-flight
    // checkpoint). One decoration, re-applied after every rewrite so it tracks
    // the row as the buffer changes; disposed when the marker text is gone.
    let progressDecoration: IDecoration | null = null
    let progressMarker: IMarker | null = null
    const clearProgress = () => {
      progressDecoration?.dispose()
      progressMarker?.dispose()
      progressDecoration = null
      progressMarker = null
    }

    let onMouseMove: ((e: MouseEvent) => void) | null = null
    let onMouseLeave: (() => void) | null = null
    let onClick: ((e: MouseEvent) => void) | null = null
    let onCtxMenu: ((e: MouseEvent) => void) | null = null

    // Anti-flicker for fold rewrites: reset+write repaints over several frames,
    // so a static snapshot of the current screen covers the terminal until the
    // rewrite (and its anchor scroll) has painted.
    let removeSnapshot: (() => void) | null = null
    let hasWrittenOnce = false
    let disposeSearchResultsRef: { dispose(): void } | null = null

    waitForTerminalFont().then(() => {
      if (disposed) return

      term.open(container)

      // WebGL renderer: rasterizes every glyph into a fixed grid cell, so CJK
      // (wide) characters occupy exactly the two cells xterm allocates them.
      // The default DOM renderer instead lays glyphs out as text and leans on
      // the font for cell width — but our terminal font (JetBrains Mono) has no
      // CJK glyphs, so Chinese falls back to a system font whose advance width
      // isn't exactly double, drifting box-drawing borders out of alignment on
      // rows with Chinese. Must load after open(). On context loss dispose the
      // addon so xterm transparently reverts to the DOM renderer; wrap in
      // try/catch for environments without WebGL support.
      try {
        const webgl = new WebglAddon()
        webgl.onContextLoss(() => webgl.dispose())
        term.loadAddon(webgl)
      } catch {
        // WebGL unavailable → keep the DOM renderer (still functional).
      }

      fitAddon.fit()
      currentCols = term.cols

      const xtermCanvas = container.querySelector<HTMLCanvasElement>('.xterm-screen canvas')
      const dpr = window.devicePixelRatio || 1
      const cellHeight = xtermCanvas && term.rows > 0
        ? xtermCanvas.height / dpr / term.rows
        : TERMINAL_LINE_HEIGHT

      container.style.position = 'relative'

      tooltipEl = document.createElement('div')
      tooltipEl.style.cssText = [
        'position:fixed', 'padding:3px 10px',
        'background:rgba(30,30,46,0.96)', 'color:#cdd6f4',
        'border:1px solid rgba(124,58,237,0.55)', 'border-radius:4px',
        'font-size:12px', 'font-family:system-ui,-apple-system,sans-serif',
        'pointer-events:none', 'z-index:9999', 'display:none',
        'white-space:nowrap', 'box-shadow:0 2px 10px rgba(0,0,0,0.45)',
      ].join(';')
      document.body.appendChild(tooltipEl)

      const xtermScreen = container.querySelector<HTMLElement>('.xterm-screen')
      const eventTarget = xtermScreen ?? container

      const getScreenRect = () => (xtermScreen ?? container).getBoundingClientRect()

      const getBufLine = (e: MouseEvent): number | null => {
        // Keep hit-testing in xterm's coordinate system. Hand-rolled
        // rect/cellHeight math drifts by 1-2 rows because xterm accounts for
        // screen padding, renderService cell dimensions, ceil/clamp behavior,
        // and viewport state internally.
        const core = (term as unknown as { _core?: XtermCoreWithMouse })._core
        const screenElement = core?.screenElement ?? xtermScreen ?? container
        const coords = core?._mouseService?.getCoords?.(e, screenElement, term.cols, term.rows, false)
        const xtermLine = getBufferLineFromXtermCoords(coords, term.buffer.active.viewportY)
        if (xtermLine !== null) return xtermLine

        const screenRect = screenElement.getBoundingClientRect?.() ?? getScreenRect()
        return getBufferLineFromPointer({
          clientY: e.clientY,
          screenTop: screenRect.top,
          cellHeight,
          viewportY: term.buffer.active.viewportY,
          rowCount: term.rows,
        })
      }

      const clearHoverDecoration = () => {
        hoverDecoration?.dispose()
        hoverMarker?.dispose()
        hoverDecoration = null
        hoverMarker = null
        activeHoverLine = null
      }

      const flashLines = (startLine: number, count = 2) => {
        clearFlash()
        for (let i = 0; i < count; i++) {
          const offset = getMarkerOffsetForBufferLine({
            bufferLine: startLine + i,
            baseY: term.buffer.active.baseY,
            cursorY: term.buffer.active.cursorY,
          })
          const marker = term.registerMarker(offset)
          if (!marker) continue
          let decoration: IDecoration | undefined
          try {
            decoration = term.registerDecoration({ marker, width: term.cols, height: 1, layer: 'top' })
          } catch {
            marker.dispose()
            continue
          }
          if (!decoration) { marker.dispose(); continue }
          decoration.onRender(element => {
            element.style.pointerEvents = 'none'
            element.style.left = '0'
            element.style.width = '100%'
            element.style.boxSizing = 'border-box'
            element.style.background = 'rgba(37, 99, 235, 0.30)'
            element.style.transition = 'background 1.2s ease-out 0.2s'
            requestAnimationFrame(() => { element.style.background = 'rgba(37, 99, 235, 0)' })
          })
          flashMarkers.push(marker)
          flashDecorations.push(decoration)
        }
        flashTimer = setTimeout(clearFlash, 1600)
      }

      const hideHover = () => {
        clearHoverDecoration()
        if (tooltipEl) tooltipEl.style.display = 'none'
        if (xtermScreen) xtermScreen.style.cursor = ''
      }

      const showHoverDecoration = (bufLine: number) => {
        if (activeHoverLine === bufLine && hoverDecoration && !hoverDecoration.isDisposed) return
        clearHoverDecoration()

        const offset = getMarkerOffsetForBufferLine({
          bufferLine: bufLine,
          baseY: term.buffer.active.baseY,
          cursorY: term.buffer.active.cursorY,
        })
        const marker = term.registerMarker(offset)
        if (!marker) return

        let decoration: IDecoration | undefined
        try {
          decoration = term.registerDecoration({
            marker,
            width: term.cols,
            height: 1,
            layer: 'top',
          })
        } catch {
          marker.dispose()
          return
        }
        if (!decoration) {
          marker.dispose()
          return
        }

        decoration.onRender(element => {
          element.style.pointerEvents = 'none'
          element.style.left = '0'
          element.style.right = '0'
          element.style.width = '100%'
          element.style.boxSizing = 'border-box'
          element.style.borderBottom = '1.5px solid rgba(124,58,237,0.65)'
          element.style.background = 'rgba(124,58,237,0.07)'
        })

        hoverMarker = marker
        hoverDecoration = decoration
        activeHoverLine = bufLine
      }

      // Scan the xterm.js buffer for all registered matchers and populate interactionMap.
      // Called after every render so buffer lines are the source of truth (no Go lineStart dependency).
      const scanBuffer = () => {
        interactionMap.clear()
        rowValidity.clear()
        if (lineMatchers.length === 0) return
        const buf = term.buffer.active
        const matchCounts = new Map<TerminalLineMatcher<unknown>, number>()
        for (let i = 0; i < buf.length; i++) {
          const line = buf.getLine(i)
          if (!line) continue
          const text = line.translateToString(true)
          for (const matcher of lineMatchers) {
            const data = matcher.match(text)
            if (data !== null) {
              const idx = matchCounts.get(matcher) ?? 0
              matchCounts.set(matcher, idx + 1)
              interactionMap.set(i, { matcher, data, matchIndex: idx })
              break
            }
          }
        }
      }

      // Register collapsed/expanded tool-group headers as clickable rows.
      // Injected after every scanBuffer so a rewrite can't leave stale rows.
      const foldToggleMatcher: TerminalLineMatcher<FoldRange> = {
        match: () => null, // rows come from fold geometry, not text scanning
        tooltip: '收起/展开 Tools',
        onActivate: (_bufLine, fold) => toggleFold(fold),
      }
      const injectFoldRows = () => {
        if (!foldRanges.length) return
        for (const f of foldRanges) {
          const row = toDisplayLine(f.headerDisplay)
          interactionMap.set(row, { matcher: foldToggleMatcher as TerminalLineMatcher<unknown>, data: f, matchIndex: 0 })
        }
      }

      // Overlay a spinning hourglass on chrys's "推理中…" in-progress row. The
      // marker rides xterm's own viewport math (AGENTS.md: no hand-rolled DOM
      // row coordinates); the row sits near the tail, so scan from the bottom.
      const injectProgressRow = () => {
        clearProgress()
        const buf = term.buffer.active
        for (let i = buf.length - 1; i >= 0; i--) {
          const line = buf.getLine(i)
          if (!line) continue
          if (!line.translateToString(true).includes(PROGRESS_ROW_TEXT)) continue
          const offset = getMarkerOffsetForBufferLine({ bufferLine: i, baseY: buf.baseY, cursorY: buf.cursorY })
          const marker = term.registerMarker(offset)
          if (!marker) break
          let decoration: IDecoration | undefined
          try {
            decoration = term.registerDecoration({ marker, x: 0, width: 2, height: 1, layer: 'top' })
          } catch {
            marker.dispose()
            break
          }
          if (!decoration) { marker.dispose(); break }
          decoration.onRender(element => {
            if (element.dataset.siProgress === '1') return // onRender fires per xterm paint
            element.dataset.siProgress = '1'
            element.style.pointerEvents = 'none'
            element.style.display = 'flex'
            element.style.alignItems = 'center'
            element.style.justifyContent = 'center'
            element.textContent = '⏳'
            element.animate(
              [{ transform: 'rotate(0deg)' }, { transform: 'rotate(360deg)' }],
              { duration: 1600, iterations: Infinity, easing: 'linear' },
            )
          })
          progressMarker = marker
          progressDecoration = decoration
          break
        }
      }

      const snapshotTerminal = () => {
        removeSnapshot?.()
        removeSnapshot = null
        const screen = container.querySelector<HTMLElement>('.xterm')
        if (!screen) return
        let snap: HTMLElement
        try {
          snap = screen.cloneNode(true) as HTMLElement
          // Canvas pixels (canvas/webgl renderers) don't survive cloneNode;
          // copy them explicitly. No-op under the DOM renderer.
          const srcCanvases = screen.querySelectorAll('canvas')
          const dstCanvases = snap.querySelectorAll('canvas')
          srcCanvases.forEach((src, i) => {
            const dst = dstCanvases[i]
            if (!dst) return
            dst.width = src.width
            dst.height = src.height
            dst.getContext('2d')?.drawImage(src, 0, 0)
          })
        } catch {
          return
        }
        const wrapper = document.createElement('div')
        const bg = term.options.theme?.background ?? '#1a1b26'
        wrapper.style.cssText = `position:absolute;inset:0;overflow:hidden;z-index:5;pointer-events:none;background:${bg}`
        wrapper.appendChild(snap)
        container.appendChild(wrapper)
        removeSnapshot = () => {
          wrapper.remove()
          removeSnapshot = null
        }
      }

      const writeComposed = (afterWrite?: () => void) => {
        if (hasWrittenOnce) snapshotTerminal()
        clearHoverDecoration()
        term.reset()
        term.write('\x1b[3J') // clear accumulated scrollback so buffer lines start at 0
        term.write(foldView?.text ?? rawAnsi, () => {
          hasWrittenOnce = true
          scanBuffer()
          injectFoldRows()
          injectProgressRow()
          queueMetrics()
          afterWrite?.()
          // Two frames: one for xterm's render, one to be past the paint.
          requestAnimationFrame(() => requestAnimationFrame(() => removeSnapshot?.()))
        })
      }

      const recompose = (afterWrite?: () => void) => {
        foldView = composeFoldView(rawAnsi, foldRanges, collapsedKeys)
        writeComposed(() => {
          afterWrite?.()
          onFoldChangeRef.current?.()
        })
      }

      const toggleFold = (fold: FoldRange) => {
        if (!rawAnsi) return
        // Keep the toggled header at the same on-screen row across the rewrite.
        const beforeRow = toDisplayLine(fold.headerDisplay)
        const viewportOffset = beforeRow - term.buffer.active.viewportY
        if (collapsedKeys.has(fold.key)) collapsedKeys.delete(fold.key)
        else collapsedKeys.add(fold.key)
        recompose(() => {
          const afterRow = toDisplayLine(fold.headerDisplay)
          term.scrollToLine(Math.max(0, afterRow - viewportOffset))
        })
      }

      const setFoldsCollapsed = (keys: string[], collapsed: boolean, anchorOriginalRow?: number | null) => {
        if (!rawAnsi || !foldRanges.length) return
        const valid = new Set(foldRanges.map(f => f.key))
        let changed = false
        for (const k of keys) {
          if (!valid.has(k)) continue
          if (collapsed && !collapsedKeys.has(k)) { collapsedKeys.add(k); changed = true }
          if (!collapsed && collapsedKeys.has(k)) { collapsedKeys.delete(k); changed = true }
        }
        if (!changed) return
        const anchor = anchorOriginalRow ?? toOriginalLine(term.buffer.active.viewportY)
        const viewportOffset = toDisplayLine(anchor) - term.buffer.active.viewportY
        recompose(() => {
          term.scrollToLine(Math.max(0, toDisplayLine(anchor) - viewportOffset))
        })
      }

      applyFoldsRef.current = (next: FoldRange[]) => {
        foldRanges = next
        // Drop collapsed state for folds that no longer exist (session grew).
        const valid = new Set(next.map(f => f.key))
        let dropped = false
        for (const k of [...collapsedKeys]) {
          if (!valid.has(k)) { collapsedKeys.delete(k); dropped = true }
        }
        if (collapsedKeys.size > 0 || dropped || foldView) recompose()
        else { injectFoldRows(); injectProgressRow() }
      }

      const showHoverFor = (bl: number, entry: InteractionEntry, clientX: number, clientY: number) => {
        showHoverDecoration(bl)
        if (tooltipEl) {
          const tip = entry.matcher.tooltip ?? ''
          tooltipEl.textContent = tip
          tooltipEl.style.display = tip ? 'block' : 'none'
          tooltipEl.style.left = (clientX + 14) + 'px'
          tooltipEl.style.top = (clientY - 38) + 'px'
        }
        if (xtermScreen) xtermScreen.style.cursor = 'pointer'
      }

      let lastHover: { bl: number; clientX: number; clientY: number } | null = null

      onMouseMove = (e: MouseEvent) => {
        if (!interactionMap.size) { hideHover(); return }
        const bl = getBufLine(e)
        if (bl === null) { hideHover(); lastHover = null; return }
        const entry = interactionMap.get(bl)
        lastHover = { bl, clientX: e.clientX, clientY: e.clientY }
        if (!entry) { hideHover(); return }

        // Rows with a validator only get the affordance once it confirms
        // (e.g. the detected path really exists). The mouse may sit still
        // while the check resolves, so completion re-shows from lastHover.
        if (entry.matcher.validate) {
          const state = rowValidity.get(bl)
          if (state === undefined) {
            rowValidity.set(bl, 'pending')
            hideHover()
            const text = term.buffer.active.getLine(bl)?.translateToString(true) ?? ''
            entry.matcher.validate(text).then(ok => {
              if (disposed || rowValidity.get(bl) !== 'pending') return
              rowValidity.set(bl, ok)
              if (!ok) {
                interactionMap.delete(bl)
                return
              }
              if (lastHover?.bl === bl) showHoverFor(bl, entry, lastHover.clientX, lastHover.clientY)
            })
            return
          }
          if (state !== true) { hideHover(); return }
        }
        showHoverFor(bl, entry, e.clientX, e.clientY)
      }

      onMouseLeave = () => hideHover()

      onClick = (e: MouseEvent) => {
        const bl = getBufLine(e)
        if (bl === null) return
        const entry = interactionMap.get(bl)
        if (entry) {
          // Validated matchers only activate after their check confirmed.
          if (entry.matcher.validate && rowValidity.get(bl) !== true) return
          e.preventDefault()
          e.stopPropagation()
          const core = (term as unknown as { _core?: XtermCoreWithMouse })._core
          const screenElement = core?.screenElement ?? xtermScreen ?? container
          const coords = core?._mouseService?.getCoords?.(e, screenElement, term.cols, term.rows, false)
          entry.matcher.onActivate(bl, entry.data, entry.matchIndex, {
            clientX: e.clientX,
            clientY: e.clientY,
            column: coords ? coords[0] - 1 : null,
            lineText: term.buffer.active.getLine(bl)?.translateToString(true) ?? '',
          })
        }
      }

      onCtxMenu = (e: MouseEvent) => {
        e.preventDefault()
        const bl = getBufLine(e)
        // Column comes from the same xterm MouseService call as the row
        // (coords are 1-based); needed to pick the path token under the cursor.
        const core = (term as unknown as { _core?: XtermCoreWithMouse })._core
        const screenElement = core?.screenElement ?? xtermScreen ?? container
        const coords = core?._mouseService?.getCoords?.(e, screenElement, term.cols, term.rows, false)
        onContextMenuRef.current?.({
          clientX: e.clientX,
          clientY: e.clientY,
          originalRow: bl !== null ? toOriginalLine(bl) : null,
          column: coords ? coords[0] - 1 : null,
          lineText: bl !== null ? (term.buffer.active.getLine(bl)?.translateToString(true) ?? '') : '',
          collapsedFoldKeys: [...collapsedKeys],
        })
      }

      eventTarget.addEventListener('mousemove', onMouseMove)
      eventTarget.addEventListener('mouseleave', onMouseLeave)
      eventTarget.addEventListener('click', onClick)
      // The custom menu replaces the browser one across the whole terminal
      // area (container, not just the text screen).
      container.addEventListener('contextmenu', onCtxMenu)

      const getMetrics = (): ScrollMetrics => ({
        scrollTop: term.buffer.active.viewportY * TERMINAL_LINE_HEIGHT,
        scrollHeight: term.buffer.active.length * TERMINAL_LINE_HEIGHT,
        clientHeight: term.rows * TERMINAL_LINE_HEIGHT,
      })
      let lastMetrics: ScrollMetrics | undefined
      metricsBatcher = createFrameBatcher(metrics => {
        if (
          lastMetrics
          && lastMetrics.scrollTop === metrics.scrollTop
          && lastMetrics.scrollHeight === metrics.scrollHeight
          && lastMetrics.clientHeight === metrics.clientHeight
        ) {
          return
        }
        lastMetrics = metrics
        onScrollMetricsRef.current?.(metrics)
      })
      const queueMetrics = () => metricsBatcher?.push(getMetrics())

      const loadRender = (cols: number) => {
        fetchRenderANSI(sessionId, cols)
          .then(ansi => {
            if (disposed) return
            rawAnsi = ansi
            foldView = collapsedKeys.size > 0 ? composeFoldView(rawAnsi, foldRanges, collapsedKeys) : null
            writeComposed(() => { if (foldView) onFoldChangeRef.current?.() })
          })
          .catch(err => { term.write(`\x1b[31mError loading render: ${err.message}\x1b[0m`) })
      }

      // Search: decorations are always enabled (they drive the n/m counter);
      // visual styling lives in app.css on the addon's decoration classes
      // (the DOM renderer ignores decoration background options). "Highlight
      // all" off = container class that blanks the all-match layer via CSS.
      const buildSearchOptions = (o: { caseSensitive: boolean; wholeWord: boolean; regex: boolean; highlightAll: boolean }) => {
        container.classList.toggle('si-search-active-only', !o.highlightAll)
        return {
          caseSensitive: o.caseSensitive,
          wholeWord: o.wholeWord,
          regex: o.regex,
          decorations: {
            matchOverviewRuler: o.highlightAll ? '#facc15' : '#00000000',
            // The inline outline this sets is the only DOM marker of the
            // active match in addon-search 0.16 (see app.css); the visible
            // outline itself is suppressed there in favor of a background.
            activeMatchBorder: '#f59e0b',
            activeMatchColorOverviewRuler: '#f59e0b',
          },
        }
      }
      // addon-search 0.16 overwrites lastSearchOptions before diffing them,
      // so an option change alone never refreshes the all-match highlight;
      // clearing the cached term forces the next find to re-highlight.
      let lastSearchOptsKey = ''
      const invalidateSearchOnOptionChange = (o: { caseSensitive: boolean; wholeWord: boolean; regex: boolean; highlightAll: boolean }) => {
        const key = `${o.caseSensitive}|${o.wholeWord}|${o.regex}|${o.highlightAll}`
        if (key !== lastSearchOptsKey) {
          lastSearchOptsKey = key
          searchAddon.clearDecorations()
        }
      }
      let searchResultsCb: ((index: number, count: number) => void) | null = null
      const disposeSearchResults = searchAddon.onDidChangeResults(r => {
        searchResultsCb?.(r.resultIndex, r.resultCount)
      })
      disposeSearchResultsRef = disposeSearchResults

      if (controlRef) {
        controlRef.current = {
          scrollToLine: (line) => term.scrollToLine(line),
          getMetrics,
          setLineMatchers: (matchers) => {
            lineMatchers = matchers
            if (term.buffer.active.length > 1) {
              scanBuffer()
              injectFoldRows()
              injectProgressRow()
            }
          },
          flashLines,
          toDisplayLine,
          toOriginalLine,
          hiddenLineCount: () => foldView?.hiddenTotal ?? 0,
          setFoldsCollapsed,
          getCollapsedFoldKeys: () => [...collapsedKeys],
          searchNext: (query, opts) => {
            invalidateSearchOnOptionChange(opts)
            try {
              return searchAddon.findNext(query, buildSearchOptions(opts))
            } catch { return false } // invalid regex mid-typing
          },
          searchPrev: (query, opts) => {
            invalidateSearchOnOptionChange(opts)
            try {
              return searchAddon.findPrevious(query, buildSearchOptions(opts))
            } catch { return false }
          },
          searchClear: () => {
            searchAddon.clearDecorations()
            term.clearSelection() // the active match is selection-backed
          },
          refreshContent: async () => {
            if (!hasWrittenOnce || currentCols === 0) return 'unchanged'
            const ansi = await fetchRenderANSI(sessionId, currentCols)
            if (disposed || ansi === rawAnsi) return 'unchanged'
            const buf = term.buffer.active
            const atBottom = buf.viewportY >= buf.baseY

            // Pure append: rendering is deterministic, so as long as nothing
            // structural changed upstream (an in-progress group header's n/m
            // counter, a collapsed fold), the old ANSI is a strict prefix and
            // the delta streams straight into the buffer.
            if (collapsedKeys.size === 0 && !foldView && ansi.startsWith(rawAnsi)) {
              const suffix = ansi.slice(rawAnsi.length)
              rawAnsi = ansi
              await new Promise<void>(resolve => term.write(suffix, () => {
                scanBuffer()
                injectFoldRows()
                injectProgressRow()
                queueMetrics()
                resolve()
              }))
              if (atBottom) term.scrollToBottom()
              return 'appended'
            }

            const keepRow = buf.viewportY
            rawAnsi = ansi
            foldView = collapsedKeys.size > 0 ? composeFoldView(rawAnsi, foldRanges, collapsedKeys) : null
            await new Promise<void>(resolve => writeComposed(() => {
              if (atBottom) term.scrollToBottom()
              else term.scrollToLine(keepRow)
              resolve()
            }))
            if (foldView) onFoldChangeRef.current?.()
            return 'rewritten'
          },
          setSearchResultsListener: (cb) => { searchResultsCb = cb },
        }
      }

      disposeOnScroll = term.onScroll(() => {
        clearHoverDecoration()
        if (tooltipEl) tooltipEl.style.display = 'none'
        if (xtermScreen) xtermScreen.style.cursor = ''
        queueMetrics()
      })

      observer = new ResizeObserver(() => {
        fitAddon.fit()
        queueMetrics()
        const newCols = term.cols
        if (newCols !== currentCols) {
          currentCols = newCols
          if (resizeDebounce) clearTimeout(resizeDebounce)
          resizeDebounce = setTimeout(() => {
            if (disposed) return
            loadRender(newCols)
            onColsReadyRef.current?.(newCols)
          }, 500)
        }
      })
      observer.observe(container)

      loadRender(currentCols)
      onColsReadyRef.current?.(currentCols)
      if (foldsRef.current.length) applyFoldsRef.current?.(foldsRef.current)
    })

    return () => {
      disposed = true
      if (resizeDebounce) clearTimeout(resizeDebounce)
      observer?.disconnect()
      disposeOnScroll?.dispose()
      metricsBatcher?.cancel()
      const eventTarget = container.querySelector<HTMLElement>('.xterm-screen') ?? container
      if (onMouseMove) eventTarget.removeEventListener('mousemove', onMouseMove)
      if (onMouseLeave) eventTarget.removeEventListener('mouseleave', onMouseLeave)
      if (onClick) eventTarget.removeEventListener('click', onClick)
      if (onCtxMenu) container.removeEventListener('contextmenu', onCtxMenu)
      disposeSearchResultsRef?.dispose()
      removeSnapshot?.()
      hoverDecoration?.dispose()
      hoverMarker?.dispose()
      clearFlash()
      clearProgress()
      tooltipEl?.remove()
      if (controlRef) controlRef.current = null
      applyFoldsRef.current = null
      termRef.current = null
      term.dispose()
    }
  }, [sessionId])

  useEffect(() => {
    if (termRef.current) {
      termRef.current.options.theme = terminalTheme(isDark, agentType)
    }
  }, [isDark, agentType])

  useEffect(() => {
    return onBannerColorChange(() => {
      if (termRef.current) {
        termRef.current.options.theme = terminalTheme(isDark, agentType)
      }
    })
  }, [isDark, agentType])

  useEffect(() => {
    applyFoldsRef.current?.(folds ?? [])
  }, [folds])

  return (
    <div style={{ flex: 1, overflow: 'hidden', background: terminalTheme(isDark).background, display: 'flex', flexDirection: 'column' }}>
      <div ref={containerRef} style={{ flex: 1, overflow: 'hidden' }} />
    </div>
  )
}
