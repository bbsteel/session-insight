interface StarIconProps {
  size?: number
  /** Filled solid star vs outline only. */
  filled?: boolean
  className?: string
  strokeWidth?: number
}

/** Shared five-point star used for bookmark affordances. */
export default function StarIcon({
  size = 16,
  filled = false,
  className = '',
  strokeWidth = 2,
}: StarIconProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill={filled ? 'currentColor' : 'none'}
      stroke="currentColor"
      strokeWidth={strokeWidth}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden="true"
    >
      <polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2" />
    </svg>
  )
}
