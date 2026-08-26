import { Fragment, useEffect, useMemo, useRef, useState } from 'react'
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
import { useI18n } from '../i18n'
import {
  getModelSortPref,
  setModelSortPref,
  sortModels,
  type ModelSortKey,
  type ModelSortPref,
} from '../modelSort'
import SortControls from './SortControls'
import { useAnchoredPanelRect } from './useAnchoredPanelRect'

const openCodeIcon = '/icons/opencode-logo-light-square.png'
const hyIcon = '/icons/hy.webp'

export interface ModelEntry {
  key: string
  id: string
  name: string
  provider: string
  providerKey: string
  iconKey: string
  label: string
  session_count: number
  /** ISO timestamp of the most recent session activity on this model (empty if unknown). */
  last_active: string
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

const SORT_KEYS: ModelSortKey[] = ['name', 'sessions', 'recent']

/** Widest panel the flat model grid opens at; clamped to the viewport. */
const PANEL_MAX_WIDTH = 660

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
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [expandedModelKeys, setExpandedModelKeys] = useState<ReadonlySet<string>>(new Set())
  const [currentSortPref, setCurrentSortPref] = useState<ModelSortPref>(() => getModelSortPref())
  const containerRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const panelRect = useAnchoredPanelRect(open, containerRef, PANEL_MAX_WIDTH)

  const total = models.reduce((n, m) => n + m.session_count, 0)
  const selectedModel = selected
    ? models.find(m => m.key === selected || m.providers.some(p => p.key === selected))
    : undefined
  const selectedProvider = selectedModel?.providers.find(p => p.key === selected)
  const selectedMeta = selectedModel ?? modelMeta(selected)
  const label = selectedModel?.label ?? selectedMeta.label

  /** Subtitle for a model: its single provider's name, or a localized provider count. */
  const providerLine = (model: ModelEntry) =>
    model.providers.length === 1
      ? model.providers[0].provider
      : t('filter.providerCount', { count: model.providers.length })

  // Only show a provider line when a concrete model/provider is selected — not for "All Models".
  const providerLabel = selected
    ? (selectedProvider?.provider ?? (selectedModel ? providerLine(selectedModel) : selectedMeta.provider))
    : ''
  const count = selectedProvider?.session_count ?? selectedModel?.session_count ?? total

  useEffect(() => {
    if (!open) {
      setSearch('')
      setExpandedModelKeys(new Set())
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

  const applySort = (newSortPref: ModelSortPref) => {
    setCurrentSortPref(newSortPref)
    setModelSortPref(newSortPref)
  }

  const toggleProviders = (modelKey: string) => {
    setExpandedModelKeys(prev => {
      const next = new Set(prev)
      if (next.has(modelKey)) next.delete(modelKey)
      else next.add(modelKey)
      return next
    })
  }

  const sortedModels = useMemo(
    () => sortModels(models, currentSortPref.key, currentSortPref.order),
    [models, currentSortPref],
  )

  const query = search.trim().toLowerCase()
  const visibleModels = useMemo(() => {
    if (!query) return sortedModels
    return sortedModels.filter(model =>
      model.label.toLowerCase().includes(query) ||
      model.name.toLowerCase().includes(query) ||
      model.providers.some(p => p.provider.toLowerCase().includes(query)),
    )
  }, [sortedModels, query])
  // A model matched only through one of its providers opens expanded so the
  // matching provider tile is visible without a second click.
  const autoExpandedKeys = useMemo(() => {
    if (!query) return new Set<string>()
    return new Set(
      visibleModels
        .filter(model =>
          !model.label.toLowerCase().includes(query) &&
          !model.name.toLowerCase().includes(query))
        .map(model => model.key),
    )
  }, [visibleModels, query])

  if (models.length === 0) return null

  return (
    <div className="px-4 pb-2 flex-shrink-0">
      <div ref={containerRef} className="relative">
        <button
          type="button"
          onClick={() => setOpen(v => !v)}
          aria-expanded={open}
          aria-haspopup="listbox"
          className="w-full h-9 px-2.5 rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] text-body text-[var(--text-primary)] flex items-center gap-2 transition-colors duration-fast hover:bg-[var(--bg-surface-hover)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
          title={providerLabel ? `${selected ? label : t('filter.allModels')} · ${providerLabel}` : undefined}
        >
          <span className="flex-shrink-0">
            <ModelIcon meta={selected ? selectedMeta : { id: 'all-models', provider: '', label: t('filter.allModels'), iconKey: 'all-models' }} size={16} />
          </span>
          <span className="min-w-0 flex-1 text-left truncate">
            {selected ? label : t('filter.allModels')}
            {providerLabel ? (
              <span className="text-helper text-[var(--text-muted)]"> · {providerLabel}</span>
            ) : null}
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

        {open && panelRect && (
          <div
            role="listbox"
            aria-label={t('filter.modelsLabel')}
            style={{ position: 'fixed', left: panelRect.left, top: panelRect.top, width: panelRect.width, maxHeight: panelRect.maxHeight }}
            className="z-[var(--z-dropdown)] flex flex-col rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] shadow-lg"
          >
            <div className="p-1.5 border-b border-[var(--border-default)]">
              <div className="relative">
                <svg className="absolute left-2 top-1/2 -translate-y-1/2 w-3 h-3 text-[var(--text-muted)] pointer-events-none" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <circle cx="11" cy="11" r="8" /><line x1="21" y1="21" x2="16.65" y2="16.65" />
                </svg>
                <input
                  ref={searchRef}
                  type="text"
                  placeholder={t('filter.searchModels')}
                  value={search}
                  onChange={e => setSearch(e.target.value)}
                  className="w-full h-7 rounded border border-[var(--border-default)] bg-[var(--bg-inset)] pl-6 pr-2 text-helper text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none focus:ring-1 focus:ring-[var(--accent-blue)]/30"
                />
              </div>
            </div>

            <SortControls
              keys={SORT_KEYS}
              nameKey="name"
              currentSortPref={currentSortPref}
              onSortPrefChange={applySort}
              groupLabel={t('filter.sort.label')}
            />

            <div className="min-h-0 flex-1 overflow-y-auto py-1">
              {!search.trim() && (
                <button
                  type="button"
                  role="option"
                  aria-selected={selected === ''}
                  onClick={() => pick('')}
                  className={`w-full px-2.5 py-2 flex items-center gap-2 text-left transition-colors duration-fast ${
                    selected === '' ? 'bg-[var(--bg-surface-selected)]' : 'hover:bg-[var(--bg-surface-hover)]'
                  }`}
                >
                  <span className="text-[var(--text-muted)] flex-shrink-0">
                    <ModelIcon meta={{ id: 'all-models', provider: '', label: t('filter.allModels'), iconKey: 'all-models' }} size={18} />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-body text-[var(--text-primary)]">{t('filter.allModels')}</span>
                  </span>
                  <span className="ml-auto text-helper text-[var(--text-muted)] flex-shrink-0 tabular-nums">{total}</span>
                </button>
              )}

              <div className="grid gap-0.5 px-1" style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(172px, 1fr))' }}>
                {visibleModels.map(model => {
                  const isExpanded = expandedModelKeys.has(model.key) || autoExpandedKeys.has(model.key)
                  const isSelected = selected === model.key || model.providers.some(p => p.key === selected)
                  const hasMultipleProviders = model.providers.length > 1
                  return (
                    <Fragment key={model.key}>
                      {/*
                        The model tile is a div[role=option] (not a <button>) so
                        the provider chevron can be a real nested button.
                      */}
                      <div
                        role="option"
                        aria-selected={isSelected}
                        tabIndex={0}
                        onClick={() => pick(model.key)}
                        onKeyDown={e => {
                          if (e.target !== e.currentTarget) return
                          if (e.key === 'Enter' || e.key === ' ') {
                            e.preventDefault()
                            pick(model.key)
                          }
                        }}
                        title={`${providerLine(model)} / ${model.label}`}
                        className={`px-2 py-1 flex items-center gap-2 text-left rounded cursor-pointer transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
                          isSelected ? 'bg-[var(--bg-surface-selected)]' : 'hover:bg-[var(--bg-surface-hover)]'
                        }`}
                      >
                        <span className="flex-shrink-0"><ModelIcon meta={model} size={20} /></span>
                        <span className="min-w-0 flex-1">
                          <span className="block truncate text-body text-[var(--text-primary)]">{model.label}</span>
                          <span className="block truncate text-helper text-[var(--text-muted)]">{providerLine(model)}</span>
                        </span>
                        <span className="ml-auto text-helper text-[var(--text-muted)] flex-shrink-0 tabular-nums">{model.session_count}</span>
                        {hasMultipleProviders && (
                          <button
                            type="button"
                            aria-expanded={isExpanded}
                            aria-label={t('filter.toggleProviders', { model: model.label })}
                            title={t('filter.toggleProviders', { model: model.label })}
                            onClick={e => {
                              e.stopPropagation()
                              toggleProviders(model.key)
                            }}
                            className="w-4 h-4 flex items-center justify-center rounded-sm text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-inset)] flex-shrink-0 transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
                          >
                            <svg
                              className={`w-3 h-3 transition-transform duration-fast ${isExpanded ? 'rotate-90' : ''}`}
                              viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"
                              aria-hidden="true"
                            >
                              <polyline points="9 6 15 12 9 18" />
                            </svg>
                          </button>
                        )}
                      </div>
                      {/*
                        Expanded provider tiles insert inline right after their
                        model tile: the panel chrome, scroll position, and every
                        tile above stay exactly where they were — only content
                        below the insertion point shifts down.
                      */}
                      {isExpanded && model.providers.map(provider => (
                        <div
                          key={provider.key}
                          role="option"
                          aria-selected={selected === provider.key}
                          tabIndex={0}
                          onClick={() => pick(provider.key)}
                          onKeyDown={e => {
                            if (e.key === 'Enter' || e.key === ' ') {
                              e.preventDefault()
                              pick(provider.key)
                            }
                          }}
                          title={`${provider.provider} / ${model.label}`}
                          className={`pl-6 pr-2 py-1 flex items-center gap-2 text-left rounded cursor-pointer transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
                            selected === provider.key ? 'bg-[var(--bg-surface-selected)]' : 'hover:bg-[var(--bg-surface-hover)]'
                          }`}
                        >
                          <span className="w-1.5 h-1.5 rounded-full bg-[var(--text-muted)]/60 flex-shrink-0" aria-hidden="true" />
                          <span className="min-w-0 flex-1">
                            <span className="block truncate text-helper text-[var(--text-primary)]">{provider.provider}</span>
                          </span>
                          <span className="ml-auto text-helper text-[var(--text-muted)] flex-shrink-0 tabular-nums">{provider.session_count}</span>
                        </div>
                      ))}
                    </Fragment>
                  )
                })}
              </div>

              {visibleModels.length === 0 && (
                <div className="px-2.5 py-3 text-center text-helper text-[var(--text-muted)]">{t('filter.noModels')}</div>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
