import { useEffect, useRef } from 'react'
import { Terminal, type IDecoration, type IMarker } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { SearchAddon } from '@xterm/addon-search'
import { fetchRenderANSI } from '../api'
import { getBufferLineFromPointer, getBufferLineFromXtermCoords, getMarkerOffsetForBufferLine } from '../terminalInteractionGeometry'
import type { ScrollMetrics } from '../minimapGeometry'
import { createFrameBatcher } from '../scrollSync'
import { TERMINAL_LINE_HEIGHT, type TerminalContextMenuEvent, type TerminalControl, type TerminalLineMatcher } from '../terminalControl'
import { composeFoldView, type FoldRange, type FoldView } from '../terminalFolds'
import { onBannerColorChange, terminalTheme, useIsDark } from '../terminalTheme'

const TERMINAL_FONT_FAMILY = '"JetBrains Mono", "Menlo", monospace'
const TERMINAL_FONT_SIZE = 13

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
        else injectFoldRows()
      }

      onMouseMove = (e: MouseEvent) => {
        if (!interactionMap.size) { hideHover(); return }
        const bl = getBufLine(e)
        if (bl === null) { hideHover(); return }
        const entry = interactionMap.get(bl)
        if (entry) {
          showHoverDecoration(bl)
          if (tooltipEl) {
            const tip = entry.matcher.tooltip ?? ''
            tooltipEl.textContent = tip
            tooltipEl.style.display = tip ? 'block' : 'none'
            tooltipEl.style.left = (e.clientX + 14) + 'px'
            tooltipEl.style.top = (e.clientY - 38) + 'px'
          }
          if (xtermScreen) xtermScreen.style.cursor = 'pointer'
        } else {
          hideHover()
        }
      }

      onMouseLeave = () => hideHover()

      onClick = (e: MouseEvent) => {
        const bl = getBufLine(e)
        if (bl === null) return
        const entry = interactionMap.get(bl)
        if (entry) {
          e.preventDefault()
          e.stopPropagation()
          entry.matcher.onActivate(bl, entry.data, entry.matchIndex)
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
      const buildSearchOptions = (o: { caseSensitive: boolean; wholeWord: boolean; highlightAll: boolean }) => {
        container.classList.toggle('si-search-active-only', !o.highlightAll)
        return {
          caseSensitive: o.caseSensitive,
          wholeWord: o.wholeWord,
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
      const invalidateSearchOnOptionChange = (o: { caseSensitive: boolean; wholeWord: boolean; highlightAll: boolean }) => {
        const key = `${o.caseSensitive}|${o.wholeWord}|${o.highlightAll}`
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
            return searchAddon.findNext(query, buildSearchOptions(opts))
          },
          searchPrev: (query, opts) => {
            invalidateSearchOnOptionChange(opts)
            return searchAddon.findPrevious(query, buildSearchOptions(opts))
          },
          searchClear: () => {
            searchAddon.clearDecorations()
            term.clearSelection() // the active match is selection-backed
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
