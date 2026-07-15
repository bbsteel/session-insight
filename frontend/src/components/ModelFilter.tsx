import { useEffect, useRef, useState } from 'react'
import anthropicIcon from '@lobehub/icons-static-svg/icons/anthropic.svg'
import azureIcon from '@lobehub/icons-static-svg/icons/azureai-color.svg'
import bytedanceIcon from '@lobehub/icons-static-svg/icons/bytedance-color.svg'
import claudeIcon from '@lobehub/icons-static-svg/icons/claude-color.svg'
import cohereIcon from '@lobehub/icons-static-svg/icons/cohere-color.svg'
import copilotIcon from '@lobehub/icons-static-svg/icons/githubcopilot.svg'
import deepseekIcon from '@lobehub/icons-static-svg/icons/deepseek-color.svg'
import doubaoIcon from '@lobehub/icons-static-svg/icons/doubao-color.svg'
import geminiIcon from '@lobehub/icons-static-svg/icons/gemini-color.svg'
import googleIcon from '@lobehub/icons-static-svg/icons/google-color.svg'
import grokIcon from '@lobehub/icons-static-svg/icons/grok.svg'
import kimiIcon from '@lobehub/icons-static-svg/icons/kimi-color.svg'
import metaIcon from '@lobehub/icons-static-svg/icons/metaai-color.svg'
import minimaxIcon from '@lobehub/icons-static-svg/icons/minimax-color.svg'
import mistralIcon from '@lobehub/icons-static-svg/icons/mistral-color.svg'
import moonshotIcon from '@lobehub/icons-static-svg/icons/moonshot.svg'
import ollamaIcon from '@lobehub/icons-static-svg/icons/ollama.svg'
import openAIIcon from '@lobehub/icons-static-svg/icons/openai.svg'
import openRouterIcon from '@lobehub/icons-static-svg/icons/openrouter.svg'
import perplexityIcon from '@lobehub/icons-static-svg/icons/perplexity-color.svg'
import qwenIcon from '@lobehub/icons-static-svg/icons/qwen-color.svg'
import xaiIcon from '@lobehub/icons-static-svg/icons/xai.svg'
import zhipuIcon from '@lobehub/icons-static-svg/icons/zhipu-color.svg'
import { fallbackModelColor, modelMeta, type ModelMeta } from '../modelMeta'
import { AllAgentsIcon } from './AgentFilter'

const openCodeIcon = '/icons/opencode-logo-light-square.png'
const hyIcon = '/icons/hy.webp'

export interface ModelEntry {
  key: string
  id: string
  name: string
  provider: string
  providerKey: string
  providerSummary: string
  iconKey: string
  label: string
  session_count: number
  providers: ModelProviderEntry[]
}

export interface ModelProviderEntry {
  key: string
  provider: string
  providerKey: string
  session_count: number
}

interface ModelFilterProps {
  models: ModelEntry[]
  selected: string
  onSelect: (model: string) => void
}

const MODEL_ICONS: Record<string, string> = {
  anthropic: anthropicIcon,
  azure: azureIcon,
  bytedance: bytedanceIcon,
  claude: claudeIcon,
  cohere: cohereIcon,
  copilot: copilotIcon,
  deepseek: deepseekIcon,
  doubao: doubaoIcon,
  gemini: geminiIcon,
  google: googleIcon,
  grok: grokIcon,
  hy: hyIcon,
  kimi: kimiIcon,
  meta: metaIcon,
  minimax: minimaxIcon,
  mistral: mistralIcon,
  moonshot: moonshotIcon,
  ollama: ollamaIcon,
  opencode: openCodeIcon,
  openai: openAIIcon,
  openrouter: openRouterIcon,
  perplexity: perplexityIcon,
  qwen: qwenIcon,
  xai: xaiIcon,
  zhipu: zhipuIcon,
}

const ICON_BACKPLATES: Record<string, string> = {
  kimi: '#1783ff',
}

function ModelIcon({ meta, size = 16 }: { meta: Pick<ModelMeta, 'id' | 'iconKey' | 'provider' | 'label'>; size?: number }) {
  if (meta.iconKey === 'all-models') {
    return (
      <span className="inline-flex items-center justify-center flex-shrink-0" style={{ width: size, height: size }} aria-hidden="true">
        <AllAgentsIcon size={size} />
      </span>
    )
  }

  const icon = MODEL_ICONS[meta.iconKey]
  if (icon) {
    const backplate = ICON_BACKPLATES[meta.iconKey]
    return (
      <span
        className="inline-flex items-center justify-center flex-shrink-0 overflow-hidden"
        style={{
          width: size,
          height: size,
          borderRadius: backplate ? Math.max(4, Math.round(size * 0.22)) : undefined,
          backgroundColor: backplate,
        }}
        aria-hidden="true"
      >
        <img
          src={icon}
          alt=""
          className="block object-contain"
          style={{ width: backplate ? Math.round(size * 0.78) : size, height: backplate ? Math.round(size * 0.78) : size }}
        />
      </span>
    )
  }

  const initial = (meta.provider !== 'Unknown' ? meta.provider : meta.label || meta.id).trim().charAt(0).toUpperCase() || '?'
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      aria-hidden="true"
    >
      <rect width="24" height="24" rx="6" fill={fallbackModelColor(meta.id || meta.iconKey)} />
      <text x="12" y="16" textAnchor="middle" fontSize="12" fontWeight="700" fill="white">{initial}</text>
    </svg>
  )
}

export default function ModelFilter({ models, selected, onSelect }: ModelFilterProps) {
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [expandedKey, setExpandedKey] = useState<string | null>(null)
  const [expandedRect, setExpandedRect] = useState<{ left: number; top: number } | null>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)

  const total = models.reduce((n, m) => n + m.session_count, 0)
  const selectedModel = selected
    ? models.find(m => m.key === selected || m.providers.some(p => p.key === selected))
    : undefined
  const selectedProvider = selectedModel?.providers.find(p => p.key === selected)
  const selectedMeta = selectedModel ?? modelMeta(selected)
  const label = selectedModel?.label ?? selectedMeta.label
  const providerLabel = selectedProvider?.provider ?? (selected ? selectedModel?.providerSummary ?? selectedMeta.provider : 'All Providers')
  const count = selectedProvider?.session_count ?? selectedModel?.session_count ?? total

  useEffect(() => {
    if (!open) {
      setSearch('')
      setExpandedKey(null)
      setExpandedRect(null)
      return
    }
    setTimeout(() => searchRef.current?.focus(), 0)
    const onClickOutside = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    const onEscape = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onClickOutside)
    window.addEventListener('keydown', onEscape)
    return () => {
      document.removeEventListener('mousedown', onClickOutside)
      window.removeEventListener('keydown', onEscape)
    }
  }, [open])

  const pick = (name: string) => {
    onSelect(name)
    setOpen(false)
  }

  const visible = search.trim()
    ? models.map(model => {
      const q = search.toLowerCase()
      const modelMatches =
        model.name.toLowerCase().includes(q) ||
        model.label.toLowerCase().includes(q) ||
        model.provider.toLowerCase().includes(q) ||
        model.providerSummary.toLowerCase().includes(q)
      const providers = modelMatches
        ? model.providers
        : model.providers.filter(p => p.provider.toLowerCase().includes(q) || p.providerKey.toLowerCase().includes(q))
      return modelMatches || providers.length > 0 ? { ...model, providers } : null
    }).filter((model): model is ModelEntry => model !== null)
    : models
  const sorted = [...visible].sort((a, b) => {
    if (a.id === 'Other' && b.id !== 'Other') return 1
    if (b.id === 'Other' && a.id !== 'Other') return -1
    return a.id.localeCompare(b.id)
  })

  if (models.length === 0) return null

  return (
    <div className="px-4 pb-2 flex-shrink-0">
      <div ref={containerRef} className="relative">
        <button
          type="button"
          onClick={() => setOpen(v => !v)}
          aria-expanded={open}
          aria-haspopup="listbox"
          className="w-full h-10 px-2.5 rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] text-body text-[var(--text-primary)] flex items-center gap-2 transition-colors duration-fast hover:bg-[var(--bg-surface-hover)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
        >
          <span className="flex-shrink-0">
            <ModelIcon meta={selected ? selectedMeta : { id: 'all-models', provider: 'All Providers', label: 'All Models', iconKey: 'all-models' }} size={18} />
          </span>
          <span className="min-w-0 flex-1 text-left leading-tight">
            <span className="block truncate">{selected ? label : 'All Models'}</span>
            <span className="block truncate text-helper text-[var(--text-muted)]">{providerLabel}</span>
          </span>
          <span className="text-helper text-[var(--text-muted)] flex-shrink-0 tabular-nums">
            {count}
          </span>
          <svg
            className={`w-3.5 h-3.5 text-[var(--text-muted)] flex-shrink-0 transition-transform duration-fast ${open ? 'rotate-180' : ''}`}
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
          >
            <polyline points="6 9 12 15 18 9" />
          </svg>
        </button>

        {open && (
          <div
            role="listbox"
            aria-label="按模型筛选会话"
            className="absolute top-full mt-1 left-0 right-0 z-[var(--z-dropdown)] rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] shadow-lg"
          >
            <div className="p-1.5 border-b border-[var(--border-default)]">
              <div className="relative">
                <svg className="absolute left-2 top-1/2 -translate-y-1/2 w-3 h-3 text-[var(--text-muted)] pointer-events-none" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <circle cx="11" cy="11" r="8" /><line x1="21" y1="21" x2="16.65" y2="16.65" />
                </svg>
                <input
                  ref={searchRef}
                  type="text"
                  placeholder="搜索模型..."
                  value={search}
                  onChange={e => setSearch(e.target.value)}
                  className="w-full h-7 rounded border border-[var(--border-default)] bg-[var(--bg-inset)] pl-6 pr-2 text-helper text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none focus:ring-1 focus:ring-[var(--accent-blue)]/30"
                />
              </div>
            </div>

            <div className="max-h-[28rem] overflow-y-auto py-1">
              {!search.trim() && (
                <button
                  type="button"
                  role="option"
                  aria-selected={selected === ''}
                  onClick={() => pick('')}
                  className={`w-full px-2.5 py-2 flex items-center gap-2 text-left transition-colors duration-fast ${
                    selected === '' ? 'bg-[var(--bg-surface-hover)]' : 'hover:bg-[var(--bg-surface-hover)]'
                  }`}
                >
                  <span className="text-[var(--text-muted)] flex-shrink-0">
                    <ModelIcon meta={{ id: 'all-models', provider: 'All Providers', label: 'All Models', iconKey: 'all-models' }} size={18} />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-body text-[var(--text-primary)]">All Models</span>
                    <span className="block truncate text-helper text-[var(--text-muted)]">All Providers</span>
                  </span>
                  <span className="ml-auto text-helper text-[var(--text-muted)] flex-shrink-0 tabular-nums">{total}</span>
                </button>
              )}

              {sorted.map(model => {
                const isExpanded = expandedKey === model.key
                return (
                  <div key={model.key} className="relative">
                    <button
                      type="button"
                      role="option"
                      aria-selected={selected === model.key || model.providers.some(p => p.key === selected)}
                      onClick={e => {
                        const rect = (e.currentTarget as HTMLElement).getBoundingClientRect()
                        setExpandedRect({ left: rect.right + 4, top: rect.top })
                        setExpandedKey(isExpanded ? null : model.key)
                      }}
                      className={`w-full px-2.5 py-2 flex items-center gap-2 text-left transition-colors duration-fast ${
                        isExpanded ? 'bg-[var(--bg-surface-hover)]' : 'hover:bg-[var(--bg-surface-hover)]'
                      }`}
                    >
                      <span className="flex-shrink-0"><ModelIcon meta={model} size={20} /></span>
                      <span className="min-w-0 flex-1" title={`${model.providerSummary} / ${model.label}`}>
                        <span className="block truncate text-body text-[var(--text-primary)]">{model.label}</span>
                        <span className="block truncate text-helper text-[var(--text-muted)]">{model.providerSummary}</span>
                      </span>
                      <span className="ml-auto text-helper text-[var(--text-muted)] flex-shrink-0 tabular-nums">{model.session_count}</span>
                      <svg
                        className={`w-3 h-3 text-[var(--text-muted)] flex-shrink-0 transition-transform duration-fast ${isExpanded ? 'rotate-90' : ''}`}
                        viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"
                        aria-hidden="true"
                      >
                        <polyline points="9 6 15 12 9 18" />
                      </svg>
                    </button>
                  </div>
                )
              })}

              {visible.length === 0 && (
                <div className="px-2.5 py-3 text-center text-helper text-[var(--text-muted)]">无匹配模型</div>
              )}
            </div>
          </div>
        )}

        {open && expandedKey && expandedRect && (() => {
          const model = models.find(m => m.key === expandedKey)
          if (!model) return null
          return (
            <div
              style={{ position: 'fixed', left: expandedRect.left, top: expandedRect.top }}
              className="min-w-44 rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] shadow-lg z-[var(--z-dropdown)] py-1"
              onMouseLeave={() => { setExpandedKey(null); setExpandedRect(null) }}
            >
              {model.providers.length > 1 && (
                <button
                  type="button"
                  role="option"
                  aria-selected={selected === model.key}
                  onClick={() => pick(model.key)}
                  className={`w-full px-2.5 py-1.5 flex items-center gap-2 text-left transition-colors duration-fast ${
                    selected === model.key ? 'bg-[var(--bg-surface-hover)]' : 'hover:bg-[var(--bg-surface-hover)]'
                  }`}
                >
                  <span className="w-2 h-2 rounded-full bg-[var(--accent-blue)] flex-shrink-0" aria-hidden="true" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-helper text-[var(--text-primary)]">All Providers</span>
                  </span>
                  <span className="ml-auto text-helper text-[var(--text-muted)] flex-shrink-0 tabular-nums">{model.session_count}</span>
                </button>
              )}
              {model.providers.map(provider => (
                <button
                  key={provider.key}
                  type="button"
                  role="option"
                  aria-selected={selected === provider.key}
                  onClick={() => pick(provider.key)}
                  className={`w-full px-2.5 py-1.5 flex items-center gap-2 text-left transition-colors duration-fast ${
                    selected === provider.key ? 'bg-[var(--bg-surface-hover)]' : 'hover:bg-[var(--bg-surface-hover)]'
                  }`}
                >
                  <span className="w-2 h-2 rounded-full bg-[var(--text-muted)]/60 flex-shrink-0" aria-hidden="true" />
                  <span className="min-w-0 flex-1" title={`${provider.provider} / ${model.label}`}>
                    <span className="block truncate text-helper text-[var(--text-primary)]">{provider.provider}</span>
                  </span>
                  <span className="ml-auto text-helper text-[var(--text-muted)] flex-shrink-0 tabular-nums">{provider.session_count}</span>
                </button>
              ))}
            </div>
          )
        })()}
      </div>
    </div>
  )
}
