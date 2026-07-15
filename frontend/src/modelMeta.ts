export interface ModelMeta {
  id: string
  provider: string
  providerKey: string
  label: string
  iconKey: string
}

type ProviderAlias = { provider: string; iconKey: string; providerKey: string }

const PROVIDER_ALIASES: Record<string, { provider: string; iconKey: string; providerKey?: string }> = {
  anthropic: { provider: 'Anthropic', iconKey: 'anthropic' },
  claude: { provider: 'Claude', iconKey: 'claude', providerKey: 'anthropic' },
  openai: { provider: 'OpenAI', iconKey: 'openai' },
  google: { provider: 'Google', iconKey: 'google' },
  gemini: { provider: 'Google', iconKey: 'gemini', providerKey: 'google' },
  xai: { provider: 'xAI', iconKey: 'xai' },
  grok: { provider: 'xAI', iconKey: 'grok', providerKey: 'xai' },
  deepseek: { provider: 'DeepSeek', iconKey: 'deepseek' },
  qwen: { provider: 'Qwen', iconKey: 'qwen' },
  dashscope: { provider: 'Qwen', iconKey: 'qwen' },
  moonshot: { provider: 'Moonshot', iconKey: 'moonshot' },
  kimi: { provider: 'Moonshot', iconKey: 'kimi', providerKey: 'moonshot' },
  mistral: { provider: 'Mistral', iconKey: 'mistral' },
  meta: { provider: 'Meta', iconKey: 'meta' },
  llama: { provider: 'Meta', iconKey: 'meta', providerKey: 'meta' },
  ollama: { provider: 'Ollama', iconKey: 'ollama' },
  openrouter: { provider: 'OpenRouter', iconKey: 'openrouter' },
  azure: { provider: 'Azure AI', iconKey: 'azure' },
  perplexity: { provider: 'Perplexity', iconKey: 'perplexity' },
  cohere: { provider: 'Cohere', iconKey: 'cohere' },
  zhipu: { provider: 'Zhipu', iconKey: 'zhipu' },
  glm: { provider: 'Zhipu', iconKey: 'zhipu' },
  chatglm: { provider: 'Zhipu', iconKey: 'zhipu' },
  minimax: { provider: 'MiniMax', iconKey: 'minimax' },
  doubao: { provider: 'ByteDance', iconKey: 'doubao', providerKey: 'bytedance' },
  bytedance: { provider: 'ByteDance', iconKey: 'bytedance' },
  copilot: { provider: 'GitHub Copilot', iconKey: 'copilot' },
  hy3: { provider: 'HY', iconKey: 'hy', providerKey: 'hy' },
  opencode: { provider: 'OpenCode', iconKey: 'opencode' },
  'opencode-go': { provider: 'OpenCode Go', iconKey: 'opencode' },
}

function titleCaseToken(token: string): string {
  return token
    .split(/[-_\s]+/)
    .filter(Boolean)
    .map(part => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ')
}

function cleanModelName(name: string): string {
  return name
    .replace(/\x1b\[[0-9;?]*[ -/]*[@-~]/g, '')
    .replace(/\[(?:\d{1,3};)*\d{1,3}m\]/g, '')
    .replace(/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/g, '')
    .trim()
}

function aliasFor(text: string): ProviderAlias | null {
  const lower = text.toLowerCase()
  const exact = PROVIDER_ALIASES[lower]
  if (exact) return { ...exact, providerKey: exact.providerKey ?? lower }
  for (const [key, value] of Object.entries(PROVIDER_ALIASES)) {
    if (lower.includes(key)) return { ...value, providerKey: value.providerKey ?? key }
  }
  return null
}

export function modelMeta(modelName: string, modelProvider = ''): ModelMeta {
  const raw = cleanModelName(modelName)
  if (!raw) {
    return { id: '', provider: 'Unknown', providerKey: 'unknown', label: 'unknown model', iconKey: 'unknown' }
  }
  if (raw.toLowerCase() === '<synthetic>') {
    return { id: raw, provider: 'Synthetic', providerKey: 'synthetic', label: 'Synthetic', iconKey: 'synthetic' }
  }

  const recordedProvider = modelProvider.trim()
  if (recordedProvider) {
    const providerAlias = aliasFor(recordedProvider)
    const modelAlias = aliasFor(raw)
    const providerKey = providerAlias?.providerKey ?? recordedProvider.toLowerCase()
    return {
      id: raw,
      provider: providerAlias?.provider ?? titleCaseToken(recordedProvider),
      providerKey,
      label: raw,
      iconKey: modelAlias?.iconKey ?? providerAlias?.iconKey ?? providerKey,
    }
  }

  const slash = raw.indexOf('/')
  if (slash > 0 && slash < raw.length - 1) {
    const providerToken = raw.slice(0, slash)
    const modelToken = raw.slice(slash + 1)
    const providerAlias = aliasFor(providerToken)
    const modelAlias = aliasFor(modelToken)
    const providerKey = providerAlias?.providerKey ?? providerToken.toLowerCase()
    return {
      id: raw,
      provider: providerAlias?.provider ?? titleCaseToken(providerToken),
      providerKey,
      label: modelToken,
      iconKey: modelAlias?.iconKey ?? providerAlias?.iconKey ?? providerKey,
    }
  }

  const fallbackProvider = aliasFor(raw)
  if (fallbackProvider) {
    return {
      id: raw,
      provider: fallbackProvider.provider,
      providerKey: fallbackProvider.providerKey,
      label: raw,
      iconKey: fallbackProvider.iconKey,
    }
  }

  return {
    id: raw,
    provider: 'Unknown',
    providerKey: 'unknown',
    label: raw,
    iconKey: raw.toLowerCase(),
  }
}

export function fallbackModelColor(key: string): string {
  const palette = [
    '#2563eb', '#0891b2', '#059669', '#7c3aed', '#db2777',
    '#dc2626', '#d97706', '#4f46e5', '#0d9488', '#a21caf',
  ]
  let hash = 0
  for (let i = 0; i < key.length; i++) {
    hash = ((hash << 5) - hash) + key.charCodeAt(i)
    hash |= 0
  }
  return palette[Math.abs(hash) % palette.length]
}
