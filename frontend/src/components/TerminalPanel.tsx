import { useEffect, useRef } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { fetchRenderANSI } from '../api'

interface Props {
  sessionId: string
}

export default function TerminalPanel({ sessionId }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)

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

    const observer = new ResizeObserver(() => fitAddon.fit())
    observer.observe(container)

    fetchRenderANSI(sessionId)
      .then(ansi => { term.write(ansi) })
      .catch(err => { term.write(`\x1b[31mError loading render: ${err.message}\x1b[0m`) })

    return () => {
      observer.disconnect()
      term.dispose()
    }
  }, [sessionId])

  return (
    <div style={{ flex: 1, overflow: 'hidden', background: '#1a1b26', display: 'flex', flexDirection: 'column' }}>
      <div ref={containerRef} style={{ flex: 1, overflow: 'hidden' }} />
    </div>
  )
}
