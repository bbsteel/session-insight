interface BadgeProps {
  label: string
  value: string
  intent?: 'default' | 'warning' | 'error'
}

const intentColors: Record<string, string> = {
  default: 'bg-[var(--bg-inset)] text-[var(--text-secondary)]',
  warning: 'bg-[var(--warning)]/10 text-[var(--warning)]',
  error: 'bg-[var(--error)]/10 text-[var(--error)]',
}

export default function Badge({ label, value, intent = 'default' }: BadgeProps) {
  return (
    <span className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded-sm text-meta whitespace-nowrap flex-shrink-0 ${intentColors[intent]}`}>
      <span className="font-medium">{value}</span>
      <span className="opacity-60">{label}</span>
    </span>
  )
}
