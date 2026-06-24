import { useEffect, useRef } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { fetchRenderANSI } from '../api'
import type { ScrollMetrics } from '../minimapGeometry'
import { createFrameBatcher } from '../scrollSync'
import { TERMINAL_LINE_HEIGHT, type TerminalControl } from '../terminalControl'

const TERMINAL_FONT_FAMILY = '"JetBrains Mono", "Menlo", monospace'
const TERMINAL_FONT_SIZE = 13

interface Props {
  sessionId: string
  onScrollMetrics?: (m: ScrollMetrics) => void
  controlRef?: React.MutableRefObject<TerminalControl | null>
}

async function waitForTerminalFont() {
  if (!document.fonts?.load) return

  // xterm measures cell width at open time. Force the webfont request before
  // opening so the first session does not measure a fallback font.
  await document.fonts.load(`400 ${TERMINAL_FONT_SIZE}px ${TERMINAL_FONT_FAMILY}`)
  await document.fonts.ready
}

export default function TerminalPanel({ sessionId, onScrollMetrics, controlRef }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const onScrollMetricsRef = useRef(onScrollMetrics)
  onScrollMetricsRef.current = onScrollMetrics

  useEffect(() => {
    const container = containerRef.current
    if (!container) return

    const term = new Terminal({
      theme: {
        background: '#1a1b26',
        foreground: '#e2e2e2',
        cursor: '#e2e2e2',
        selectionBackground: 'rgba(192,202,245,0.3)',
      },
      fontFamily: TERMINAL_FONT_FAMILY,
      fontSize: TERMINAL_FONT_SIZE,
      scrollback: 20000,
      convertEol: true,
      disableStdin: true,
      screenReaderMode: false,
    })
    const fitAddon = new FitAddon()
    term.loadAddon(fitAddon)

    let disposeOnScroll: { dispose(): void } | null = null
    let observer: ResizeObserver | null = null
    let metricsBatcher: ReturnType<typeof createFrameBatcher<ScrollMetrics>> | null = null
    let disposed = false

    waitForTerminalFont().then(() => {
      if (disposed) return

      term.open(container)
      fitAddon.fit()
      const termCols = term.cols

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

      if (controlRef) {
        controlRef.current = {
          scrollToLine: (line) => term.scrollToLine(line),
          getMetrics,
        }
      }

      disposeOnScroll = term.onScroll(queueMetrics)

      observer = new ResizeObserver(() => {
        fitAddon.fit()
        queueMetrics()
      })
      observer.observe(container)

      fetchRenderANSI(sessionId, termCols)
        .then(ansi => {
          if (disposed) return
          term.write(ansi, () => {
            queueMetrics()
          })
        })
        .catch(err => { term.write(`\x1b[31mError loading render: ${err.message}\x1b[0m`) })
    })

    return () => {
      disposed = true
      observer?.disconnect()
      disposeOnScroll?.dispose()
      metricsBatcher?.cancel()
      if (controlRef) controlRef.current = null
      term.dispose()
    }
  }, [sessionId])

  return (
    <div style={{ flex: 1, overflow: 'hidden', background: '#1a1b26', display: 'flex', flexDirection: 'column' }}>
      <div ref={containerRef} style={{ flex: 1, overflow: 'hidden' }} />
    </div>
  )
}
