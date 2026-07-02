import claudeIcon from '@lobehub/icons-static-svg/icons/claude-color.svg'
import githubCopilotIcon from '@lobehub/icons-static-svg/icons/githubcopilot.svg'
import openAIIcon from '@lobehub/icons-static-svg/icons/openai.svg'
const openCodeIcon = '/icons/opencode-logo-light-square.png'
import { resolveAgentStyle } from '../agentStyles'

interface AgentIconProps {
  agentType?: string
  size?: number
  className?: string
}

function resolveIcon(agentType?: string): { src: string; colored: boolean } | null {
  const normalized = agentType?.toLowerCase() ?? ''
  if (normalized.includes('claude')) return { src: claudeIcon, colored: true }
  if (normalized.includes('codex')) return { src: openAIIcon, colored: false }
  if (normalized.includes('copilot')) return { src: githubCopilotIcon, colored: false }
  if (normalized.includes('opencode')) return { src: openCodeIcon, colored: true }
  return null
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
        {icon.colored ? (
          <img src={icon.src} alt="" className="block w-full h-full object-contain" />
        ) : (
          <span
            className="block w-full h-full bg-[var(--text-primary)]"
            style={{
              WebkitMaskImage: `url("${icon.src}")`,
              maskImage: `url("${icon.src}")`,
              WebkitMaskPosition: 'center',
              maskPosition: 'center',
              WebkitMaskRepeat: 'no-repeat',
              maskRepeat: 'no-repeat',
              WebkitMaskSize: 'contain',
              maskSize: 'contain',
            }}
          />
        )}
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
