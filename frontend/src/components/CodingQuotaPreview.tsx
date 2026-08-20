import { useCallback, useRef, useState, type ReactNode } from 'react'
import { fetchCodingQuotas, type CodingQuotaProvider, type CodingQuotaWindow } from '../api'
import { formatNumber, useI18n } from '../i18n'
import InstantTooltip from './InstantTooltip'

interface CodingQuotaPreviewProps {
  icon: ReactNode
  onOpen: () => void
}

function isVisibleInPreview(provider: CodingQuotaProvider): boolean {
  return provider.snapshot.status !== 'not_configured' && provider.snapshot.status !== 'unsupported'
}

function providerStatusKey(provider: CodingQuotaProvider): string {
  return provider.snapshot.stale ? 'stale' : provider.snapshot.status
}

function windowSummary(
  window: CodingQuotaWindow,
  locale: 'zh-CN' | 'en',
  translate: (key: string) => string,
): string {
  if (typeof window.remaining_percent === 'number') {
    return `${formatNumber(locale, window.remaining_percent, { maximumFractionDigits: 1 })}%`
  }
  if (typeof window.remaining_amount === 'number') {
    const amount = formatNumber(locale, window.remaining_amount, { maximumFractionDigits: 2 })
    return window.unit ? `${amount} ${window.unit}` : amount
  }
  return translate('quota.noRemainingValue')
}

function providerValueSummary(
  provider: CodingQuotaProvider,
  locale: 'zh-CN' | 'en',
  translate: (key: string) => string,
): string {
  const windows = provider.snapshot.windows ?? []
  if (windows.length === 0) return translate(`quota.status.${providerStatusKey(provider)}`)
  return windows.slice(0, 3).map(window => windowSummary(window, locale, translate)).join(' · ')
}

function PreviewContent({
  providers,
  loading,
  error,
}: {
  providers: CodingQuotaProvider[]
  loading: boolean
  error: boolean
}) {
  const { locale, t } = useI18n()

  if (loading) {
    return <div data-testid="coding-quota-preview" className="w-[320px]">{t('quota.previewLoading')}</div>
  }
  if (error) {
    return <div data-testid="coding-quota-preview" className="w-[320px]">{t('quota.previewUnavailable')}</div>
  }

  const visibleProviders = providers.filter(isVisibleInPreview)
  return (
    <div data-testid="coding-quota-preview" className="w-[320px] space-y-2">
      <div className="font-medium">{t('quota.previewTitle')}</div>
      {visibleProviders.length === 0 ? (
        <p className="text-[var(--text-secondary)]" data-testid="coding-quota-preview-empty">{t('quota.previewEmpty')}</p>
      ) : (
        <div className="space-y-2">
          {visibleProviders.map(provider => (
            <div key={provider.id} data-testid={`coding-quota-preview-provider-${provider.id}`}>
              <div className="flex items-start justify-between gap-3">
                <span className="font-medium">{t(provider.display_name_key)}</span>
                <span className="shrink-0 text-[var(--text-secondary)]">{providerValueSummary(provider, locale, t)}</span>
              </div>
              {provider.quota_strategy_key && (
                <p className="mt-0.5 text-[var(--text-secondary)]">
                  <span className="font-medium text-[var(--text-primary)]">{t('quota.strategyLabel')}:</span>{' '}
                  {t(provider.quota_strategy_key)}
                </p>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

export default function CodingQuotaPreview({ icon, onOpen }: CodingQuotaPreviewProps) {
  const { t } = useI18n()
  const [providers, setProviders] = useState<CodingQuotaProvider[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(false)
  const requestStartedRef = useRef(false)

  const requestPreview = useCallback(() => {
    if (requestStartedRef.current) return
    requestStartedRef.current = true
    setLoading(true)
    setError(false)
    void fetchCodingQuotas()
      .then(response => setProviders(response.providers))
      .catch(() => {
        requestStartedRef.current = false
        setError(true)
      })
      .finally(() => setLoading(false))
  }, [])

  return (
    <InstantTooltip
      content={<PreviewContent providers={providers} loading={loading} error={error} />}
      placement="bottom"
      maxWidth={360}
      className="inline-flex"
    >
      <button
        type="button"
        onClick={onOpen}
        onMouseEnter={requestPreview}
        onFocus={requestPreview}
        aria-label={t('quota.open')}
        title={t('quota.open')}
        className="inline-flex h-7 items-center gap-1 rounded-md px-1.5 text-nav text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--accent-blue)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
        data-testid="global-coding-quota"
      >
        {icon}
        <span>{t('quota.openShort')}</span>
      </button>
    </InstantTooltip>
  )
}
