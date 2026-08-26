import { useEffect, useState, type RefObject } from 'react'
import { computeAnchoredPanelRect, type AnchoredPanelRect } from './anchoredPanelRect'

export type { AnchoredPanelRect } from './anchoredPanelRect'

/**
 * Panel rect state for an anchored filter panel: computed when `open` turns
 * true, recomputed on window resize and whenever the anchor element itself
 * changes size (for example while the sidebar is being resized), cleared on
 * close. `open` itself stays owned by the caller (trigger toggle, Escape,
 * click-outside).
 */
export function useAnchoredPanelRect(
  open: boolean,
  anchorRef: RefObject<HTMLElement | null>,
  maxWidth: number,
): AnchoredPanelRect | null {
  const [panelRect, setPanelRect] = useState<AnchoredPanelRect | null>(null)

  useEffect(() => {
    if (!open) {
      setPanelRect(null)
      return
    }
    const update = () => setPanelRect(computeAnchoredPanelRect(anchorRef.current, maxWidth))
    update()
    window.addEventListener('resize', update)
    // The anchor tracks the sidebar width, so observing it keeps the fixed
    // panel glued to the anchor while the sidebar resize handle is dragged —
    // a window resize event does not fire in that case.
    const anchor = anchorRef.current
    const observer = typeof ResizeObserver !== 'undefined' && anchor
      ? new ResizeObserver(update)
      : null
    if (observer && anchor) observer.observe(anchor)
    return () => {
      window.removeEventListener('resize', update)
      observer?.disconnect()
    }
    // anchorRef is a stable ref object; maxWidth is a constant per panel.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  return panelRect
}
