import { useEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

interface InstantTooltipProps {
  /** Empty/undefined text disables the tooltip and renders children as-is. */
  text?: string
  /** Structured content for tooltips that need deliberate visual hierarchy. */
  content?: ReactNode
  children: ReactNode
  className?: string
  placement?: 'top' | 'bottom' | 'left' | 'cursor' | 'cursor-left'
  /** Max width for long notes. */
  maxWidth?: number
  /** Keep compact metric tooltips on one line. */
  nowrap?: boolean
  /** Keep structured content open while the pointer moves into it. */
  interactive?: boolean
  /** Handle a click anywhere inside interactive structured content. */
  onContentClick?: () => void
}

/**
 * Custom hover tooltip with no browser delay (native `title` waits ~1s).
 * Use for bookmark notes and other hover copy that must appear immediately.
 */
export default function InstantTooltip({
  text,
  content,
  children,
  className,
  placement = 'top',
  maxWidth = 280,
  nowrap = false,
  interactive = false,
  onContentClick,
}: InstantTooltipProps) {
  const [pos, setPos] = useState<{ x: number; y: number } | null>(null)
  const wrapRef = useRef<HTMLSpanElement>(null)
  const hideTimerRef = useRef<number | null>(null)
  const tip = text?.trim() ?? ''
  const tooltipContent = content ?? tip

  useEffect(() => () => {
    if (hideTimerRef.current !== null) window.clearTimeout(hideTimerRef.current)
  }, [])

  if (!tip && content == null) {
    return <span className={className}>{children}</span>
  }

  const showAt = (clientX: number, clientY: number) => {
    if (placement === 'cursor' || placement === 'cursor-left') {
      setPos({ x: clientX, y: clientY })
      return
    }
    const r = wrapRef.current?.getBoundingClientRect()
    if (!r) {
      setPos({ x: clientX, y: clientY })
      return
    }
    setPos({
      x: placement === 'left' ? r.left : r.left + r.width / 2,
      y: placement === 'left' ? r.top + r.height / 2 : placement === 'bottom' ? r.bottom : r.top,
    })
  }

  const cancelHide = () => {
    if (hideTimerRef.current !== null) {
      window.clearTimeout(hideTimerRef.current)
      hideTimerRef.current = null
    }
  }

  const hide = () => {
    cancelHide()
    if (!interactive) {
      setPos(null)
      return
    }
    hideTimerRef.current = window.setTimeout(() => {
      hideTimerRef.current = null
      setPos(null)
    }, 160)
  }

  const handleContentClick = () => {
    onContentClick?.()
    cancelHide()
    setPos(null)
  }

  return (
    <span
      ref={wrapRef}
      className={className ?? 'inline-flex max-w-full'}
      onMouseEnter={e => {
        cancelHide()
        showAt(e.clientX, e.clientY)
      }}
      onMouseMove={placement === 'cursor' || placement === 'cursor-left' ? e => setPos({ x: e.clientX, y: e.clientY }) : undefined}
      onMouseLeave={hide}
      onFocus={() => showAt(0, 0)}
      onBlur={e => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setPos(null)
      }}
    >
      {children}
      {pos && createPortal(
        <div
          role={interactive ? 'button' : 'tooltip'}
          className={`fixed z-[var(--z-tooltip)] rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] px-2 py-1.5 text-helper text-[var(--text-primary)] shadow-md ${interactive ? 'pointer-events-auto cursor-pointer' : 'pointer-events-none'} ${nowrap ? 'whitespace-nowrap' : 'whitespace-pre-wrap break-words'}`}
          onMouseEnter={interactive ? cancelHide : undefined}
          onMouseLeave={interactive ? hide : undefined}
          onClick={interactive ? handleContentClick : undefined}
          style={{
            left: placement === 'left' ? pos.x - 6 : pos.x,
            top: placement === 'bottom' ? pos.y + 6 : placement === 'left' ? pos.y : pos.y - 6,
            width: nowrap ? 'max-content' : undefined,
            maxWidth: nowrap ? 'calc(100vw - 16px)' : maxWidth,
            transform:
              placement === 'left'
                ? 'translate(-100%, -50%)'
                : placement === 'cursor-left'
                  ? 'translate(calc(-100% - 12px), calc(-100% - 4px))'
                : placement === 'cursor'
                ? 'translate(12px, calc(-100% - 4px))'
                : placement === 'bottom'
                  ? 'translate(-50%, 0)'
                  : 'translate(-50%, -100%)',
          }}
        >
          {tooltipContent}
        </div>,
        document.body,
      )}
    </span>
  )
}
