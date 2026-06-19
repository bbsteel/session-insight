import { useState, useRef, useEffect, useCallback, useMemo } from 'react'
import { resolveAgentStyle } from '../agentStyles'
import type { AgentInfo } from '../types'

const STORAGE_KEY = 'agent-filter-state'

interface AgentFilterState {
  pinned: string[]
  mruOrder: string[]
}

function loadState(): AgentFilterState {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw) return JSON.parse(raw)
  } catch { /* ignore */ }
  return { pinned: [], mruOrder: [] }
}

function saveState(state: AgentFilterState) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(state))
  } catch { /* ignore */ }
}

// Visible slot count (excluding All) by container width
function slotCount(width: number): number {
  if (width >= 260) return 3
  if (width >= 220) return 2
  return 1
}

interface AgentFilterProps {
  agents: AgentInfo[]
  selected: string
  onSelect: (agentType: string) => void
}

export default function AgentFilter({ agents, selected, onSelect }: AgentFilterProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const dropdownRef = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(260)
  const [dropdownOpen, setDropdownOpen] = useState(false)
  const [state, setState] = useState<AgentFilterState>(loadState)

  // ResizeObserver for responsive slot count
  useEffect(() => {
    const el = containerRef.current
    if (!el) return
    const ro = new ResizeObserver(entries => {
      for (const entry of entries) {
        setWidth(entry.contentRect.width)
        setDropdownOpen(false)
      }
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  // Persist state changes
  useEffect(() => {
    saveState(state)
  }, [state])

  // Close dropdown on outside click
  useEffect(() => {
    if (!dropdownOpen) return
    const handler = (e: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target as Node)) {
        setDropdownOpen(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [dropdownOpen])

  // Close dropdown on Escape
  useEffect(() => {
    if (!dropdownOpen) return
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setDropdownOpen(false)
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [dropdownOpen])

  // Build visible agent list: All + pinned + MRU fill
  const { visibleKeys, overflowKeys } = useMemo(() => {
    const slots = slotCount(width)
    const agentTypes = agents.map(a => a.type)
    const agentMap = new Map(agents.map(a => [a.type, a]))

    // Known agents (in MRU or pinned) that still exist
    const knownPinned = state.pinned.filter(t => agentMap.has(t))
    const knownMru = state.mruOrder.filter(t => agentMap.has(t) && !knownPinned.includes(t))

    // Agents not yet in state: sort by session count desc, fallback to API order
    const unseen = agentTypes.filter(t => !knownPinned.includes(t) && !knownMru.includes(t))
    unseen.sort((a, b) => (agentMap.get(b)?.session_count ?? 0) - (agentMap.get(a)?.session_count ?? 0))

    const ordered = [...knownPinned, ...knownMru, ...unseen]

    const visible = ordered.slice(0, slots)
    const overflow = ordered.slice(slots)
    // Folded agents in the dropdown are sorted by session count descending
    overflow.sort((a, b) => (agentMap.get(b)?.session_count ?? 0) - (agentMap.get(a)?.session_count ?? 0))
    return { visibleKeys: visible, overflowKeys: overflow }
  }, [agents, state, width])

  const hasOverflow = overflowKeys.length > 0

  const agentLabel = useCallback((type: string) => {
    return agents.find(a => a.type === type)?.display_name ?? type
  }, [agents])

  const agentCount = useCallback((type: string) => {
    return agents.find(a => a.type === type)?.session_count ?? 0
  }, [agents])

  const selectAgent = useCallback((type: string) => {
    onSelect(type)

    // Update MRU: push selected to front
    if (type !== '') {
      setState(prev => {
        const mru = [type, ...prev.mruOrder.filter(t => t !== type && !prev.pinned.includes(t))]
        return { ...prev, mruOrder: mru }
      })
    }
    setDropdownOpen(false)
  }, [onSelect])

  const togglePin = useCallback((type: string) => {
    setState(prev => {
      const isPinned = prev.pinned.includes(type)
      if (isPinned) {
        return {
          ...prev,
          pinned: prev.pinned.filter(t => t !== type),
        }
      } else {
        return {
          ...prev,
          pinned: [...prev.pinned, type],
          mruOrder: prev.mruOrder.filter(t => t !== type),
        }
      }
    })
    // Don't close dropdown — user may want to pin multiple
  }, [])

  return (
    <div ref={containerRef} className="px-4 pb-2 flex-shrink-0">
      <div className="flex rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] p-0.5 gap-0.5 relative">
        {/* Always-visible All button */}
        <button
          onClick={() => selectAgent('')}
          aria-pressed={selected === ''}
          className={`h-6 px-2 rounded-sm text-meta transition-colors duration-fast flex-shrink-0 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
            selected === '' ? 'bg-[var(--bg-surface)] text-[var(--text-primary)] shadow-sm' : 'text-[var(--text-muted)] hover:text-[var(--text-primary)]'
          }`}
        >
          All
        </button>

        {/* Visible agent buttons */}
        {visibleKeys.map(type => {
          const style = resolveAgentStyle(type)
          const pinned = state.pinned.includes(type)
          return (
            <button
              key={type}
              onClick={() => selectAgent(type)}
              aria-pressed={selected === type}
              className={`h-6 px-1.5 rounded-sm text-meta transition-colors duration-fast truncate flex items-center gap-1 flex-shrink-0 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
                selected === type ? 'bg-[var(--bg-surface)] text-[var(--text-primary)] shadow-sm' : 'text-[var(--text-muted)] hover:text-[var(--text-primary)]'
              }`}
            >
              <span
                className="inline-block rounded-full flex-shrink-0"
                style={{
                  width: 10,
                  height: 10,
                  backgroundColor: style?.accent ?? '#6b7280',
                  border: '1px solid rgba(255,255,255,0.2)',
                }}
              />
              {pinned && <span className="text-[9px] leading-none">📌</span>}
              <span className="truncate">{agentLabel(type)}</span>
            </button>
          )
        })}

        {/* "+N" overflow button */}
        {hasOverflow && (
          <div className="relative" ref={dropdownRef}>
            <button
              onClick={() => setDropdownOpen(o => !o)}
              className={`h-6 px-1.5 rounded-sm text-meta transition-colors duration-fast flex-shrink-0 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
                dropdownOpen ? 'bg-[var(--bg-surface)] text-[var(--text-primary)] shadow-sm' : 'text-[var(--text-muted)] hover:text-[var(--text-primary)]'
              }`}
            >
              +{overflowKeys.length} ▾
            </button>

            {dropdownOpen && (
              <div
                className="absolute top-full mt-1 left-0 z-[var(--z-dropdown)] rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] shadow-lg py-1"
                style={{ minWidth: '100%' }}
              >
                {overflowKeys.map(type => {
                  const style = resolveAgentStyle(type)
                  const pinned = state.pinned.includes(type)
                  return (
                    <div
                      key={type}
                      className="flex items-center gap-2 px-3 py-1.5 hover:bg-[var(--bg-surface-hover)] cursor-pointer transition-colors duration-fast"
                    >
                      <button
                        onClick={() => selectAgent(type)}
                        className="flex items-center gap-2 flex-1 min-w-0 text-left"
                      >
                        <span
                          className="inline-block rounded-full flex-shrink-0"
                          style={{
                            width: 10,
                            height: 10,
                            backgroundColor: style?.accent ?? '#6b7280',
                            border: '1px solid rgba(255,255,255,0.2)',
                          }}
                        />
                        <span className="text-body text-[var(--text-primary)] truncate">{agentLabel(type)}</span>
                      </button>
                      <span className="text-helper text-[var(--text-muted)] flex-shrink-0">{agentCount(type)}</span>
                      <button
                        onClick={(e) => { e.stopPropagation(); togglePin(type) }}
                        className={`flex-shrink-0 text-sm transition-opacity duration-fast hover:opacity-100 ${pinned ? 'opacity-100' : 'opacity-30'}`}
                        title={pinned ? '取消固定' : '固定到可见行'}
                      >
                        📌
                      </button>
                    </div>
                  )
                })}
                <div className="border-t border-[var(--border-muted)] mt-1 pt-1">
                  <div className="px-3 py-1.5 text-helper text-[var(--text-muted)] opacity-50 cursor-not-allowed">
                    ⚙️ Manage agents
                  </div>
                </div>
              </div>
            )}
          </div>
        )}

        {/* Flex spacer to fill remaining space */}
        <div className="flex-1" />
      </div>
    </div>
  )
}
