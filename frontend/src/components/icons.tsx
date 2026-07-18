// Small UI icons: stroke SVGs using currentColor so they match text-nav chrome.

import type { ReactNode, SVGProps } from 'react'

type IconProps = SVGProps<SVGSVGElement>

const strokeProps = {
  viewBox: '0 0 16 16',
  fill: 'none' as const,
  stroke: 'currentColor',
  strokeWidth: 1.5,
  strokeLinecap: 'round' as const,
  strokeLinejoin: 'round' as const,
  'aria-hidden': true as const,
}

function Icon({ className, children, ...rest }: IconProps & { children: ReactNode }) {
  return (
    <svg className={className ?? 'h-3.5 w-3.5 shrink-0'} {...strokeProps} {...rest}>
      {children}
    </svg>
  )
}

/**
 * Geometric push-pin: bold angular head with hollow triangle, thick shaft.
 * Same path for both states — unpinned tilted (ref pose), pinned vertical (needle down).
 */
export function PinIcon({ filled, className, ...rest }: IconProps & { filled?: boolean }) {
  const cls = className ?? 'h-3.5 w-3.5 shrink-0'
  // Drawn upright (needle down). Unpinned: diagonal like the reference image.
  const rot = filled ? 0 : -40
  return (
    <svg className={cls} viewBox="0 0 16 16" aria-hidden {...rest}>
      <g transform={`rotate(${rot} 8 8)`}>
        {/*
          Bold mitered silhouette (head → shoulders → shaft → tip) with a
          triangular cutout pointing toward the tip — same family as the
          product reference, original coordinates.
        */}
        <path
          fill="currentColor"
          fillRule="evenodd"
          d={
            'M8 .9 13.1 6l-2.15 2.15 2.35 2.35-3.05 3.05-.05.05V15.1H5.8v-1.55L2.75 10.5l2.35-2.35L2.95 6 8 .9Zm0 3.55L6.05 6.4 8 8.35 9.95 6.4 8 4.45Z'
          }
        />
      </g>
    </svg>
  )
}

/** Expand panel width (point outward). */
export function WidenIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <path d="M2 8h5M9 8h5" />
      <path d="M4.5 5.5 2 8l2.5 2.5M11.5 5.5 14 8l-2.5 2.5" />
    </Icon>
  )
}

/** Restore standard panel width (point inward). */
export function NarrowIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <path d="M2 8h5M9 8h5" />
      <path d="M5.5 5.5 8 8 5.5 10.5M10.5 5.5 8 8l2.5 2.5" />
    </Icon>
  )
}

export function ExpandAllIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <path d="M3 5h10M3 8h10M3 11h10" />
      <path d="M12 3.5 13.5 5 12 6.5M12 9.5 13.5 11 12 12.5" />
    </Icon>
  )
}

export function CollapseAllIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <path d="M3 5h10M3 8h10M3 11h10" />
      <path d="M14 3.5 12.5 5 14 6.5M14 9.5 12.5 11 14 12.5" />
    </Icon>
  )
}

export function CloseIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <path d="M4 4l8 8M12 4l-8 8" />
    </Icon>
  )
}

export function SunIcon({ className, ...rest }: IconProps) {
  return (
    <Icon className={className ?? 'h-3.5 w-3.5 shrink-0'} {...rest}>
      <circle cx="8" cy="8" r="2.5" />
      <path d="M8 1.5v1.5M8 13v1.5M1.5 8H3M13 8h1.5M3.2 3.2l1.1 1.1M11.7 11.7l1.1 1.1M3.2 12.8l1.1-1.1M11.7 4.3l1.1-1.1" />
    </Icon>
  )
}

export function MoonIcon({ className, ...rest }: IconProps) {
  return (
    <Icon className={className ?? 'h-3.5 w-3.5 shrink-0'} {...rest}>
      <path d="M12.5 9.2A4.8 4.8 0 0 1 6.8 3.5 4.8 4.8 0 1 0 12.5 9.2Z" />
    </Icon>
  )
}
