import { useCallback, useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import { fetchCodingQuotas, type CodingQuotaProvider, type CodingQuotaWindow } from '../api'
import { formatDate, formatNumber, useI18n } from '../i18n'

interface CodingQuotaDialogProps {
  onClose: () => void
}

function localizedDurationPart(
  count: number,
  singularKey: string,
  pluralKey: string,
  translate: (key: string, vars?: Record<string, string | number>) => string,
): string {
  return translate(count === 1 ? singularKey : pluralKey, { count })
}

function fullDuration(
  totalSeconds: number,
  translate: (key: string, vars?: Record<string, string | number>) => string,
): string {
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

function fallbackWindowLabel(windowID: string): string {
  return windowID
    .replace(/[_-]+/g, ' ')
    .replace(/\b\w/g, character => character.toUpperCase())
}

function windowLabel(windowID: string, translate: (key: string, vars?: Record<string, string | number>) => string): string {
  const rateLimitMatch = /^limit_(\d+)$/.exec(windowID)
  if (rateLimitMatch) return translate('quota.window.numbered', { index: Number(rateLimitMatch[1]) + 1 })
  const bucketMatch = /^bucket_(\d+)$/.exec(windowID)
  if (bucketMatch) return translate('quota.window.numbered', { index: Number(bucketMatch[1]) + 1 })
  const key = `quota.window.${windowID}`
  const translated = translate(key)
  return translated === key
    ? translate('quota.window.generic', { name: fallbackWindowLabel(windowID) })
    : translated
}

function remainingValue(window: CodingQuotaWindow, locale: 'zh-CN' | 'en', translate: (key: string) => string): string {
  if (typeof window.remaining_percent === 'number') {
    return `${formatNumber(locale, window.remaining_percent, { maximumFractionDigits: 1 })}%`
  }
  if (typeof window.remaining_amount === 'number') {
    const amount = formatNumber(locale, window.remaining_amount, { maximumFractionDigits: 2 })
    return window.unit ? `${amount} ${window.unit}` : amount
  }
  return translate('quota.noRemainingValue')
}

function resetValue(window: CodingQuotaWindow, translate: (key: string, vars?: Record<string, string | number>) => string): string {
  if (!window.reset_at) return translate('quota.resetUnknown')
  const secondsUntilReset = (new Date(window.reset_at).getTime() - Date.now()) / 1000
  if (secondsUntilReset <= 0) return translate('quota.resetNow')
  return translate('quota.resetsIn', { duration: fullDuration(secondsUntilReset, translate) })
}

function remainingToneClass(window: CodingQuotaWindow): string {
  if (typeof window.remaining_percent !== 'number') return 'text-[var(--text-primary)]'
  if (window.remaining_percent <= 10) return 'text-[var(--error)]'
  if (window.remaining_percent <= 25) return 'text-[var(--warning)]'
  return 'text-[var(--text-primary)]'
}

function isPercentageWindow(window: CodingQuotaWindow): boolean {
  return typeof window.remaining_percent === 'number' && (window.unit === 'percent' || typeof window.remaining_amount !== 'number')
}

function isVisibleByDefault(provider: CodingQuotaProvider): boolean {
  return provider.snapshot.status !== 'not_configured' && provider.snapshot.status !== 'unsupported'
}

function statusLabel(provider: CodingQuotaProvider, translate: (key: string) => string): string {
  const status = provider.snapshot.stale ? 'stale' : provider.snapshot.status
  return translate(`quota.status.${status}`)
}

function ProviderCard({ provider }: { provider: CodingQuotaProvider }) {
  const { locale, t } = useI18n()
  const snapshot = provider.snapshot
  const windows = snapshot.windows ?? []
  const hasFreshQuota = snapshot.status === 'available' && windows.length > 0
  const statusText = statusLabel(provider, t)

  return (
    <article
      className="rounded-lg border border-[var(--border-default)] bg-[var(--bg-surface)] p-3"
      data-testid={`quota-provider-${provider.id}`}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-nav font-semibold text-[var(--text-primary)]">{t(provider.display_name_key)}</h3>
            <span className={`rounded px-1.5 py-0.5 text-meta ${hasFreshQuota ? 'bg-[var(--accent-green)]/15 text-[var(--success)]' : 'bg-[var(--bg-inset)] text-[var(--text-secondary)]'}`}>
              {statusText}
            </span>
            {provider.supports_exact_quota && (
              <span className="rounded bg-[var(--accent-blue)]/10 px-1.5 py-0.5 text-meta text-[var(--accent-blue)]">
                {t('quota.exact')}
              </span>
            )}
          </div>
          <p className="mt-1 text-helper text-[var(--text-secondary)]">{t(provider.description_key)}</p>
          {provider.quota_strategy_key && (
            <p className="mt-1 text-helper text-[var(--text-secondary)]" data-testid={`quota-strategy-${provider.id}`}>
              <span className="font-medium text-[var(--text-primary)]">{t('quota.strategyLabel')}:</span>{' '}
              {t(provider.quota_strategy_key)}
            </p>
          )}
        </div>
        <div className="flex flex-shrink-0 items-start gap-2 text-helper">
          {provider.documentation_url && (
            <a
              className="text-[var(--accent-blue)] hover:underline"
              href={provider.documentation_url}
              target="_blank"
              rel="noreferrer"
              data-testid={`quota-documentation-${provider.id}`}
            >
              {t('quota.documentation')}
            </a>
          )}
        </div>
      </div>

      {windows.length > 0 ? (
        <div className="mt-3 grid gap-2 sm:grid-cols-2">
          {windows.map(window => (
            <div
              key={`${provider.id}-${window.id}`}
              className="rounded-md bg-[var(--bg-inset)] px-3 py-2"
              data-testid={`quota-window-${provider.id}-${window.id}`}
              data-quota-percentage-window={isPercentageWindow(window) ? 'true' : 'false'}
            >
              <div className="flex items-center justify-between gap-2 text-helper text-[var(--text-secondary)]">
                <span>{windowLabel(window.id, t)}</span>
                {window.reset_at && (
                  <span className="text-[var(--text-muted)]" data-testid={`quota-reset-${provider.id}-${window.id}`}>
                    {resetValue(window, t)}
                  </span>
                )}
              </div>
              <div
                className={`mt-1 text-title font-semibold tabular-nums ${remainingToneClass(window)}`}
                data-testid={`quota-remaining-${provider.id}-${window.id}`}
              >
                {remainingValue(window, locale, t)}
                {typeof window.remaining_percent === 'number' && <span className="ml-1 text-helper font-normal text-[var(--text-secondary)]">{t('quota.remaining')}</span>}
              </div>
              {typeof window.limit_amount === 'number' && !isPercentageWindow(window) && (
                <div className="mt-0.5 text-meta text-[var(--text-muted)]">
                  {t('quota.limit', { amount: `${formatNumber(locale, window.limit_amount, { maximumFractionDigits: 2 })}${window.unit ? ` ${window.unit}` : ''}` })}
                </div>
              )}
            </div>
          ))}
        </div>
      ) : (
        <div className="mt-3 rounded-md bg-[var(--bg-inset)] px-3 py-2 text-body text-[var(--text-secondary)]">
          {t(`quota.statusHelp.${snapshot.status}`)}
        </div>
      )}

      {snapshot.stale && windows.length > 0 && (
        <p className="mt-2 text-meta text-[var(--warning)]">{t('quota.staleHelp')}</p>
      )}
      {snapshot.observed_at && (
        <p className="mt-2 text-meta text-[var(--text-muted)]">
          {t('quota.updatedAt', { time: formatDate(locale, snapshot.observed_at, { dateStyle: 'medium', timeStyle: 'short' }) })}
        </p>
      )}
    </article>
  )
}

export default function CodingQuotaDialog({ onClose }: CodingQuotaDialogProps) {
  const { t } = useI18n()
  const [providers, setProviders] = useState<CodingQuotaProvider[]>([])
  const [showAllProviders, setShowAllProviders] = useState(false)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState(false)

  const load = useCallback((forceRefresh: boolean) => {
    if (forceRefresh) setRefreshing(true)
    else setLoading(true)
    setError(false)
    void fetchCodingQuotas(forceRefresh)
      .then(response => setProviders(response.providers))
      .catch(() => setError(true))
      .finally(() => {
        setLoading(false)
        setRefreshing(false)
      })
  }, [])

  useEffect(() => {
    load(false)
  }, [load])

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [onClose])

  const visibleProviders = showAllProviders ? providers : providers.filter(isVisibleByDefault)

  return createPortal(
    <div
      className="fixed inset-0 z-[420] flex items-center justify-center bg-[rgba(0,0,0,var(--opacity-overlay,0.4))] p-4"
      onClick={event => { if (event.target === event.currentTarget) onClose() }}
    >
      <section
        className="flex max-h-[86vh] w-full max-w-3xl flex-col overflow-hidden rounded-xl border border-[var(--border-default)] bg-[var(--bg-surface)] shadow-2xl"
        role="dialog"
        aria-modal="true"
        aria-labelledby="coding-quota-title"
        data-testid="coding-quota-dialog"
      >
        <header className="flex items-start justify-between gap-4 border-b border-[var(--border-default)] px-5 py-4">
          <div>
            <h2 id="coding-quota-title" className="text-title font-semibold text-[var(--text-primary)]">{t('quota.title')}</h2>
            <p className="mt-1 max-w-2xl text-body text-[var(--text-secondary)]">{t('quota.subtitle')}</p>
          </div>
          <div className="flex flex-shrink-0 items-center gap-2">
            <div className="inline-flex rounded-md border border-[var(--border-default)] p-0.5" role="group" aria-label={t('quota.viewLabel')}>
              <button
                type="button"
                onClick={() => setShowAllProviders(false)}
                aria-pressed={!showAllProviders}
                className={`h-6 rounded px-1.5 text-meta ${!showAllProviders ? 'bg-[var(--bg-surface-hover)] text-[var(--text-primary)]' : 'text-[var(--text-muted)] hover:text-[var(--text-primary)]'}`}
                data-testid="coding-quota-filter-configured"
              >
                {t('quota.view.configured')}
              </button>
              <button
                type="button"
                onClick={() => setShowAllProviders(true)}
                aria-pressed={showAllProviders}
                className={`h-6 rounded px-1.5 text-meta ${showAllProviders ? 'bg-[var(--bg-surface-hover)] text-[var(--text-primary)]' : 'text-[var(--text-muted)] hover:text-[var(--text-primary)]'}`}
                data-testid="coding-quota-filter-all"
              >
                {t('quota.view.all')}
              </button>
            </div>
            <button
              type="button"
              onClick={() => load(true)}
              disabled={loading || refreshing}
              className="h-7 rounded-md border border-[var(--border-default)] px-2.5 text-nav text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
              data-testid="coding-quota-refresh"
            >
              {refreshing ? t('quota.refreshing') : t('quota.refresh')}
            </button>
            <button
              type="button"
              onClick={onClose}
              aria-label={t('common.close')}
              className="flex h-7 w-7 items-center justify-center rounded-md text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
            >
              ×
            </button>
          </div>
        </header>
        <div className="min-h-0 overflow-y-auto p-5">
          {loading ? (
            <div className="space-y-3" role="status" aria-label={t('common.loading')}>
              {[0, 1, 2].map(index => <div key={index} className="h-24 animate-pulse rounded-lg bg-[var(--bg-surface-hover)]" />)}
            </div>
          ) : error ? (
            <div className="rounded-lg border border-[var(--border-default)] bg-[var(--bg-inset)] px-4 py-5 text-center text-body text-[var(--text-secondary)]">
              <p>{t('quota.loadFailed')}</p>
              <button
                type="button"
                onClick={() => load(false)}
                className="mt-3 h-7 rounded-md bg-[var(--accent-blue)] px-3 text-nav text-white hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
              >
                {t('common.retry')}
              </button>
            </div>
          ) : (
            visibleProviders.length > 0 ? (
              <div className="space-y-3">
                {visibleProviders.map(provider => <ProviderCard key={provider.id} provider={provider} />)}
              </div>
            ) : (
              <div className="rounded-lg border border-[var(--border-default)] bg-[var(--bg-inset)] px-4 py-6 text-center text-body text-[var(--text-secondary)]" data-testid="coding-quota-empty-configured">
                <p>{t('quota.emptyConfigured')}</p>
                <button
                  type="button"
                  onClick={() => setShowAllProviders(true)}
                  className="mt-3 h-7 rounded-md border border-[var(--border-default)] px-3 text-nav text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
                >
                  {t('quota.view.all')}
                </button>
              </div>
            )
          )}
        </div>
      </section>
    </div>,
    document.body,
  )
}
