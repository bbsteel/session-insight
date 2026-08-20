import type { CodingQuotaWindow } from '../api'
import { formatNumber, type Locale } from '../i18n'

export type QuotaTranslate = (key: string, vars?: Record<string, string | number>) => string

function localizedDurationPart(
  count: number,
  singularKey: string,
  pluralKey: string,
  translate: QuotaTranslate,
): string {
  return translate(count === 1 ? singularKey : pluralKey, { count })
}

function fullDuration(totalSeconds: number, translate: QuotaTranslate): string {
  const wholeSeconds = Math.max(0, Math.ceil(totalSeconds))
  const days = Math.floor(wholeSeconds / 86400)
  const hours = Math.floor((wholeSeconds % 86400) / 3600)
  const minutes = Math.floor((wholeSeconds % 3600) / 60)
  const durationParts: string[] = []
  if (days > 0) durationParts.push(localizedDurationPart(days, 'quota.duration.day', 'quota.duration.days', translate))
  if (hours > 0) durationParts.push(localizedDurationPart(hours, 'quota.duration.hour', 'quota.duration.hours', translate))
  if (days === 0 && minutes > 0) durationParts.push(localizedDurationPart(minutes, 'quota.duration.minute', 'quota.duration.minutes', translate))
  if (durationParts.length === 0) durationParts.push(localizedDurationPart(1, 'quota.duration.minute', 'quota.duration.minutes', translate))
  return durationParts.join(' ')
}

function fallbackWindowLabel(windowId: string): string {
  return windowId
    .replace(/[_-]+/g, ' ')
    .replace(/\b\w/g, character => character.toUpperCase())
}

function periodFromSeconds(seconds: number): string | undefined {
  if (seconds >= 25 * 86400) return 'monthly'
  if (seconds >= 5 * 86400) return 'weekly'
  if (seconds > 0 && seconds <= 6 * 3600) return 'five_hour'
  return undefined
}

function normalizedWindowPeriod(window: CodingQuotaWindow): string | undefined {
  const normalizedID = window.id.trim().toLowerCase().replace(/[-\s]+/g, '_')
  if (normalizedID === 'monthly' || normalizedID === 'month') return 'monthly'
  if (normalizedID === 'weekly' || normalizedID === 'week' || normalizedID === 'usage') return 'weekly'
  if (normalizedID === 'five_hour' || normalizedID === 'five_hours' || normalizedID === '5h') return 'five_hour'
  if (normalizedID === 'seven_day' || normalizedID.startsWith('seven_day_')) return normalizedID
  if (typeof window.window_seconds === 'number') return periodFromSeconds(window.window_seconds)
  if (window.reset_at) {
    const secondsUntilReset = (new Date(window.reset_at).getTime() - Date.now()) / 1000
    return periodFromSeconds(secondsUntilReset)
  }
  return undefined
}

export function windowLabel(window: CodingQuotaWindow, translate: QuotaTranslate): string {
  const periodID = normalizedWindowPeriod(window)
  if (periodID) {
    const periodKey = `quota.window.${periodID}`
    const periodTranslation = translate(periodKey)
    if (periodTranslation !== periodKey) return periodTranslation
  }
  const windowId = window.id
  const rateLimitMatch = /^limit_(\d+)$/.exec(windowId)
  if (rateLimitMatch) return translate('quota.window.numbered', { index: Number(rateLimitMatch[1]) + 1 })
  const bucketMatch = /^bucket_(\d+)$/.exec(windowId)
  if (bucketMatch) return translate('quota.window.numbered', { index: Number(bucketMatch[1]) + 1 })
  const key = `quota.window.${windowId}`
  const translated = translate(key)
  return translated === key
    ? translate('quota.window.generic', { name: fallbackWindowLabel(windowId) })
    : translated
}

export function remainingValue(window: CodingQuotaWindow, locale: Locale, translate: QuotaTranslate): string {
  if (typeof window.remaining_percent === 'number') {
    return `${formatNumber(locale, window.remaining_percent, { maximumFractionDigits: 1 })}%`
  }
  if (typeof window.remaining_amount === 'number') {
    const amount = formatNumber(locale, window.remaining_amount, { maximumFractionDigits: 2 })
    return window.unit ? `${amount} ${window.unit}` : amount
  }
  return translate('quota.noRemainingValue')
}

export function resetValue(window: CodingQuotaWindow, translate: QuotaTranslate): string {
  if (!window.reset_at) return translate('quota.resetUnknown')
  const secondsUntilReset = (new Date(window.reset_at).getTime() - Date.now()) / 1000
  if (secondsUntilReset <= 0) return translate('quota.resetNow')
  return translate('quota.resetsIn', { duration: fullDuration(secondsUntilReset, translate) })
}

export function remainingToneClass(window: CodingQuotaWindow): string {
  if (typeof window.remaining_percent !== 'number') return 'text-[var(--text-primary)]'
  if (window.remaining_percent <= 10) return 'text-[var(--error)]'
  if (window.remaining_percent <= 25) return 'text-[var(--warning)]'
  return 'text-[var(--text-primary)]'
}

export function isPercentageWindow(window: CodingQuotaWindow): boolean {
  return typeof window.remaining_percent === 'number' && (window.unit === 'percent' || typeof window.remaining_amount !== 'number')
}
