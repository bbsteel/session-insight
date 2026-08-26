import { useEffect, useState, type RefObject } from 'react'

export interface AnchoredPanelRect {
  left: number
  top: number
  width: number
  maxHeight: number
}

/**
 * Compute the fixed-position rect for a filter panel that opens to the RIGHT of
 * its sidebar trigger, so the panel tiles into the content area instead of
 * covering the session list. Clamped to the viewport.
 */
export function computeAnchoredPanelRect(anchor: HTMLElement | null, maxWidth: number): AnchoredPanelRect | null {
  const anchorRect = anchor?.getBoundingClientRect()
  if (!anchorRect) return null
  const width = Math.min(maxWidth, window.innerWidth - 16)
  const left = Math.max(8, Math.min(anchorRect.right + 8, window.innerWidth - width - 8))
  const top = Math.max(8, anchorRect.top)
  const maxHeight = Math.max(240, window.innerHeight - top - 16)
  return { left, top, width, maxHeight }
}

/**
 * Panel rect state for an anchored filter panel: computed when `open` turns
 * true, recomputed on window resize, cleared on close. `open` itself stays
 * owned by the caller (trigger toggle, Escape, click-outside).
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
    return () => window.removeEventListener('resize', update)
    // anchorRef is a stable ref object; maxWidth is a constant per panel.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  return panelRect
}
