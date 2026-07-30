/**
 * Adaptive time-axis tick computation for the collaboration timeline
 * (pure; no React/DOM/i18n imports).
 *
 * Picks the smallest "nice" step that keeps the visible tick count at or
 * below the target, then emits ticks on exact step multiples inside the
 * visible domain. The renderer owns label formatting; this module only
 * chooses times so it stays testable without a locale.
 */

const SECOND = 1_000
const MINUTE = 60 * SECOND
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

/** Candidate steps in ascending order (nice human-readable boundaries). */
export const AXIS_STEPS_MS: readonly number[] = [
  SECOND,
  5 * SECOND,
  15 * SECOND,
  30 * SECOND,
  MINUTE,
  5 * MINUTE,
  15 * MINUTE,
  30 * MINUTE,
  HOUR,
  3 * HOUR,
  6 * HOUR,
  12 * HOUR,
  DAY,
  2 * DAY,
  7 * DAY,
  14 * DAY,
  30 * DAY,
  90 * DAY,
  365 * DAY,
]

/** Smallest step that yields at most `targetTicks` intervals across the span. */
export function axisStepMs(spanMs: number, targetTicks = 5): number {
  const span = Math.max(1, spanMs)
  for (const step of AXIS_STEPS_MS) {
    if (span / step <= targetTicks) return step
  }
  return AXIS_STEPS_MS[AXIS_STEPS_MS.length - 1]
}

/**
 * Tick times on exact step multiples within [startMs, endMs]. Never exceeds
 * targetTicks + 1 entries for a sane domain; empty when the range is invalid.
 */
export function axisTicks(startMs: number, endMs: number, targetTicks = 5): number[] {
  if (!(endMs > startMs)) return []
  const step = axisStepMs(endMs - startMs, targetTicks)
  const first = Math.ceil(startMs / step) * step
  const ticks: number[] = []
  for (let t = first; t <= endMs; t += step) ticks.push(t)
  return ticks
}
