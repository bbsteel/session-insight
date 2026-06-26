import { useEffect, useMemo, useRef, useState } from 'react'
import type { AgentInfo } from '../types'
import AgentIcon from './AgentIcon'

interface AgentFilterProps {
  agents: AgentInfo[]
  selected: string
  onSelect: (agentType: string) => void
}

export default function AgentFilter({ agents, selected, onSelect }: AgentFilterProps) {
  const dropdownRef = useRef<HTMLDivElement>(null)
  const [dropdownOpen, setDropdownOpen] = useState(false)

  const agentMap = useMemo(
    () => new Map(agents.map(agent => [agent.type, agent])),
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
  const selectedAgent = selected ? agentMap.get(selected) : undefined
  const selectedLabel = selectedAgent?.display_name ?? (selected || 'All')
  const selectedCount = selectedAgent?.session_count ?? totalSessions
  const selectedLive = selectedAgent?.live_count ?? totalLive

  const formatCount = (live: number, total: number) => `${live}/${total}`

  useEffect(() => {
    if (!dropdownOpen) return

    const closeOnOutsideClick = (event: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(event.target as Node)) {
        setDropdownOpen(false)
      }
    }
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setDropdownOpen(false)
    }

    document.addEventListener('mousedown', closeOnOutsideClick)
    window.addEventListener('keydown', closeOnEscape)
    return () => {
      document.removeEventListener('mousedown', closeOnOutsideClick)
      window.removeEventListener('keydown', closeOnEscape)
    }
  }, [dropdownOpen])

  const selectAgent = (agentType: string) => {
    onSelect(agentType)
    setDropdownOpen(false)
  }

  return (
    <div className="px-4 pb-2 flex-shrink-0">
      <div ref={dropdownRef} className="relative">
        <button
          type="button"
          onClick={() => setDropdownOpen(open => !open)}
          aria-expanded={dropdownOpen}
          aria-haspopup="listbox"
          className="w-full h-8 px-2.5 rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] text-body text-[var(--text-primary)] flex items-center gap-2 transition-colors duration-fast hover:bg-[var(--bg-surface-hover)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
        >
          {selected ? (
            <AgentIcon agentType={selected} size={18} />
          ) : (
            <span className="inline-flex w-[18px] h-[18px] items-center justify-center rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] text-[var(--text-muted)] flex-shrink-0">
              <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <circle cx="12" cy="12" r="9" />
                <path d="M3 12h18M12 3a15 15 0 0 1 0 18M12 3a15 15 0 0 0 0 18" />
              </svg>
            </span>
          )}
          <span className="truncate">{selectedLabel}</span>
          <span className="ml-auto text-helper text-[var(--text-muted)] flex-shrink-0">
            {formatCount(selectedLive, selectedCount)}
          </span>
          <svg
            className={`w-3.5 h-3.5 text-[var(--text-muted)] flex-shrink-0 transition-transform duration-fast ${dropdownOpen ? 'rotate-180' : ''}`}
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

        {dropdownOpen && (
          <div
            role="listbox"
            aria-label="按 Agent 筛选会话"
            className="absolute top-full mt-1 left-0 right-0 z-[var(--z-dropdown)] max-h-80 overflow-y-auto rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] shadow-lg py-1"
          >
            <button
              type="button"
              role="option"
              aria-selected={selected === ''}
              onClick={() => selectAgent('')}
              className={`w-full px-2.5 py-2 flex items-center gap-2 text-left transition-colors duration-fast ${
                selected === '' ? 'bg-[var(--bg-surface-hover)]' : 'hover:bg-[var(--bg-surface-hover)]'
              }`}
            >
              <span className="inline-flex w-[18px] h-[18px] items-center justify-center rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] text-[var(--text-muted)] flex-shrink-0">
                <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                  <circle cx="12" cy="12" r="9" />
                  <path d="M3 12h18M12 3a15 15 0 0 1 0 18M12 3a15 15 0 0 0 0 18" />
                </svg>
              </span>
              <span className="text-body text-[var(--text-primary)] truncate">All</span>
              <span className="ml-auto text-helper text-[var(--text-muted)] flex-shrink-0">
                {formatCount(totalLive, totalSessions)}
              </span>
            </button>

            {agents.map(agent => {
              const isSelected = selected === agent.type
              return (
                <button
                  type="button"
                  role="option"
                  aria-selected={isSelected}
                  key={agent.type}
                  onClick={() => selectAgent(agent.type)}
                  className={`w-full px-2.5 py-2 flex items-center gap-2 text-left transition-colors duration-fast ${
                    isSelected ? 'bg-[var(--bg-surface-hover)]' : 'hover:bg-[var(--bg-surface-hover)]'
                  }`}
                >
                  <AgentIcon agentType={agent.type} size={18} />
                  <span className="text-body text-[var(--text-primary)] truncate">
                    {agent.display_name}
                  </span>
                  <span className="ml-auto text-helper text-[var(--text-muted)] flex-shrink-0">
                    {formatCount(agent.live_count ?? 0, agent.session_count)}
                  </span>
                </button>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}
