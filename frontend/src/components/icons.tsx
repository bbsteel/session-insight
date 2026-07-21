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
 * Person bust: head circle + shoulders arc. Default user-message marker in
 * the interaction panel (replaced by the custom avatar when one is set).
 */
export function UserIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <circle cx="8" cy="5.5" r="2.5" />
      <path d="M3.5 13.5c0-2.5 2-4 4.5-4s4.5 1.5 4.5 4" />
    </Icon>
  )
}

/**
 * Bold geometric push-pin: diamond head (hollow via stroke) + needle.
 * Unpinned is diagonal (reference pose); pinned is the same shape, vertical.
 * Uses stroke (not a filled evenodd blob) so it stays legible at ~14px.
 */
export function PinIcon({ filled, className, ...rest }: IconProps & { filled?: boolean }) {
  const cls = className ?? 'h-3.5 w-3.5 shrink-0'
  const rot = filled ? 0 : -40
  return (
    <svg
      className={cls}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2.6}
      strokeLinejoin="miter"
      strokeMiterlimit={2}
      aria-hidden
      {...rest}
    >
      <g transform={`rotate(${rot} 12 12)`}>
        {/* Angular head ring — white center is the hole */}
        <path d="M12 2.2 19.2 9.4 12 16.6 4.8 9.4Z" />
        {/* Needle */}
        <path d="M12 16.6V21.8" strokeLinecap="square" />
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

export function AppearanceIcon({ className, ...rest }: IconProps) {
  return (
    <Icon className={className ?? 'h-4 w-4 shrink-0'} {...rest}>
      <circle cx="6" cy="6" r="2.5" />
      <path d="M9.5 3.5a3 3 0 0 1 0 5M12 9.5a3 3 0 0 1 0-5" />
      <path d="M11.5 12.5a3 3 0 0 0 2-2" />
    </Icon>
  )
}

export function NavigationIcon({ className, ...rest }: IconProps) {
  return (
    <Icon className={className ?? 'h-4 w-4 shrink-0'} {...rest}>
      <circle cx="8" cy="8" r="5.5" />
      <path d="m11 5-5 2.5 2.5 2.5L11 5Z" />
    </Icon>
  )
}

export function SearchIcon({ className, ...rest }: IconProps) {
  return (
    <Icon className={className ?? 'h-4 w-4 shrink-0'} {...rest}>
      <circle cx="7" cy="7" r="4.5" />
      <path d="M10.5 10.5 14 14" />
    </Icon>
  )
}

export function TerminalIcon({ className, ...rest }: IconProps) {
  return (
    <Icon className={className ?? 'h-4 w-4 shrink-0'} {...rest}>
      <rect x="1.5" y="2.5" width="13" height="11" rx="1.5" />
      <path d="M3.5 6.5 6 8 3.5 9.5M7.5 9.5h3" />
    </Icon>
  )
}

export function EditorIcon({ className, ...rest }: IconProps) {
  return (
    <Icon className={className ?? 'h-4 w-4 shrink-0'} {...rest}>
      <path d="M4 3 1 8l3 5M12 3l3 5-3 5" />
    </Icon>
  )
}

export function SparklesIcon({ className, ...rest }: IconProps) {
  return (
    <Icon className={className ?? 'h-4 w-4 shrink-0'} {...rest}>
      <path d="M8 1v3M8 12v3M3 8h3M10 8h3M4.5 4.5l2 2M9.5 9.5l2 2M4.5 11.5l2-2M9.5 2.5l2 2" />
    </Icon>
  )
}

export function FontIcon({ className, ...rest }: IconProps) {
  return (
    <Icon className={className ?? 'h-4 w-4 shrink-0'} {...rest}>
      <path d="M4 13.5V3.5h1M4 3.5 2.5 2M4 3.5 5.5 2M8 13.5v-10M8 3.5 6.5 2M8 3.5 9.5 2M12 13.5V3.5h1M12 3.5 10.5 2M12 3.5 13.5 2" />
    </Icon>
  )
}

/** Globe / language icon. */
export function GlobeIcon({ className, ...rest }: IconProps) {
  return (
    <Icon className={className ?? 'h-3.5 w-3.5 shrink-0'} {...rest}>
      <circle cx="8" cy="8" r="6" />
      <ellipse cx="8" cy="8" rx="3" ry="6" />
      <path d="M2 8h12" />
    </Icon>
  )
}

export function InfoIcon({ className, ...rest }: IconProps) {
  return (
    <Icon className={className ?? 'h-4 w-4 shrink-0'} {...rest}>
      <circle cx="8" cy="8" r="6.5" />
      <path d="M8 7.2v4" />
      <circle cx="8" cy="4.6" r="0.4" fill="currentColor" stroke="none" />
    </Icon>
  )
}

