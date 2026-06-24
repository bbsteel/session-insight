import type { TurnVM } from './types'

export type TokenPressureTone = 'empty' | 'low' | 'medium' | 'high' | 'critical'
export type MiniMapEventKind = 'anomaly' | 'compaction' | 'user'

export function hasCompaction(turn: TurnVM): boolean {
  return turn.anomalies?.some(a => a.includes('compaction') || a.includes('compression')) ?? false
}

export function getTokenPressureTone(ratio: number): TokenPressureTone {
  if (ratio <= 0) return 'empty'
  if (ratio >= 0.95) return 'critical'
  if (ratio >= 0.75) return 'high'
  if (ratio >= 0.4) return 'medium'
  return 'low'
}

export function getMiniMapEventKind(turn: TurnVM): MiniMapEventKind | null {
  if (hasCompaction(turn)) return 'compaction'
  if ((turn.anomalies?.length ?? 0) > 0 || turn.error_count > 0) return 'anomaly'
  if (turn.user_message) return 'user'
  return null
}

export function getMiniMapTurnPositionPercent(index: number, turnCount: number): number {
  if (turnCount <= 1) return 0
  return (index / (turnCount - 1)) * 100
}
