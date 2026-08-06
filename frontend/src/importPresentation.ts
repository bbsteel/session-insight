/**
 * Pure presentation helpers for the session migration (export/import bundle)
 * feature. No React / fetch / DOM access — unit-tested standalone.
 */

import type { SessionSummary } from './types'
import type { ExportBundleRequest, ImportBundleSummary } from './api'

export type TranslateFn = (key: string, vars?: Record<string, string | number>) => string

/** Stable checkbox key; mirrors the CLI selector shape (`agent_type:id`). */
export function sessionSelectionKey(s: Pick<SessionSummary, 'id' | 'agent_type'>): string {
  return `${s.agent_type}:${s.id}`
}

/**
 * Initial export selection: only the focused sidebar session when it is in
 * the offered list; otherwise empty. Never pre-selects the whole list.
 */
export function initialExportSelection(
  sessions: Pick<SessionSummary, 'id' | 'agent_type'>[],
  preferred?: Pick<SessionSummary, 'id' | 'agent_type'> | null,
): string[] {
  if (!preferred?.id || !preferred.agent_type) return []
  const key = sessionSelectionKey(preferred)
  return sessions.some(s => sessionSelectionKey(s) === key) ? [key] : []
}

/** Sidebar badge line for an imported session: "imported · <host>". */
export function importedBadgeText(t: TranslateFn, host?: string): string {
  const trimmed = host?.trim()
  return t('sidebar.importedBadge', { host: trimmed || t('importBundle.unknownHost') })
}

export interface ExportOptions {
  includeRaw: boolean
  redact: boolean
  caseLabel: string
}

/** Maps the modal's selection + options to the POST body (ids only, trimmed label). */
export function buildExportRequest(
  sessions: Pick<SessionSummary, 'id' | 'agent_type'>[],
  opts: ExportOptions,
): ExportBundleRequest {
  return {
    sessions: sessions.map(s => ({ agent_type: s.agent_type, id: s.id })),
    include_raw: opts.includeRaw,
    redact: opts.redact,
    case_label: opts.caseLabel.trim(),
  }
}

/** One-line summary for a bundle row: "[case ·] host · N sessions". */
export function formatBundleSummary(t: TranslateFn, b: Pick<ImportBundleSummary, 'origin_host' | 'case_label' | 'session_count'>): string {
  const parts = [
    b.case_label?.trim(),
    b.origin_host?.trim() || t('importBundle.unknownHost'),
    t('importBundle.bundleCount', { count: b.session_count }),
  ].filter(Boolean)
  return parts.join(' · ')
}

/** Local-time download filename: si-bundle-<YYYYMMDD-HHMMSS>.sibundle. */
export function bundleFilename(date: Date = new Date()): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  const stamp = `${date.getFullYear()}${pad(date.getMonth() + 1)}${pad(date.getDate())}`
  const time = `${pad(date.getHours())}${pad(date.getMinutes())}${pad(date.getSeconds())}`
  return `si-bundle-${stamp}-${time}.sibundle`
}
