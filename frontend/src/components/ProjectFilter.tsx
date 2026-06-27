import { useEffect, useRef, useState } from 'react'

export interface ProjectEntry {
  name: string
  session_count: number
}

interface ProjectFilterProps {
  projects: ProjectEntry[]
  selected: string
  onSelect: (project: string) => void
}

function FolderIcon({ size = 16 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
    </svg>
  )
}

export default function ProjectFilter({ projects, selected, onSelect }: ProjectFilterProps) {
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)

  const total = projects.reduce((n, p) => n + p.session_count, 0)
  const selectedEntry = selected ? projects.find(p => p.name === selected) : undefined
  const label = selectedEntry?.name ?? 'All Projects'
  const count = selectedEntry?.session_count ?? total

  useEffect(() => {
    if (!open) return
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

  if (projects.length === 0) return null

  return (
    <div className="px-4 pb-2 flex-shrink-0">
      <div ref={containerRef} className="relative">
        <button
          type="button"
          onClick={() => setOpen(v => !v)}
          aria-expanded={open}
          aria-haspopup="listbox"
          className="w-full h-8 px-2.5 rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] text-body text-[var(--text-primary)] flex items-center gap-2 transition-colors duration-fast hover:bg-[var(--bg-surface-hover)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
        >
          <span className={`flex-shrink-0 ${selected ? 'text-[var(--accent-blue)]' : 'text-[var(--text-muted)]'}`}>
            <FolderIcon size={16} />
          </span>
          <span className="truncate">{label}</span>
          <span className="ml-auto text-helper text-[var(--text-muted)] flex-shrink-0 tabular-nums">
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
            aria-label="按项目筛选会话"
            className="absolute top-full mt-1 left-0 right-0 z-[var(--z-dropdown)] max-h-72 overflow-y-auto rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] shadow-lg py-1"
          >
            <button
              type="button"
              role="option"
              aria-selected={selected === ''}
              onClick={() => pick('')}
              className={`w-full px-2.5 py-2 flex items-center gap-2 text-left transition-colors duration-fast ${
                selected === '' ? 'bg-[var(--bg-surface-hover)]' : 'hover:bg-[var(--bg-surface-hover)]'
              }`}
            >
              <span className="text-[var(--text-muted)] flex-shrink-0"><FolderIcon size={16} /></span>
              <span className="text-body text-[var(--text-primary)] truncate">All Projects</span>
              <span className="ml-auto text-helper text-[var(--text-muted)] flex-shrink-0 tabular-nums">{total}</span>
            </button>

            {projects.map(p => (
              <button
                key={p.name}
                type="button"
                role="option"
                aria-selected={selected === p.name}
                onClick={() => pick(p.name)}
                className={`w-full px-2.5 py-2 flex items-center gap-2 text-left transition-colors duration-fast ${
                  selected === p.name ? 'bg-[var(--bg-surface-hover)]' : 'hover:bg-[var(--bg-surface-hover)]'
                }`}
              >
                <span className="text-[var(--accent-blue)] flex-shrink-0"><FolderIcon size={16} /></span>
                <span className="text-body text-[var(--text-primary)] truncate" title={p.name}>{p.name}</span>
                <span className="ml-auto text-helper text-[var(--text-muted)] flex-shrink-0 tabular-nums">{p.session_count}</span>
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
