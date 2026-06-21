import { useEffect, useRef } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { fetchRenderANSI } from '../api'
import type { ScrollMetrics } from '../minimapGeometry'

// Pixel height per terminal line, used to convert xterm's line-based scroll
// position into the pixel-based ScrollMetrics that MiniMap expects.
export const TERMINAL_LINE_HEIGHT = 16

export interface TerminalControl {
  scrollToLine: (line: number) => void
  getMetrics: () => ScrollMetrics
}

interface Props {
  sessionId: string
  onScrollMetrics?: (m: ScrollMetrics) => void
  controlRef?: React.MutableRefObject<TerminalControl | null>
}

export default function TerminalPanel({ sessionId, onScrollMetrics, controlRef }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  // Keep a stable ref to the callback so the xterm onScroll handler never
  // needs to be torn down and recreated when the parent re-renders.
  const onScrollMetricsRef = useRef(onScrollMetrics)
  onScrollMetricsRef.current = onScrollMetrics

  useEffect(() => {
    const container = containerRef.current
    if (!container) return

    const term = new Terminal({
      theme: {
        background: '#1a1b26',
        foreground: '#c0caf5',
        cursor: '#c0caf5',
        selectionBackground: 'rgba(192,202,245,0.3)',
      },
      fontFamily: '"JetBrains Mono", "Menlo", monospace',
      fontSize: 13,
      scrollback: 20000,
      convertEol: true,
      disableStdin: true,
      screenReaderMode: false,
    })
    const fitAddon = new FitAddon()
    term.loadAddon(fitAddon)
    term.open(container)
    fitAddon.fit()
    const termCols = term.cols

    const getMetrics = (): ScrollMetrics => ({
      scrollTop: term.buffer.active.viewportY * TERMINAL_LINE_HEIGHT,
      scrollHeight: term.buffer.active.length * TERMINAL_LINE_HEIGHT,
      clientHeight: term.rows * TERMINAL_LINE_HEIGHT,
    })

    if (controlRef) {
      controlRef.current = {
        scrollToLine: (line) => term.scrollToLine(line),
        getMetrics,
      }
    }

    const disposeOnScroll = term.onScroll(() => {
      onScrollMetricsRef.current?.(getMetrics())
    })

    const observer = new ResizeObserver(() => {
      fitAddon.fit()
      onScrollMetricsRef.current?.(getMetrics())
    })
    observer.observe(container)

    fetchRenderANSI(sessionId, termCols)
      .then(ansi => {
        term.write(ansi, () => {
          onScrollMetricsRef.current?.(getMetrics())
        })
      })
      .catch(err => { term.write(`\x1b[31mError loading render: ${err.message}\x1b[0m`) })

    return () => {
      observer.disconnect()
      disposeOnScroll.dispose()
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
