import { useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

interface InstantTooltipProps {
  /** Empty/undefined text disables the tooltip and renders children as-is. */
  text?: string
  children: ReactNode
  className?: string
  placement?: 'top' | 'bottom' | 'cursor'
  /** Max width for long notes. */
  maxWidth?: number
}

/**
 * Custom hover tooltip with no browser delay (native `title` waits ~1s).
 * Use for bookmark notes and other hover copy that must appear immediately.
 */
export default function InstantTooltip({
  text,
  children,
  className,
  placement = 'top',
  maxWidth = 280,
}: InstantTooltipProps) {
  const [pos, setPos] = useState<{ x: number; y: number } | null>(null)
  const wrapRef = useRef<HTMLSpanElement>(null)
  const tip = text?.trim() ?? ''

  if (!tip) {
    return <span className={className}>{children}</span>
  }

  const showAt = (clientX: number, clientY: number) => {
    if (placement === 'cursor') {
      setPos({ x: clientX, y: clientY })
      return
    }
    const r = wrapRef.current?.getBoundingClientRect()
    if (!r) {
      setPos({ x: clientX, y: clientY })
      return
    }
    setPos({
      x: r.left + r.width / 2,
      y: placement === 'bottom' ? r.bottom : r.top,
    })
  }

  return (
    <span
      ref={wrapRef}
      className={className ?? 'inline-flex max-w-full'}
      onMouseEnter={e => showAt(e.clientX, e.clientY)}
      onMouseMove={placement === 'cursor' ? e => setPos({ x: e.clientX, y: e.clientY }) : undefined}
      onMouseLeave={() => setPos(null)}
    >
      {children}
      {pos && createPortal(
        <div
          role="tooltip"
          className="fixed z-[var(--z-tooltip)] rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] px-2 py-1.5 text-helper text-[var(--text-primary)] shadow-md pointer-events-none whitespace-pre-wrap break-words"
          style={{
            left: pos.x,
            top: placement === 'bottom' ? pos.y + 6 : pos.y - 6,
            maxWidth,
            transform:
              placement === 'cursor'
                ? 'translate(12px, calc(-100% - 4px))'
                : placement === 'bottom'
                  ? 'translate(-50%, 0)'
                  : 'translate(-50%, -100%)',
          }}
        >
          {tip}
        </div>,
        document.body,
      )}
    </span>
  )
}
