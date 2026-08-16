// Pure MiniMap logic: relative cost-pressure tone and fallback-mode layout.
// v0.6.1 removed the event-marker mapping (hasCompaction /
// getMiniMapEventKind): precise event discovery and jumping now live in the
// key-event outline panel, and the MiniMap is a passive overview.

export type TokenPressureTone = 'empty' | 'low' | 'medium' | 'high' | 'critical'

export function getTokenPressureTone(ratio: number): TokenPressureTone {
  if (ratio <= 0) return 'empty'
  if (ratio >= 0.95) return 'critical'
  if (ratio >= 0.75) return 'high'
  if (ratio >= 0.4) return 'medium'
  return 'low'
}

export function getMiniMapTurnPositionPercent(index: number, turnCount: number): number {
  if (turnCount <= 1) return 0
  return (index / (turnCount - 1)) * 100
}
