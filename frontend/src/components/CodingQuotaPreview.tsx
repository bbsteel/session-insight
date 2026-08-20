import { useCallback, useRef, useState, type ReactNode } from 'react'
import { fetchCodingQuotas, type CodingQuotaProvider } from '../api'
import { useI18n } from '../i18n'
import InstantTooltip from './InstantTooltip'
import { remainingToneClass, remainingValue, resetValue, windowLabel } from './quotaPresentation'

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
          {visibleProviders.map(provider => {
            const windows = provider.snapshot.windows ?? []
            return (
              <div key={provider.id} className="border-t border-[var(--border-default)] pt-1.5 first:border-t-0 first:pt-0" data-testid={`coding-quota-preview-provider-${provider.id}`}>
                <div className="flex items-center justify-between gap-3">
                  <span className="font-semibold">{t(provider.display_name_key)}</span>
                  {windows.length === 0 && <span className="text-meta text-[var(--text-secondary)]">{t(`quota.status.${providerStatusKey(provider)}`)}</span>}
                </div>
                {windows.length > 0 ? (
                  <div className="mt-1 space-y-1">
                    {windows.slice(0, 3).map(window => (
                      <div key={`${provider.id}-${window.id}`} className="grid grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-3 gap-y-0.5" data-testid={`coding-quota-preview-window-${provider.id}-${window.id}`}>
                        <span className="truncate text-meta text-[var(--text-secondary)]">{windowLabel(window.id, t)}</span>
                        <span
                          className={`text-title font-bold tabular-nums ${remainingToneClass(window)}`}
                          data-testid={`coding-quota-preview-remaining-${provider.id}-${window.id}`}
                        >
                          {remainingValue(window, locale, t)}
                        </span>
                        {window.reset_at && (
                          <span
                            className="col-span-2 text-right text-meta font-semibold text-[var(--accent-blue)]"
                            data-testid={`coding-quota-preview-reset-${provider.id}-${window.id}`}
                          >
                            {resetValue(window, t)}
                          </span>
                        )}
                      </div>
                    ))}
                  </div>
                ) : null}
              </div>
            )
          })}
        </div>
      )}
      <p className="border-t border-[var(--border-default)] pt-1.5 text-meta text-[var(--accent-blue)]">
        {t('quota.previewClickHint')}
      </p>
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
        className="inline-flex h-7 items-center gap-1 rounded-md px-1.5 text-nav text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--accent-blue)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
        data-testid="global-coding-quota"
      >
        {icon}
        <span>{t('quota.openShort')}</span>
      </button>
    </InstantTooltip>
  )
}
