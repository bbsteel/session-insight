import { lazy, Suspense, useCallback, useEffect, useRef, useState } from 'react'
import { fsList, fsRead, openFile, type FsEntry } from '../api'
import ThemeToggle from './ThemeToggle'

const SyntaxCodeBlock = lazy(() => import('./SyntaxCodeBlock'))

// Standalone file viewer opened in a new tab (#/file?path=…&cwd=…): directory
// tree rooted at the session cwd on the left with the target file expanded and
// highlighted, syntax-highlighted content on the right.

const EXT_LANG: Record<string, string> = {
  ts: 'typescript', tsx: 'tsx', js: 'javascript', jsx: 'jsx', mjs: 'javascript', cjs: 'javascript',
  go: 'go', py: 'python', rs: 'rust', java: 'java', kt: 'kotlin', rb: 'ruby', php: 'php',
  c: 'c', h: 'c', cpp: 'cpp', cc: 'cpp', hpp: 'cpp', cs: 'csharp', swift: 'swift',
  css: 'css', scss: 'scss', less: 'less', html: 'markup', xml: 'markup', vue: 'markup', svelte: 'markup',
  json: 'json', yaml: 'yaml', yml: 'yaml', toml: 'toml', ini: 'ini',
  md: 'markdown', sh: 'bash', bash: 'bash', zsh: 'bash', sql: 'sql', diff: 'diff',
}

function langForPath(path: string): string {
  const ext = path.split('.').pop()?.toLowerCase() ?? ''
  return EXT_LANG[ext] ?? 'text'
}

interface TreeDirProps {
  dir: string
  targetPath: string
  depth: number
  onOpenFile: (path: string) => void
  targetRef: React.RefObject<HTMLDivElement>
}

function TreeDir({ dir, targetPath, depth, onOpenFile, targetRef }: TreeDirProps) {
  const [entries, setEntries] = useState<FsEntry[] | null>(null)
  const [error, setError] = useState(false)
  const [open, setOpen] = useState<Record<string, boolean>>({})

  useEffect(() => {
    let cancelled = false
    fsList(dir)
      .then(list => { if (!cancelled) setEntries(list) })
      .catch(() => { if (!cancelled) setError(true) })
    return () => { cancelled = true }
  }, [dir])

  // Auto-expand directories on the path to the target file.
  useEffect(() => {
    if (!entries) return
    const next: Record<string, boolean> = {}
    for (const e of entries) {
      if (!e.is_dir) continue
      const sub = dir.replace(/\/$/, '') + '/' + e.name
      if (targetPath.startsWith(sub + '/')) next[e.name] = true
    }
    if (Object.keys(next).length) setOpen(prev => ({ ...next, ...prev }))
  }, [entries, dir, targetPath])

  if (error) return <div className="px-2 py-0.5 text-meta text-[var(--text-muted)]">无法读取目录</div>
  if (!entries) return <div className="px-2 py-0.5 text-meta text-[var(--text-muted)]">…</div>

  return (
    <div>
      {entries.map(e => {
        const full = dir.replace(/\/$/, '') + '/' + e.name
        const pad = { paddingLeft: `${depth * 14 + 8}px` }
        if (e.is_dir) {
          const expanded = !!open[e.name]
          return (
            <div key={e.name}>
              <div
                style={pad}
                onClick={() => setOpen(prev => ({ ...prev, [e.name]: !expanded }))}
                className="cursor-pointer whitespace-nowrap py-0.5 text-helper text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)]"
              >
                {expanded ? '▾ ' : '▸ '}{e.name}
              </div>
              {expanded && (
                <TreeDir dir={full} targetPath={targetPath} depth={depth + 1} onOpenFile={onOpenFile} targetRef={targetRef} />
              )}
            </div>
          )
        }
        const isTarget = full === targetPath
        return (
          <div
            key={e.name}
            ref={isTarget ? targetRef : undefined}
            style={pad}
            onClick={() => onOpenFile(full)}
            className={`cursor-pointer whitespace-nowrap py-0.5 text-helper hover:bg-[var(--bg-surface-hover)] ${
              isTarget
                ? 'bg-[var(--accent-blue)]/15 font-medium text-[var(--accent-blue)]'
                : 'text-[var(--text-primary)]'
            }`}
          >
            {e.name}
          </div>
        )
      })}
    </div>
  )
}

export default function FileViewer({ path, cwd }: { path: string; cwd: string }) {
  const [current, setCurrent] = useState(path)
  const [content, setContent] = useState<string | null>(null)
  const [truncated, setTruncated] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  const targetRef = useRef<HTMLDivElement>(null)

  const root = cwd && path.startsWith(cwd.replace(/\/$/, '') + '/')
    ? cwd
    : path.slice(0, path.lastIndexOf('/')) || '/'

  const load = useCallback((p: string) => {
    setCurrent(p)
    setContent(null)
    setLoadError(null)
    document.title = p.split('/').pop() ?? p
    fsRead(p)
      .then(d => { setContent(d.content); setTruncated(d.truncated) })
      .catch(err => setLoadError(err instanceof Error ? err.message : '读取失败'))
  }, [])

  useEffect(() => { load(path) }, [path, load])

  // Scroll the highlighted file into view once the tree has expanded to it.
  useEffect(() => {
    const timer = setTimeout(() => targetRef.current?.scrollIntoView({ block: 'center' }), 600)
    return () => clearTimeout(timer)
  }, [path])

  const rel = current.startsWith(root) ? current.slice(root.length).replace(/^\//, '') : current

  return (
    <div className="flex h-screen flex-col bg-[var(--bg-primary)] text-[var(--text-primary)]">
      <header className="flex flex-shrink-0 items-center gap-2 border-b border-[var(--border-default)] bg-[var(--bg-surface)] px-3" style={{ height: 40 }}>
        <span className="truncate text-helper text-[var(--text-secondary)]" title={current}>{current}</span>
        <span className="flex-1" />
        <button
          onClick={() => void openFile({ path: current }).catch(err => alert(err instanceof Error ? err.message : '打开失败'))}
          className="h-7 rounded-md border border-[var(--border-default)] px-2 text-nav text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]"
        >
          用编辑器打开
        </button>
        <ThemeToggle />
      </header>
      <div className="flex min-h-0 flex-1">
        <aside className="w-[280px] flex-shrink-0 overflow-auto border-r border-[var(--border-default)] bg-[var(--bg-surface)] py-1">
          <div className="truncate px-2 pb-1 text-meta text-[var(--text-muted)]" title={root}>{root}</div>
          <TreeDir dir={root} targetPath={current} depth={0} onOpenFile={load} targetRef={targetRef} />
        </aside>
        <main className="min-w-0 flex-1 overflow-auto">
          {loadError && <div className="p-4 text-helper text-[var(--error)]">{loadError}</div>}
          {content !== null && (
            <div className="p-2 text-[13px]">
              {truncated && <div className="px-2 pb-1 text-meta text-[var(--text-muted)]">（文件过大，仅显示前 1MB）</div>}
              <Suspense fallback={<pre className="p-2 text-helper text-[var(--text-muted)]">加载高亮…</pre>}>
                <SyntaxCodeBlock code={content} language={langForPath(current)} />
              </Suspense>
            </div>
          )}
          {content === null && !loadError && <div className="p-4 text-helper text-[var(--text-muted)]">加载 {rel}…</div>}
        </main>
      </div>
    </div>
  )
}
