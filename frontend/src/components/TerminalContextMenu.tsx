import { useLayoutEffect, useEffect, useRef, useState } from 'react'

// Sectioned context menu for the terminal area. Sections render in order with
// a separator between them; an empty section shows its emptyText placeholder
// so the frame stays visible while a section has no actions yet.

export interface TerminalMenuItem {
  label: string
  disabled?: boolean
  onClick: () => void
}

export interface TerminalMenuSection {
  title: string
  items: TerminalMenuItem[]
  emptyText?: string
}

interface Props {
  x: number
  y: number
  sections: TerminalMenuSection[]
  onClose: () => void
}

export default function TerminalContextMenu({ x, y, sections, onClose }: Props) {
  const menuRef = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState({ left: x, top: y })

  // Clamp into the viewport once the menu has a measurable size.
  useLayoutEffect(() => {
    const el = menuRef.current
    if (!el) return
    const rect = el.getBoundingClientRect()
    const left = Math.max(4, Math.min(x, window.innerWidth - rect.width - 4))
    const top = Math.max(4, Math.min(y, window.innerHeight - rect.height - 4))
    setPos({ left, top })
  }, [x, y])

  useEffect(() => {
    const onMouseDown = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) onClose()
    }
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    const close = () => onClose()
    document.addEventListener('mousedown', onMouseDown, true)
    // Capture phase: focus sits in xterm's helper textarea, which stops
    // keydown propagation before it bubbles back to document.
    document.addEventListener('keydown', onKeyDown, true)
    document.addEventListener('wheel', close, { capture: true, passive: true })
    window.addEventListener('resize', close)
    window.addEventListener('blur', close)
    return () => {
      document.removeEventListener('mousedown', onMouseDown, true)
      document.removeEventListener('keydown', onKeyDown, true)
      document.removeEventListener('wheel', close, true)
      window.removeEventListener('resize', close)
      window.removeEventListener('blur', close)
    }
  }, [onClose])

  return (
    <div
      ref={menuRef}
      role="menu"
      className="fixed z-50 min-w-[180px] rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] py-1 shadow-md"
      style={{ left: pos.left, top: pos.top }}
      onContextMenu={e => e.preventDefault()}
    >
      {sections.map((section, si) => (
        <div key={section.title}>
          {si > 0 && <div className="my-1 border-t border-[var(--border-muted)]" />}
          <div className="px-2.5 pb-0.5 pt-1 text-meta text-[var(--text-muted)] select-none">{section.title}</div>
          {section.items.length === 0 ? (
            <div className="px-2.5 py-1 text-meta text-[var(--text-muted)] italic select-none">
              {section.emptyText ?? '暂无操作'}
            </div>
          ) : (
            section.items.map(item => (
              <button
                key={item.label}
                role="menuitem"
                disabled={item.disabled}
                onClick={() => { if (!item.disabled) item.onClick() }}
                className="block w-full px-2.5 py-1 text-left text-helper text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] disabled:cursor-default disabled:text-[var(--text-muted)] disabled:hover:bg-transparent"
              >
                {item.label}
              </button>
            ))
          )}
        </div>
      ))}
    </div>
  )
}
