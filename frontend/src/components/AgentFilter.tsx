import { useEffect, useMemo, useRef, useState, useCallback } from 'react'
import type { AgentInfo } from '../types'
import AgentIcon from './AgentIcon'
import { getAgentLabel } from '../sidebarRows'

interface AgentFilterProps {
  agents: AgentInfo[]
  selected: string
  onSelect: (agentType: string) => void
}

const ICON_BTN = 30
const GAP = 4
const STEP = ICON_BTN + GAP

export default function AgentFilter({ agents, selected, onSelect }: AgentFilterProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const overflowRef = useRef<HTMLDivElement>(null)
  const [containerWidth, setContainerWidth] = useState(0)
  const [overflowOpen, setOverflowOpen] = useState(false)

  const sortedAgents = useMemo(
    () => [...agents].sort((a, b) => b.session_count - a.session_count),
    [agents],
  )

  const totalSessions = useMemo(
    () => agents.reduce((total, agent) => total + agent.session_count, 0),
    [agents],
  )
  const totalLive = useMemo(
    () => agents.reduce((total, agent) => total + (agent.live_count ?? 0), 0),
    [agents],
  )

  useEffect(() => {
    const el = containerRef.current
    if (!el) return
    const ro = new ResizeObserver(entries => {
      for (const entry of entries) {
        setContainerWidth(entry.contentRect.width)
      }
    })
    ro.observe(el)
    setContainerWidth(el.getBoundingClientRect().width)
    return () => ro.disconnect()
  }, [])

  const { visibleAgents, hiddenAgents } = useMemo(() => {
    if (containerWidth === 0 || sortedAgents.length === 0) {
      return { visibleAgents: sortedAgents, hiddenAgents: [] }
    }
    const maxNoOverflow = Math.floor(containerWidth / STEP) - 1
    if (maxNoOverflow >= sortedAgents.length) {
      return { visibleAgents: sortedAgents, hiddenAgents: [] }
    }
    const withOverflow = Math.floor(containerWidth / STEP) - 2
    const visible = Math.max(0, Math.min(withOverflow, sortedAgents.length))
    return {
      visibleAgents: sortedAgents.slice(0, visible),
      hiddenAgents: sortedAgents.slice(visible),
    }
  }, [sortedAgents, containerWidth])

  useEffect(() => {
    if (!overflowOpen) return
    const close = (e: MouseEvent) => {
      if (overflowRef.current && !overflowRef.current.contains(e.target as Node)) {
        setOverflowOpen(false)
      }
    }
    const esc = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOverflowOpen(false)
    }
    document.addEventListener('mousedown', close)
    window.addEventListener('keydown', esc)
    return () => {
      document.removeEventListener('mousedown', close)
      window.removeEventListener('keydown', esc)
    }
  }, [overflowOpen])

  const selectAgent = useCallback((agentType: string) => {
    onSelect(agentType)
    setOverflowOpen(false)
  }, [onSelect])

  const showOverflow = hiddenAgents.length > 0

  return (
    <div className="px-3 pb-2 flex-shrink-0">
      <div
        ref={containerRef}
        className="flex items-center gap-1 overflow-hidden"
      >
        <button
          type="button"
          onClick={() => selectAgent('')}
          aria-pressed={selected === ''}
          title={`All (${totalLive}/${totalSessions})`}
          className={`flex-shrink-0 flex items-center justify-center rounded-md transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
            selected === ''
              ? 'bg-[var(--bg-surface-hover)] text-[var(--text-primary)]'
              : 'text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
          }`}
          style={{ width: ICON_BTN, height: ICON_BTN }}
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <circle cx="12" cy="12" r="9" />
            <path d="M3 12h18M12 3a15 15 0 0 1 0 18M12 3a15 15 0 0 0 0 18" />
          </svg>
        </button>

        {visibleAgents.map(agent => {
          const isSelected = selected === agent.type
          const label = agent.display_name || getAgentLabel(agent.type)
          const live = agent.live_count ?? 0
          const total = agent.session_count
          const title = `${label} (${live}/${total})`
          return (
            <button
              key={agent.type}
              type="button"
              onClick={() => selectAgent(agent.type)}
              aria-pressed={isSelected}
              title={title}
              className={`flex-shrink-0 flex items-center justify-center rounded-md transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] relative ${
                isSelected
                  ? 'bg-[var(--bg-surface-hover)]'
                  : 'hover:bg-[var(--bg-surface-hover)]'
              }`}
              style={{ width: ICON_BTN, height: ICON_BTN }}
            >
              <AgentIcon agentType={agent.type} size={22} />
              {live > 0 && (
                <span
                  className="absolute top-0.5 right-0.5 w-1.5 h-1.5 rounded-full bg-[var(--success)] ring-1 ring-[var(--bg-surface)]"
                  title={`${live} 活跃中`}
                />
              )}
            </button>
          )
        })}

        {showOverflow && (
          <div ref={overflowRef} className="relative flex-shrink-0 ml-auto">
            <button
              type="button"
              onClick={() => setOverflowOpen(v => !v)}
              aria-expanded={overflowOpen}
              aria-haspopup="listbox"
              aria-label="更多 Agent"
              title="更多 Agent"
              className={`flex items-center justify-center rounded-md transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
                overflowOpen
                  ? 'bg-[var(--bg-surface-hover)] text-[var(--text-primary)]'
                  : 'text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
              }`}
              style={{ width: ICON_BTN, height: ICON_BTN }}
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor">
                <circle cx="5" cy="12" r="2" />
                <circle cx="12" cy="12" r="2" />
                <circle cx="19" cy="12" r="2" />
              </svg>
            </button>

            {overflowOpen && (
              <div
                role="listbox"
                aria-label="全部 Agent"
                className="absolute top-full right-0 mt-1 z-[var(--z-dropdown)] w-56 max-h-80 overflow-y-auto rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] shadow-lg py-1"
              >
                {sortedAgents.map(agent => {
                  const isSelected = selected === agent.type
                  const label = agent.display_name || getAgentLabel(agent.type)
                  const live = agent.live_count ?? 0
                  const total = agent.session_count
                  return (
                    <button
                      key={agent.type}
                      type="button"
                      role="option"
                      aria-selected={isSelected}
                      onClick={() => selectAgent(agent.type)}
                      className={`w-full px-2.5 py-2 flex items-center gap-2 text-left transition-colors duration-fast ${
                        isSelected ? 'bg-[var(--bg-surface-hover)]' : 'hover:bg-[var(--bg-surface-hover)]'
                      }`}
                    >
                      <AgentIcon agentType={agent.type} size={18} />
                      <span className="text-body text-[var(--text-primary)] truncate flex-1 min-w-0">
                        {label}
                      </span>
                      <span className="text-helper text-[var(--text-muted)] flex-shrink-0 tabular-nums">
                        {live}/{total}
                      </span>
                    </button>
                  )
                })}
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
