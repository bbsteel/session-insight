import InstantTooltip from './InstantTooltip'
import {
  capabilityStateSymbol,
  capabilityStateTone,
  capabilityStateLabelKey,
  reasonCodeLabelKey,
} from '../capabilityPresentation'
import { useI18n } from '../i18n'

interface Props {
  state: string | undefined
  reasonCode?: string
  /** Show label text next to the symbol. */
  showLabel?: boolean
  className?: string
  /** Include raw machine id for unknown reasons in the tooltip. */
  showRawReasonInTooltip?: boolean
}

const toneClass: Record<string, string> = {
  neutral: 'text-[var(--text-secondary)]',
  info: 'text-[var(--accent-blue)]',
  warning: 'text-[var(--warning)]',
  muted: 'text-[var(--text-muted)]',
  danger: 'text-[var(--error)]',
}

export default function CapabilityStateIndicator({
  state,
  reasonCode,
  showLabel = false,
  className = '',
  showRawReasonInTooltip = true,
}: Props) {
  const { t } = useI18n()
  const symbol = capabilityStateSymbol(state)
  const tone = capabilityStateTone(state)
  const label = t(capabilityStateLabelKey(state))
  const reasonKey = reasonCodeLabelKey(reasonCode)
  let tip = label
  if (reasonKey) {
    // unknown reason already interpolates {code}; do not append the code again.
    const reasonText =
      reasonKey === 'capability.reason.unknown' && reasonCode
        ? t(reasonKey, { code: reasonCode })
        : t(reasonKey)
    tip = `${label}: ${reasonText}`
  } else if (reasonCode && showRawReasonInTooltip) {
    tip = `${label} (${reasonCode})`
  }

  return (
    <InstantTooltip text={tip} placement="top">
      <span
        className={`inline-flex items-center gap-1 tabular-nums ${toneClass[tone] ?? toneClass.muted} ${className}`}
        aria-label={tip}
      >
        <span aria-hidden="true" className="font-medium leading-none">
          {symbol}
        </span>
        {showLabel && <span className="text-meta">{label}</span>}
      </span>
    </InstantTooltip>
  )
}
