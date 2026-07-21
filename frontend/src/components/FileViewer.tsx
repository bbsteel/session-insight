import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Virtuoso, type VirtuosoHandle } from 'react-virtuoso'
import { fsList, fsRead, openFile, type FsEntry } from '../api'
import { activeOutlineId, type OutlineItem, type OutlineKind } from '../codeOutline'
import {
  codeParsePolicy,
  formatByteSize,
  MAX_FILE_PARSE_BYTES,
  type CodeParsePolicy,
} from '../codeReaderPolicy'
import {
  languageSupportForPath,
  shouldShowLanguageSupportBanner,
} from '../codeReaderSupport'
import type { CodeReaderHandle } from './CodeReader'
import { LanguageSwitch } from './LanguageSwitch'
import { ThemeSwitch } from './ThemeToggle'

const CodeReader = lazy(() => import('./CodeReader'))

// Standalone file viewer opened in a new tab (#/file?path=…&cwd=…): directory
// tree rooted at the session cwd on the left with the target file expanded and
// highlighted, syntax-highlighted content on the right.

interface TreeDirProps {
  dir: string
  targetPath: string
  depth: number
  onOpenFile: (path: string) => void
  targetRef: React.RefObject<HTMLDivElement>
}

const KIND_ICON: Record<OutlineKind, string> = {
  class: 'C',
  interface: 'I',
  enum: 'E',
  function: 'ƒ',
  method: 'm',
  constructor: '◇',
  heading: '#',
}

function filterOutline(items: OutlineItem[], query: string): OutlineItem[] {
  if (!query) return items
  const q = query.toLocaleLowerCase()
  return items.flatMap(item => {
    const children = filterOutline(item.children, query)
    return item.name.toLocaleLowerCase().includes(q) || children.length
      ? [{ ...item, children }]
      : []
  })
}

interface FlatOutlineItem {
  item: OutlineItem
  depth: number
}

function flattenWithDepth(items: OutlineItem[], depth = 0, out: FlatOutlineItem[] = []): FlatOutlineItem[] {
  for (const item of items) {
    out.push({ item, depth })
    flattenWithDepth(item.children, depth + 1, out)
  }
  return out
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

export default function FileViewer({ path, cwd, line }: { path: string; cwd: string; line?: number }) {
  const [current, setCurrent] = useState(path)
  const [content, setContent] = useState<string | null>(null)
  const [fileSize, setFileSize] = useState(0)
  const [parsePolicy, setParsePolicy] = useState<CodeParsePolicy>('enabled')
  const [loadError, setLoadError] = useState<string | null>(null)
  const [readerLine, setReaderLine] = useState<number | undefined>(line)
  const [outline, setOutline] = useState<OutlineItem[]>([])
  const [outlineComplete, setOutlineComplete] = useState(false)
  const [outlineQuery, setOutlineQuery] = useState('')
  const [activePosition, setActivePosition] = useState(0)
  const targetRef = useRef<HTMLDivElement>(null)
  const readerRef = useRef<CodeReaderHandle>(null)
  const outlineListRef = useRef<VirtuosoHandle>(null)

  const root = cwd && path.startsWith(cwd.replace(/\/$/, '') + '/')
    ? cwd
    : path.slice(0, path.lastIndexOf('/')) || '/'

  const load = useCallback((p: string, targetLine?: number) => {
    setCurrent(p)
    setReaderLine(targetLine)
    setContent(null)
    setLoadError(null)
    setFileSize(0)
    setParsePolicy('enabled')
    setOutline([])
    setOutlineComplete(false)
    setOutlineQuery('')
    document.title = p.split('/').pop() ?? p
    fsRead(p)
      .then(d => {
        setContent(d.content)
        setFileSize(d.size)
        setParsePolicy(codeParsePolicy(d.size, d.truncated))
      })
      .catch(err => setLoadError(err instanceof Error ? err.message : '读取失败'))
  }, [])

  useEffect(() => { load(path, line) }, [path, line, load])

  // Match the terminal's find shortcut even when focus is on the file tree,
  // header, or another control instead of CodeMirror's editor surface.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && !event.altKey && event.key.toLowerCase() === 'f') {
        event.preventDefault()
        readerRef.current?.search()
      }
    }
    document.addEventListener('keydown', onKey, true)
    return () => document.removeEventListener('keydown', onKey, true)
  }, [])

  // Scroll the highlighted file into view once the tree has expanded to it.
  useEffect(() => {
    const timer = setTimeout(() => targetRef.current?.scrollIntoView({ block: 'center' }), 600)
    return () => clearTimeout(timer)
  }, [current])

  const rel = current.startsWith(root) ? current.slice(root.length).replace(/^\//, '') : current
  const parseEnabled = parsePolicy === 'enabled' || parsePolicy === 'confirmed'
  const langSupport = useMemo(() => languageSupportForPath(current), [current])
  const showLangBanner = shouldShowLanguageSupportBanner(langSupport)
  const visibleOutline = useMemo(() => filterOutline(outline, outlineQuery.trim()), [outline, outlineQuery])
  const visibleRows = useMemo(() => flattenWithDepth(visibleOutline), [visibleOutline])
  const activeId = useMemo(() => activeOutlineId(outline, activePosition), [outline, activePosition])
  const handleOutline = useCallback((next: OutlineItem[], complete: boolean) => {
    setOutline(next)
    setOutlineComplete(complete)
  }, [])
  const handleActivePosition = useCallback((position: number) => setActivePosition(position), [])

  useEffect(() => {
    const index = visibleRows.findIndex(row => row.item.id === activeId)
    if (index >= 0) outlineListRef.current?.scrollIntoView({ index, behavior: 'auto' })
  }, [activeId, visibleRows])

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
        <button
          type="button"
          onClick={() => readerRef.current?.search()}
          className="h-7 rounded-md border border-[var(--border-default)] px-2 text-nav text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]"
        >
          查找
        </button>
        <LanguageSwitch />
        <ThemeSwitch />
      </header>
      <div className="flex min-h-0 flex-1">
        <aside className="w-[280px] flex-shrink-0 overflow-auto border-r border-[var(--border-default)] bg-[var(--bg-surface)] py-1">
          <div className="truncate px-2 pb-1 text-meta text-[var(--text-muted)]" title={root}>{root}</div>
          <TreeDir dir={root} targetPath={current} depth={0} onOpenFile={p => load(p)} targetRef={targetRef} />
        </aside>
        <main className="min-w-0 flex-1 overflow-hidden">
          {loadError && <div className="p-4 text-helper text-[var(--error)]">{loadError}</div>}
          {content !== null && (
            <div className="flex h-full min-h-0 flex-col">
              {parsePolicy === 'confirm' && (
                <div className="z-10 flex flex-shrink-0 items-center gap-2 border-b border-[var(--warning)]/30 bg-[var(--warning)]/10 px-3 py-1.5 text-helper text-[var(--text-secondary)]">
                  <span>大型文件（{formatByteSize(fileSize)}）的语法解析和结构提取可能明显占用 CPU 和内存。</span>
                  <button
                    type="button"
                    onClick={() => setParsePolicy('confirmed')}
                    className="h-6 rounded border border-[var(--warning)]/50 px-2 text-meta font-medium text-[var(--warning)] hover:bg-[var(--warning)]/10"
                  >
                    仍要解析
                  </button>
                </div>
              )}
              {parsePolicy === 'confirmed' && (
                <div className="z-10 flex-shrink-0 border-b border-[var(--warning)]/30 bg-[var(--warning)]/10 px-3 py-1.5 text-helper text-[var(--warning)]">
                  性能提示：已启用大型文件解析（{formatByteSize(fileSize)}），滚动、搜索和结构导航可能持续受到影响。
                </div>
              )}
              {parsePolicy === 'refused' && (
                <div className="z-10 flex-shrink-0 border-b border-[var(--warning)]/30 bg-[var(--warning)]/10 px-3 py-1.5 text-helper text-[var(--warning)]">
                  性能提示：文件大小 {formatByteSize(fileSize)}，超过 {formatByteSize(MAX_FILE_PARSE_BYTES)} 解析上限；已拒绝语法解析，仅虚拟预览前 2 MiB。
                </div>
              )}
              {showLangBanner && (
                <div
                  role="status"
                  className="z-10 flex flex-shrink-0 items-start gap-2 border-b border-[var(--warning)]/30 bg-[var(--warning)]/10 px-3 py-1.5 text-helper text-[var(--text-secondary)]"
                  title={langSupport.summary}
                >
                  <span
                    className="mt-0.5 flex h-4 w-4 flex-shrink-0 items-center justify-center rounded-full border border-[var(--warning)]/60 text-[11px] font-bold leading-none text-[var(--warning)]"
                    aria-hidden
                  >
                    !
                  </span>
                  <span className="min-w-0 leading-snug">
                    <span className="font-medium text-[var(--warning)]">语言支持</span>
                    <span className="text-[var(--text-muted)]"> · </span>
                    {langSupport.summary}
                  </span>
                </div>
              )}
              <div className="min-h-0 flex-1">
                <Suspense fallback={<div className="p-4 text-helper text-[var(--text-muted)]">加载代码阅读器…</div>}>
                  <CodeReader
                    ref={readerRef}
                    path={current}
                    content={content}
                    initialLine={readerLine}
                    parseLanguage={parseEnabled}
                    onOutline={handleOutline}
                    onActivePosition={handleActivePosition}
                  />
                </Suspense>
              </div>
            </div>
          )}
          {content === null && !loadError && <div className="p-4 text-helper text-[var(--text-muted)]">加载 {rel}…</div>}
        </main>
        <aside className="flex w-[280px] flex-shrink-0 flex-col border-l border-[var(--border-default)] bg-[var(--bg-surface)]">
          <div className="flex h-8 flex-shrink-0 items-center gap-2 border-b border-[var(--border-muted)] px-2">
            <span className="text-nav font-medium text-[var(--text-secondary)]">结构</span>
            {parseEnabled && !outlineComplete && content !== null && <span className="text-meta text-[var(--text-muted)]">解析中…</span>}
          </div>
          <div className="flex-shrink-0 border-b border-[var(--border-muted)] p-2">
            <input
              value={outlineQuery}
              onChange={e => setOutlineQuery(e.target.value)}
              disabled={!parseEnabled}
              placeholder={parseEnabled ? '筛选结构' : '结构解析未启用'}
              aria-label="筛选文件结构"
              className="h-7 w-full rounded border border-[var(--border-default)] bg-[var(--bg-inset)] px-2 text-helper text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none"
            />
          </div>
          <div className="min-h-0 flex-1 overflow-auto py-1">
            {visibleRows.length > 0 ? (
              <Virtuoso
                ref={outlineListRef}
                className="h-full"
                data={visibleRows}
                computeItemKey={(_, row) => row.item.id}
                itemContent={(_, { item, depth }) => (
                  <button
                    type="button"
                    onClick={() => readerRef.current?.reveal(item.from)}
                    title={`${item.name} · 第 ${item.line} 行`}
                    style={{ paddingLeft: `${depth * 14 + 8}px` }}
                    className={`flex h-7 w-full min-w-0 items-center gap-1.5 pr-2 text-left text-helper hover:bg-[var(--bg-surface-hover)] ${
                      item.id === activeId ? 'bg-[var(--accent-blue)]/10 text-[var(--accent-blue)]' : 'text-[var(--text-secondary)]'
                    }`}
                  >
                    <span className="flex h-4 w-4 flex-shrink-0 items-center justify-center rounded bg-[var(--bg-inset)] font-mono text-[10px] text-[var(--text-muted)]">
                      {KIND_ICON[item.kind]}
                    </span>
                    <span className="min-w-0 flex-1 truncate">{item.name}</span>
                    <span className="flex-shrink-0 font-mono text-[10px] text-[var(--text-muted)]">{item.line}</span>
                  </button>
                )}
              />
            ) : (
              <div className="px-3 py-2 text-helper text-[var(--text-muted)]">
                {parsePolicy === 'confirm'
                  ? '这是一个较大的文件。确认风险后才会解析并展示结构。'
                  : parsePolicy === 'refused'
                    ? '文件超过解析上限，不会生成结构大纲。'
                    : outlineQuery
                      ? '没有匹配的结构'
                      : !parseEnabled
                        ? '结构解析未启用'
                        : langSupport.outline === 'none'
                          ? langSupport.highlight
                            ? '当前语言仅支持语法高亮，暂无结构大纲。'
                            : '当前文件类型无结构大纲（按纯文本显示）。'
                          : outlineComplete
                            ? '当前文件没有可展示的结构'
                            : '正在解析文件结构…'}
              </div>
            )}
          </div>
        </aside>
      </div>
    </div>
  )
}
