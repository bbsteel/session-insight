export interface AnchoredPanelRect {
  left: number
  top: number
  width: number
  maxHeight: number
}

/** Gap between the anchor's right edge and the panel, and the panel's outer viewport gutter. */
const PANEL_GUTTER = 8
/** Below the anchor the panel keeps at least this much height by shifting up; it never overflows the viewport. */
const PANEL_MIN_USABLE_HEIGHT = 240
const PANEL_BOTTOM_GUTTER = 16

/** Minimal viewport the pure geometry reads; tests stub this on globalThis. */
interface AnchoredPanelViewport {
  innerWidth: number
  innerHeight: number
}

function panelViewport(): AnchoredPanelViewport {
  return (globalThis as { window?: AnchoredPanelViewport }).window ?? { innerWidth: 0, innerHeight: 0 }
}

/**
 * Compute the fixed-position rect for a filter panel that opens to the RIGHT of
 * its sidebar trigger, so the panel tiles into the content area instead of
 * covering the session list.
 *
 * When the anchor lives inside the session sidebar (`#session-sidebar`), the
 * panel is positioned from the sidebar's outer right edge rather than the
 * padded trigger edge — otherwise the 8px gutter would land exactly on the
 * sidebar's resize handle and the open panel would swallow its pointer events.
 *
 * The rect is fully constrained by the space beside the sidebar:
 * - left always starts after the sidebar (or anchor), so the panel can never
 *   slide back over the sidebar on narrow windows;
 * - width is capped by the space remaining after the anchor, not just by the
 *   viewport;
 * - top may shift up so the panel keeps a usable height, but maxHeight is the
 *   actual remaining viewport height — the panel never crosses the bottom edge
 *   (its content scrolls internally).
 */
export function computeAnchoredPanelRect(
  anchor: {
    getBoundingClientRect(): { right: number; top: number }
    closest?(selector: string): { getBoundingClientRect(): { right: number } } | null
  } | null,
  maxWidth: number,
): AnchoredPanelRect | null {
  const anchorRect = anchor?.getBoundingClientRect()
  if (!anchorRect) return null
  const viewport = panelViewport()
  const sidebarRight = anchor?.closest?.('#session-sidebar')?.getBoundingClientRect().right
  const anchorRight = Math.max(anchorRect.right, sidebarRight ?? anchorRect.right)
  const left = Math.max(PANEL_GUTTER, anchorRight + PANEL_GUTTER)
  const availableWidth = viewport.innerWidth - left - PANEL_GUTTER
  const width = Math.max(0, Math.min(maxWidth, availableWidth))
  if (width <= 0) return null
  let top = Math.max(PANEL_GUTTER, anchorRect.top)
  if (viewport.innerHeight - top - PANEL_BOTTOM_GUTTER < PANEL_MIN_USABLE_HEIGHT) {
    top = Math.max(PANEL_GUTTER, viewport.innerHeight - PANEL_BOTTOM_GUTTER - PANEL_MIN_USABLE_HEIGHT)
  }
  const maxHeight = Math.max(0, viewport.innerHeight - top - PANEL_BOTTOM_GUTTER)
  return { left, top, width, maxHeight }
}
