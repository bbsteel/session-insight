import { useEffect, useRef } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { fetchRenderANSI } from '../api'
import type { ScrollMetrics } from '../minimapGeometry'
import { createFrameBatcher } from '../scrollSync'
import { TERMINAL_LINE_HEIGHT, type TerminalControl, type TerminalLineMatcher } from '../terminalControl'
import { terminalTheme, useIsDark } from '../terminalTheme'

const TERMINAL_FONT_FAMILY = '"JetBrains Mono", "Menlo", monospace'
const TERMINAL_FONT_SIZE = 13

interface Props {
  sessionId: string
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

export default function TerminalPanel({ sessionId, onScrollMetrics, onColsReady, controlRef }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const onScrollMetricsRef = useRef(onScrollMetrics)
  onScrollMetricsRef.current = onScrollMetrics
  const onColsReadyRef = useRef(onColsReady)
  onColsReadyRef.current = onColsReady
  const isDark = useIsDark()
  const isDarkRef = useRef(isDark)
  isDarkRef.current = isDark

  useEffect(() => {
    const container = containerRef.current
    if (!container) return

    const term = new Terminal({
      theme: terminalTheme(isDarkRef.current),
      fontFamily: TERMINAL_FONT_FAMILY,
      fontSize: TERMINAL_FONT_SIZE,
      scrollback: 20000,
      convertEol: true,
      disableStdin: true,
      screenReaderMode: false,
    })
    const fitAddon = new FitAddon()
    term.loadAddon(fitAddon)
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
    let tooltipEl: HTMLDivElement | null = null
    let hoverLine: HTMLDivElement | null = null

    let onMouseMove: ((e: MouseEvent) => void) | null = null
    let onMouseLeave: (() => void) | null = null
    let onClick: ((e: MouseEvent) => void) | null = null

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

      hoverLine = document.createElement('div')
      hoverLine.style.cssText = [
        'position:absolute', 'left:0', 'right:0', 'pointer-events:none',
        'border-bottom:1.5px solid rgba(124,58,237,0.65)',
        'background:rgba(124,58,237,0.07)',
        'display:none', 'z-index:100',
        'height:' + Math.round(cellHeight) + 'px',
      ].join(';')
      container.appendChild(hoverLine)

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

      const getBufLine = (e: MouseEvent): number => {
        const rect = container.getBoundingClientRect()
        const row = Math.floor((e.clientY - rect.top) / cellHeight)
        return row + term.buffer.active.viewportY
      }

      const hideHover = () => {
        if (hoverLine) hoverLine.style.display = 'none'
        if (tooltipEl) tooltipEl.style.display = 'none'
        if (xtermScreen) xtermScreen.style.cursor = ''
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
              console.log(`[scan] bufLine=${i} viewportY=${buf.viewportY} text="${text.slice(0, 60)}"`)
              break
            }
          }
        }
      }

      onMouseMove = (e: MouseEvent) => {
        if (!interactionMap.size) { hideHover(); return }
        const bl = getBufLine(e)
        const entry = interactionMap.get(bl)
        if (entry) {
          const row = bl - term.buffer.active.viewportY
          if (hoverLine) {
            hoverLine.style.top = (row * cellHeight) + 'px'
            hoverLine.style.display = 'block'
          }
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
        const entry = interactionMap.get(bl)
        if (entry) {
          e.preventDefault()
          e.stopPropagation()
          entry.matcher.onActivate(bl, entry.data, entry.matchIndex)
        }
      }

      container.addEventListener('mousemove', onMouseMove)
      container.addEventListener('mouseleave', onMouseLeave)
      container.addEventListener('click', onClick)

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
            term.reset()
            term.write('\x1b[3J') // clear accumulated scrollback so buffer lines start at 0
            term.write(ansi, () => {
              scanBuffer()
              queueMetrics()
            })
          })
          .catch(err => { term.write(`\x1b[31mError loading render: ${err.message}\x1b[0m`) })
      }

      if (controlRef) {
        controlRef.current = {
          scrollToLine: (line) => term.scrollToLine(line),
          getMetrics,
          setLineMatchers: (matchers) => {
            lineMatchers = matchers
            if (term.buffer.active.length > 1) scanBuffer()
          },
        }
      }

      disposeOnScroll = term.onScroll(() => {
        if (hoverLine) hoverLine.style.display = 'none'
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
    })

    return () => {
      disposed = true
      if (resizeDebounce) clearTimeout(resizeDebounce)
      observer?.disconnect()
      disposeOnScroll?.dispose()
      metricsBatcher?.cancel()
      if (onMouseMove) container.removeEventListener('mousemove', onMouseMove)
      if (onMouseLeave) container.removeEventListener('mouseleave', onMouseLeave)
      if (onClick) container.removeEventListener('click', onClick)
      hoverLine?.remove()
      tooltipEl?.remove()
      if (controlRef) controlRef.current = null
      termRef.current = null
      term.dispose()
    }
  }, [sessionId])

  useEffect(() => {
    if (termRef.current) {
      termRef.current.options.theme = terminalTheme(isDark)
    }
  }, [isDark])

  return (
    <div style={{ flex: 1, overflow: 'hidden', background: terminalTheme(isDark).background, display: 'flex', flexDirection: 'column' }}>
      <div ref={containerRef} style={{ flex: 1, overflow: 'hidden' }} />
    </div>
  )
}
