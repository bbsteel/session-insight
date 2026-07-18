import claudeColorIcon from '@lobehub/icons-static-svg/icons/claude-color.svg'
import claudeIcon from '@lobehub/icons-static-svg/icons/claude.svg'
import codexIcon from '@lobehub/icons-static-svg/icons/codex.svg'
import githubCopilotIcon from '@lobehub/icons-static-svg/icons/githubcopilot.svg'
import grokIcon from '@lobehub/icons-static-svg/icons/grok.svg'
import openCodeIcon from '@lobehub/icons-static-svg/icons/opencode.svg'
const chrysIcon = '/icons/chrys-c.png'
import { resolveAgentStyle } from '../agentStyles'

interface AgentIconProps {
  agentType?: string
  size?: number
  className?: string
}

interface IconVariant {
  src: string
  color?: string
}

interface AgentIconPair {
  light: IconVariant
  dark: IconVariant
}

/** Every known agent has an explicit light/dark treatment. */
function resolveIcon(agentType?: string): AgentIconPair | null {
  const normalized = agentType?.toLowerCase() ?? ''
  if (normalized.includes('claude')) return {
    light: { src: claudeColorIcon },
    dark: { src: claudeIcon, color: '#e8a48c' },
  }
  if (normalized.includes('codex')) return {
    light: { src: codexIcon, color: '#111827' },
    dark: { src: codexIcon, color: '#f7f7f8' },
  }
  if (normalized.includes('copilot')) return {
    light: { src: githubCopilotIcon, color: '#24292f' },
    dark: { src: githubCopilotIcon, color: '#f0f6fc' },
  }
  if (normalized.includes('opencode')) return {
    light: { src: openCodeIcon, color: '#111111' },
    dark: { src: openCodeIcon, color: '#ffffff' },
  }
  if (normalized.includes('chrys')) return {
    light: { src: chrysIcon, color: '#7c3aed' },
    dark: { src: chrysIcon, color: '#d8b4fe' },
  }
  if (normalized.includes('grok')) return {
    light: { src: grokIcon, color: '#111111' },
    dark: { src: grokIcon, color: '#ffffff' },
  }
  return null
}

function ThemeIcon({ variant, theme }: { variant: IconVariant; theme: 'light' | 'dark' }) {
  const themeClass = theme === 'light' ? 'dark:hidden' : 'hidden dark:block'

  if (!variant.color) {
    return (
      <img
        src={variant.src}
        alt=""
        className={`block w-full h-full object-contain ${themeClass}`}
        draggable={false}
        data-agent-icon-theme={theme}
      />
    )
  }

  return (
    <span
      className={`w-full h-full ${themeClass}`}
      style={{
        backgroundColor: variant.color,
        WebkitMaskImage: `url("${variant.src}")`,
        maskImage: `url("${variant.src}")`,
        WebkitMaskPosition: 'center',
        maskPosition: 'center',
        WebkitMaskRepeat: 'no-repeat',
        maskRepeat: 'no-repeat',
        WebkitMaskSize: 'contain',
        maskSize: 'contain',
      }}
      data-agent-icon-theme={theme}
    />
  )
}

export default function AgentIcon({ agentType, size = 20, className = '' }: AgentIconProps) {
  const icon = resolveIcon(agentType)

  if (icon) {
    return (
      <span
        className={`inline-flex items-center justify-center flex-shrink-0 ${className}`}
        style={{ width: size, height: size }}
        aria-hidden="true"
      >
        <ThemeIcon variant={icon.light} theme="light" />
        <ThemeIcon variant={icon.dark} theme="dark" />
      </span>
    )
  }

  const accent = resolveAgentStyle(agentType)?.accent ?? '#6b7280'
  return (
    <span
      className={`inline-flex items-center justify-center rounded-md flex-shrink-0 text-white ${className}`}
      style={{ width: size, height: size, backgroundColor: accent }}
      aria-hidden="true"
    >
      <svg
        width={Math.max(10, Math.round(size * 0.58))}
        height={Math.max(10, Math.round(size * 0.58))}
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        <rect x="5" y="7" width="14" height="12" rx="3" />
        <path d="M9 7V5a3 3 0 0 1 6 0v2" />
        <circle cx="9" cy="13" r="1" fill="currentColor" stroke="none" />
        <circle cx="15" cy="13" r="1" fill="currentColor" stroke="none" />
        <path d="M9 16h6" />
      </svg>
    </span>
  )
}
