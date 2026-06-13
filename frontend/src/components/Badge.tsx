interface BadgeProps {
  label: string
  value: string
  intent?: 'default' | 'accent' | 'success' | 'warning' | 'error'
}

const intentColors: Record<string, string> = {
  default: 'bg-[var(--bg-inset)] text-[var(--text-secondary)]',
  accent: 'bg-[var(--accent-blue)]/10 text-[var(--accent-blue)]',
  success: 'bg-[var(--success)]/10 text-[var(--success)]',
  warning: 'bg-[var(--warning)]/10 text-[var(--warning)]',
  error: 'bg-[var(--error)]/10 text-[var(--error)]',
}

export default function Badge({ label, value, intent = 'default' }: BadgeProps) {
  return (
    <span role="listitem" className={`inline-flex h-5 items-center gap-1 px-1.5 rounded-sm text-helper whitespace-nowrap flex-shrink-0 ${intentColors[intent]}`}>
      <span className="font-medium">{value}</span>
      <span className="opacity-60">{label}</span>
    </span>
  )
}
