export type AgentKey = string

export interface AgentStyle {
  accent: string
  userPrefix: string
  assistantPrefix: string
}

const STYLES: Record<AgentKey, AgentStyle> = {
  claude:  { accent: '#2563eb', userPrefix: '>',  assistantPrefix: '●' },
  codex:   { accent: '#059669', userPrefix: '▸',  assistantPrefix: '◆' },
  copilot: { accent: '#7c3aed', userPrefix: '»',  assistantPrefix: '◉' },
}

// Deterministic palette for unknown agents (10 colors, wraps via char-code sum mod 10)
const FALLBACK_ACCENTS = [
  '#f59e0b', '#dc2626', '#0891b2', '#a21caf', '#0d9488',
  '#0284c7', '#d97706', '#db2777', '#4f46e5', '#16a34a',
]

function fallbackAccent(agentType: string): string {
  let hash = 0
  for (let i = 0; i < agentType.length; i++) {
    hash = ((hash << 5) - hash) + agentType.charCodeAt(i)
    hash |= 0
  }
  return FALLBACK_ACCENTS[Math.abs(hash) % FALLBACK_ACCENTS.length]
}

export function resolveAgentStyle(agentType?: AgentKey): AgentStyle | null {
  if (!agentType) return null
  const lower = agentType.toLowerCase()
  if (STYLES[lower]) return STYLES[lower]
  // Check if any known key is contained in the agent type string
  for (const key of Object.keys(STYLES)) {
    if (lower.includes(key)) return STYLES[key]
  }
  // Fallback for unknown agents: generated accent, generic prefixes
  return {
    accent: fallbackAccent(lower),
    userPrefix: '▸',
    assistantPrefix: '◆',
  }
}
