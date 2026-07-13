import { forwardRef, useEffect, useImperativeHandle, useRef } from 'react'
import { EditorState, type Extension } from '@codemirror/state'
import {
  drawSelection,
  EditorView,
  highlightActiveLine,
  highlightActiveLineGutter,
  highlightSpecialChars,
  keymap,
  lineNumbers,
} from '@codemirror/view'
import {
  bracketMatching,
  defaultHighlightStyle,
  foldGutter,
  foldKeymap,
  forceParsing,
  syntaxHighlighting,
  syntaxTree,
} from '@codemirror/language'
import { openSearchPanel, searchKeymap } from '@codemirror/search'
import { oneDark } from '@codemirror/theme-one-dark'
import { outlineFromTree, type OutlineItem, type OutlineLanguage } from '../codeOutline'
import { useIsDark } from '../terminalTheme'

export interface CodeReaderHandle {
  reveal: (offset: number) => void
  search: () => void
}

interface LanguageConfig {
  extension: Extension
  outline: OutlineLanguage | null
  wrap: boolean
}

async function languageForPath(path: string, parseLanguage: boolean): Promise<LanguageConfig> {
  const ext = path.split('.').pop()?.toLowerCase()
  if (!parseLanguage) return { extension: [], outline: null, wrap: ext === 'md' || ext === 'markdown' }
  if (ext === 'py' || ext === 'pyw') {
    const { python } = await import('@codemirror/lang-python')
    return { extension: python(), outline: 'python', wrap: false }
  }
  if (ext === 'java') {
    const { java } = await import('@codemirror/lang-java')
    return { extension: java(), outline: 'java', wrap: false }
  }
  if (ext === 'md' || ext === 'markdown') {
    const { markdown } = await import('@codemirror/lang-markdown')
    return { extension: markdown(), outline: 'markdown', wrap: true }
  }
  return { extension: [], outline: null, wrap: false }
}

const readerTheme = EditorView.theme({
  '&': {
    height: '100%',
    backgroundColor: 'var(--bg-primary)',
    color: 'var(--text-primary)',
    fontSize: '13px',
  },
  '&.cm-focused': { outline: 'none' },
  '.cm-scroller': {
    fontFamily: "'JetBrains Mono', 'SF Mono', 'Fira Code', monospace",
    lineHeight: '1.55',
    overflow: 'auto',
  },
  '.cm-content': { padding: '8px 0 40px' },
  '.cm-line': { padding: '0 12px' },
  '.cm-gutters': {
    backgroundColor: 'var(--bg-surface)',
    color: 'var(--text-muted)',
    borderRight: '1px solid var(--border-muted)',
  },
  '.cm-activeLine, .cm-activeLineGutter': { backgroundColor: 'rgba(37, 99, 235, 0.08)' },
  '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': { backgroundColor: 'rgba(37, 99, 235, 0.22) !important' },
  '.cm-search': {
    backgroundColor: 'var(--bg-surface)',
    color: 'var(--text-primary)',
    borderTop: '1px solid var(--border-default)',
  },
  '.cm-search input': {
    backgroundColor: 'var(--bg-inset)',
    color: 'var(--text-primary)',
    border: '1px solid var(--border-default)',
  },
})

interface Props {
  path: string
  content: string
  initialLine?: number
  parseLanguage: boolean
  onOutline: (outline: OutlineItem[], complete: boolean) => void
  onActivePosition: (position: number) => void
}

function semanticPosition(view: EditorView, position: number): number {
  const line = view.state.doc.lineAt(position)
  const firstCode = line.text.search(/\S/)
  return firstCode >= 0 ? line.from + firstCode : position
}

const CodeReader = forwardRef<CodeReaderHandle, Props>(function CodeReader(
  { path, content, initialLine, parseLanguage, onOutline, onActivePosition },
  ref,
) {
  const hostRef = useRef<HTMLDivElement>(null)
  const viewRef = useRef<EditorView | null>(null)
  const isDark = useIsDark()

  useImperativeHandle(ref, () => ({
    reveal(offset: number) {
      const view = viewRef.current
      if (!view) return
      view.dispatch({
        selection: { anchor: offset },
        effects: EditorView.scrollIntoView(offset, { y: 'start', yMargin: 12 }),
      })
      view.focus()
    },
    search() {
      const view = viewRef.current
      if (view) openSearchPanel(view)
    },
  }), [])

  useEffect(() => {
    let disposed = false
    let parseTimer: number | undefined
    const host = hostRef.current
    if (!host) return

    void languageForPath(path, parseLanguage).then(config => {
      if (disposed) return
      const extensions: Extension[] = [
        lineNumbers(),
        foldGutter(),
        highlightSpecialChars(),
        drawSelection(),
        highlightActiveLine(),
        highlightActiveLineGutter(),
        bracketMatching(),
        syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
        keymap.of([...searchKeymap, ...foldKeymap]),
        EditorState.readOnly.of(true),
        EditorView.editable.of(false),
        EditorView.contentAttributes.of({ 'aria-label': '代码阅读器' }),
        EditorView.updateListener.of(update => {
          if (update.selectionSet) onActivePosition(semanticPosition(update.view, update.state.selection.main.head))
          else if (update.viewportChanged) onActivePosition(semanticPosition(update.view, update.view.viewport.from))
        }),
        config.extension,
        config.wrap ? EditorView.lineWrapping : [],
        isDark ? oneDark : [],
        readerTheme,
      ]
      const state = EditorState.create({ doc: content, extensions })
      const view = new EditorView({ state, parent: host })
      viewRef.current = view
      onActivePosition(semanticPosition(view, view.viewport.from))

      if (initialLine && initialLine > 0 && initialLine <= view.state.doc.lines) {
        const offset = view.state.doc.line(initialLine).from
        requestAnimationFrame(() => {
          if (!disposed) {
            view.dispatch({ selection: { anchor: offset }, effects: EditorView.scrollIntoView(offset, { y: 'center' }) })
          }
        })
      }

      if (!config.outline) {
        onOutline([], true)
        return
      }

      // Parse in short slices so the viewport becomes interactive immediately.
      // Extract once at completion: repeatedly walking a growing 2 MiB tree
      // costs substantially more than the incremental parse itself.
      const advanceOutline = () => {
        if (disposed) return
        const complete = forceParsing(view, view.state.doc.length, 30)
        if (complete) onOutline(outlineFromTree(syntaxTree(view.state), content, config.outline!), true)
        else parseTimer = window.setTimeout(advanceOutline, 16)
      }
      parseTimer = window.setTimeout(advanceOutline, 0)
    })

    return () => {
      disposed = true
      if (parseTimer !== undefined) window.clearTimeout(parseTimer)
      viewRef.current?.destroy()
      viewRef.current = null
      host.replaceChildren()
    }
  }, [content, path, initialLine, parseLanguage, isDark, onOutline, onActivePosition])

  return <div ref={hostRef} className="h-full min-h-0 overflow-hidden" />
})

export default CodeReader
