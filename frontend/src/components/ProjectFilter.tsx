import { useEffect, useMemo, useRef, useState } from 'react'
import { useI18n } from '../i18n'
import {
  defaultDirForKey,
  getProjectSortPref,
  setProjectSortPref,
  sortProjects,
  type ProjectEntry,
  type ProjectSortDir,
  type ProjectSortKey,
  type ProjectSortPref,
} from '../projectSort'

export type { ProjectEntry }

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

const SORT_KEYS: ProjectSortKey[] = ['name', 'sessions', 'recent']

function sortKeyLabel(t: (key: string) => string, key: ProjectSortKey): string {
  switch (key) {
    case 'name':
      return t('filter.projectSort.name')
    case 'sessions':
      return t('filter.projectSort.sessions')
    case 'recent':
      return t('filter.projectSort.recent')
  }
}

export default function ProjectFilter({ projects, selected, onSelect }: ProjectFilterProps) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [sortPref, setSortPref] = useState<ProjectSortPref>(() => getProjectSortPref())
  const containerRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)

  const total = projects.reduce((n, p) => n + p.session_count, 0)
  const selectedEntry = selected ? projects.find(p => p.name === selected) : undefined
  const label = selectedEntry?.name ?? t('filter.allProjects')
  const count = selectedEntry?.session_count ?? total

  useEffect(() => {
    if (!open) {
      setSearch('')
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

  const applySort = (next: ProjectSortPref) => {
    setSortPref(next)
    setProjectSortPref(next)
  }

  const selectSortKey = (key: ProjectSortKey) => {
    if (key === sortPref.key) return
    applySort({ key, dir: defaultDirForKey(key) })
  }

  const toggleDir = () => {
    const dir: ProjectSortDir = sortPref.dir === 'asc' ? 'desc' : 'asc'
    applySort({ ...sortPref, dir })
  }

  const ordered = useMemo(
    () => sortProjects(projects, sortPref.key, sortPref.dir),
    [projects, sortPref.key, sortPref.dir],
  )

  const visible = search.trim()
    ? ordered.filter(p => p.name.toLowerCase().includes(search.toLowerCase()))
    : ordered

  if (projects.length === 0) return null

  const dirLabel = sortPref.dir === 'asc' ? t('filter.projectSort.asc') : t('filter.projectSort.desc')
  const dirAria = t('filter.projectSort.toggleDir', { dir: dirLabel })

  return (
    <div className="px-4 pb-2 flex-shrink-0">
      <div ref={containerRef} className="relative">
        <button
          type="button"
          onClick={() => setOpen(v => !v)}
          aria-expanded={open}
          aria-haspopup="listbox"
          className="w-full h-9 px-2.5 rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] text-body text-[var(--text-primary)] flex items-center gap-2 transition-colors duration-fast hover:bg-[var(--bg-surface-hover)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
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
            aria-label={t('filter.projectsLabel')}
            className="absolute top-full mt-1 left-0 right-0 z-[var(--z-dropdown)] rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] shadow-lg"
          >
            {/* Search box */}
            <div className="p-1.5 border-b border-[var(--border-default)]">
              <div className="relative">
                <svg className="absolute left-2 top-1/2 -translate-y-1/2 w-3 h-3 text-[var(--text-muted)] pointer-events-none" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <circle cx="11" cy="11" r="8" /><line x1="21" y1="21" x2="16.65" y2="16.65" />
                </svg>
                <input
                  ref={searchRef}
                  type="text"
                  placeholder={t('filter.searchProjects')}
                  value={search}
                  onChange={e => setSearch(e.target.value)}
                  className="w-full h-7 rounded border border-[var(--border-default)] bg-[var(--bg-inset)] pl-6 pr-2 text-helper text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none focus:ring-1 focus:ring-[var(--accent-blue)]/30"
                />
              </div>
            </div>

            {/* Sort controls */}
            <div
              className="px-1.5 py-1.5 border-b border-[var(--border-default)] flex items-center gap-1"
              role="group"
              aria-label={t('filter.projectSort.label')}
            >
              <div className="flex flex-1 min-w-0 rounded border border-[var(--border-default)] overflow-hidden">
                {SORT_KEYS.map(key => {
                  const active = sortPref.key === key
                  return (
                    <button
                      key={key}
                      type="button"
                      onClick={() => selectSortKey(key)}
                      aria-pressed={active}
                      title={sortKeyLabel(t, key)}
                      className={`flex-1 min-w-0 h-6 px-1 text-[10px] leading-none truncate transition-colors duration-fast ${
                        active
                          ? 'bg-[var(--bg-surface-selected)] text-[var(--text-primary)] font-medium'
                          : 'bg-[var(--bg-inset)] text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
                      }`}
                    >
                      {sortKeyLabel(t, key)}
                    </button>
                  )
                })}
              </div>
              <button
                type="button"
                onClick={toggleDir}
                aria-label={dirAria}
                title={dirAria}
                className="flex-shrink-0 h-6 w-6 inline-flex items-center justify-center rounded border border-[var(--border-default)] bg-[var(--bg-inset)] text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] transition-colors duration-fast"
              >
                {sortPref.dir === 'asc' ? (
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.25" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <line x1="12" y1="19" x2="12" y2="5" />
                    <polyline points="5 12 12 5 19 12" />
                  </svg>
                ) : (
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.25" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <line x1="12" y1="5" x2="12" y2="19" />
                    <polyline points="19 12 12 19 5 12" />
                  </svg>
                )}
              </button>
            </div>

            {/* Options */}
            <div className="max-h-60 overflow-y-auto py-1">
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
                  <span className="text-[var(--text-muted)] flex-shrink-0"><FolderIcon size={16} /></span>
                  <span className="text-body text-[var(--text-primary)] truncate">{t('filter.allProjects')}</span>
                  <span className="ml-auto text-helper text-[var(--text-muted)] flex-shrink-0 tabular-nums">{total}</span>
                </button>
              )}

              {visible.map(p => (
                <button
                  key={p.name}
                  type="button"
                  role="option"
                  aria-selected={selected === p.name}
                  onClick={() => pick(p.name)}
                  className={`w-full px-2.5 py-2 flex items-center gap-2 text-left transition-colors duration-fast ${
                    selected === p.name ? 'bg-[var(--bg-surface-selected)]' : 'hover:bg-[var(--bg-surface-hover)]'
                  }`}
                >
                  <span className="text-[var(--accent-blue)] flex-shrink-0"><FolderIcon size={16} /></span>
                  <span className="text-body text-[var(--text-primary)] truncate" title={p.name}>{p.name}</span>
                  <span className="ml-auto text-helper text-[var(--text-muted)] flex-shrink-0 tabular-nums">{p.session_count}</span>
                </button>
              ))}

              {visible.length === 0 && (
                <div className="px-2.5 py-3 text-center text-helper text-[var(--text-muted)]">{t('filter.noProjects')}</div>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
