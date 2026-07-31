/**
 * Resolve a collaboration source anchor to a replay position for the
 * jump-to-launch / jump-to-result actions (pure; no React/DOM).
 *
 * Resolution uses only replay anchors that already exist in the positions API
 * payload — exact event_id first, then tool_call_id, turn_index, and nearest
 * timestamp — and returns null when nothing reliable matches. Result jumps
 * use the matching result coordinates carried by the tool position instead
 * of landing on the invocation that shares its tool_call_id.
 */

import type { MiniMapPosition } from '../types.js'
import type { SourceAnchorDTO } from './types.js'

export interface ResolvedJump {
  lineStart: number
  logicalStart?: number
}

export type CollaborationJumpTarget = 'launch' | 'result'

function logicalStartOf(position: MiniMapPosition): number | undefined {
  const value = position.payload?.logical_start
  return typeof value === 'number' ? value : undefined
}

function resultJumpOf(position: MiniMapPosition): ResolvedJump | null {
  const lineStart = position.payload?.result_line_start
  if (typeof lineStart !== 'number') return null
  const logicalStart = position.payload?.result_logical_start
  return {
    lineStart,
    logicalStart: typeof logicalStart === 'number' ? logicalStart : undefined,
  }
}

function jumpOf(position: MiniMapPosition, target: CollaborationJumpTarget): ResolvedJump | null {
  if (target === 'result') return resultJumpOf(position)
  return { lineStart: position.line_start, logicalStart: logicalStartOf(position) }
}

function tsOf(position: MiniMapPosition, target: CollaborationJumpTarget): number | null {
  const value = target === 'result' ? position.payload?.result_ts_ms : position.payload?.ts_ms
  return typeof value === 'number' ? value : null
}

export function resolveAnchorJump(
  anchor: SourceAnchorDTO | null | undefined,
  positions: readonly MiniMapPosition[],
  target: CollaborationJumpTarget = 'launch',
): ResolvedJump | null {
  if (!anchor) return null

  // Event identity distinguishes the invocation and result of the same tool
  // call. The formatter records both on the call's single tool position.
  if (anchor.event_id) {
    const position = positions.find((p) => (
      target === 'result'
        ? p.payload?.result_event_id === anchor.event_id
        : p.payload?.event_id === anchor.event_id
    ))
    if (position) return jumpOf(position, target)
  }

  // Exact join: the anchor's native tool call id is recorded on tool positions.
  if (anchor.tool_call_id) {
    const position = positions.find(
      (p) => p.kind === 'tool' && p.payload?.tool_call_id === anchor.tool_call_id,
    )
    if (position) {
      const jump = jumpOf(position, target)
      if (jump) return jump
    }
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
    const targetMs = Date.parse(anchor.timestamp)
    if (!Number.isNaN(targetMs)) {
      let best: MiniMapPosition | null = null
      let bestDelta = Number.POSITIVE_INFINITY
      for (const p of positions) {
        const ts = tsOf(p, target)
        if (ts === null) continue
        const delta = Math.abs(ts - targetMs)
        if (delta < bestDelta) {
          bestDelta = delta
          best = p
        }
      }
      if (best) return jumpOf(best, target)
    }
  }

  return null
}
