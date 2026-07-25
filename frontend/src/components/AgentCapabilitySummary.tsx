import type { CapabilitySummary } from '../capabilityPresentation'
import { useI18n } from '../i18n'

interface Props {
  summary: CapabilitySummary
  className?: string
}

/** Compact neutral summary chips — never a bright all-success banner or score. */
export default function AgentCapabilitySummary({ summary, className = '' }: Props) {
  const { t } = useI18n()
  if (!summary.hasWarning) {
    return (
      <span className={`text-meta text-[var(--text-muted)] ${className}`}>
        {t('capability.summary.calm')}
      </span>
    )
  }
  const parts: string[] = []
  if (summary.missing > 0) parts.push(t('capability.summary.missing', { n: summary.missing }))
  if (summary.estimated > 0) parts.push(t('capability.summary.estimated', { n: summary.estimated }))
  if (summary.unsupported > 0) parts.push(t('capability.summary.unsupported', { n: summary.unsupported }))
  return (
    <span className={`text-meta text-[var(--warning)] ${className}`} role="status">
      {parts.join(' · ')}
    </span>
  )
}
