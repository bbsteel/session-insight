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

export function PinIcon({ filled, className, ...rest }: IconProps & { filled?: boolean }) {
  if (filled) {
    return (
      <svg
        className={className ?? 'h-3.5 w-3.5 shrink-0'}
        viewBox="0 0 16 16"
        aria-hidden
        {...rest}
      >
        <path
          fill="currentColor"
          d="M10.2 2.4 8.7 3.9l3.4 3.4 1.5-1.5a1.2 1.2 0 0 0 0-1.7l-1.7-1.7a1.2 1.2 0 0 0-1.7 0ZM8.2 4.4 3.5 9.1a1 1 0 0 0-.3.7v1.5h1.5a1 1 0 0 0 .7-.3l4.7-4.7-1.9-1.9ZM4.2 12.2 2.8 13.6"
        />
      </svg>
    )
  }
  return (
    <Icon className={className} {...rest}>
      <path d="m10.1 2.5 3.4 3.4-1.4 1.4-3.4-3.4z" />
      <path d="M8.2 4.4 3.5 9.1a1 1 0 0 0-.3.7v1.5h1.5a1 1 0 0 0 .7-.3l4.7-4.7" />
      <path d="m4.2 12.2-1.4 1.4" />
    </Icon>
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
