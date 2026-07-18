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
 * Classic push-pin (thumbtack): round head with center dimple, short body, needle.
 * Visual reference: common push-pin iconography (e.g. flaticon “push-pin” style).
 * Paths are original geometry — not a copy of any commercial asset file.
 */
export function PinIcon({ filled, className, ...rest }: IconProps & { filled?: boolean }) {
  const cls = className ?? 'h-3.5 w-3.5 shrink-0'
  if (filled) {
    return (
      <svg className={cls} viewBox="0 0 16 16" aria-hidden {...rest}>
        <path
          fill="currentColor"
          fillRule="evenodd"
          d="M8 1c-2.2 0-4 1.68-4 3.75 0 1.35.72 2.53 1.8 3.2L5.1 10.7c-.18.32.05.8.45.8h4.9c.4 0 .63-.48.45-.8l-.7-2.75c1.08-.67 1.8-1.85 1.8-3.2C12 2.68 10.2 1 8 1Zm0 1.55a2.2 2.2 0 1 1 0 4.4 2.2 2.2 0 0 1 0-4.4ZM7.3 11.5h1.4v2.85a.7.7 0 0 1-1.4 0V11.5Z"
        />
      </svg>
    )
  }
  return (
    <svg
      className={cls}
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.5}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
      {...rest}
    >
      {/* Outer head */}
      <circle cx="8" cy="4.75" r="3.1" />
      {/* Center dimple (push-pin head button) */}
      <circle cx="8" cy="4.75" r="1.15" />
      {/* Body shoulders */}
      <path d="M5.55 7.55 5.15 10.5h5.7l-.4-2.95" />
      {/* Needle */}
      <path d="M8 10.5v3.85" />
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
