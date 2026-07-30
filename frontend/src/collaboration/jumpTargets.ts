/**
 * Resolve a collaboration source anchor to a replay position for the
 * jump-to-launch / jump-to-result actions (pure; no React/DOM).
 *
 * Resolution uses only replay anchors that already exist in the positions API
 * payload — exact tool_call_id first, then turn_index, then the nearest
 * timestamp — and returns null when nothing reliable matches, so the caller
 * can leave the action disabled instead of jumping to a guessed row.
 */

import type { MiniMapPosition } from '../types.js'
import type { SourceAnchorDTO } from './types.js'

export interface ResolvedJump {
  lineStart: number
  logicalStart?: number
}

function logicalStartOf(position: MiniMapPosition): number | undefined {
  const value = position.payload?.logical_start
  return typeof value === 'number' ? value : undefined
}

function tsOf(position: MiniMapPosition): number | null {
  const value = position.payload?.ts_ms
  return typeof value === 'number' ? value : null
}

export function resolveAnchorJump(
  anchor: SourceAnchorDTO | null | undefined,
  positions: readonly MiniMapPosition[],
): ResolvedJump | null {
  if (!anchor) return null

  // Exact join: the anchor's native tool call id is recorded on tool positions.
  if (anchor.tool_call_id) {
    const position = positions.find(
      (p) => p.kind === 'tool' && p.payload?.tool_call_id === anchor.tool_call_id,
    )
    if (position) return { lineStart: position.line_start, logicalStart: logicalStartOf(position) }
  }

  // Turn-level anchors land on the turn/user position for that turn.
  if (typeof anchor.turn_index === 'number') {
    const position = positions.find(
      (p) => p.turn_index === anchor.turn_index && (p.kind === 'turn' || p.kind === 'user'),
    )
    if (position) return { lineStart: position.line_start, logicalStart: logicalStartOf(position) }
  }

  // Weakest admissible fallback: nearest recorded position timestamp.
  if (anchor.timestamp) {
    const target = Date.parse(anchor.timestamp)
    if (!Number.isNaN(target)) {
      let best: MiniMapPosition | null = null
      let bestDelta = Number.POSITIVE_INFINITY
      for (const p of positions) {
        const ts = tsOf(p)
        if (ts === null) continue
        const delta = Math.abs(ts - target)
        if (delta < bestDelta) {
          bestDelta = delta
          best = p
        }
      }
      if (best) return { lineStart: best.line_start, logicalStart: logicalStartOf(best) }
    }
  }

  return null
}
