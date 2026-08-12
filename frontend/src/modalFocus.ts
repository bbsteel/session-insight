import { useEffect, type RefObject } from 'react'

const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

/**
 * Keeps keyboard focus inside the active modal and restores it to the trigger.
 * A parent modal ignores events while focus is inside a nested sibling modal.
 */
export function useModalFocus(
  containerRef: RefObject<HTMLElement>,
  onClose: () => void,
  initialFocusRef?: RefObject<HTMLElement>,
  enabled = true,
): void {
  useEffect(() => {
    if (!enabled) return
    const restore = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const frame = window.requestAnimationFrame(() => {
      const container = containerRef.current
      const initial = initialFocusRef?.current ?? container?.querySelector<HTMLElement>(FOCUSABLE)
      initial?.focus({ preventScroll: true })
    })
    const onKey = (event: KeyboardEvent) => {
      const container = containerRef.current
      if (!container || !container.contains(document.activeElement)) return
      if (event.key === 'Escape') {
        event.preventDefault()
        event.stopPropagation()
        event.stopImmediatePropagation()
        onClose()
        return
      }
      if (event.key !== 'Tab') return
      const focusable = Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE))
        .filter(element => element.getClientRects().length > 0)
      if (focusable.length === 0) {
        event.preventDefault()
        return
      }
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault()
        first.focus()
      }
    }
    window.addEventListener('keydown', onKey, true)
    return () => {
      window.cancelAnimationFrame(frame)
      window.removeEventListener('keydown', onKey, true)
      if (restore && document.contains(restore)) restore.focus({ preventScroll: true })
    }
  }, [containerRef, enabled, initialFocusRef, onClose])
}
