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
import {
  closeSearchPanel,
  findNext,
  findPrevious,
  openSearchPanel,
  SearchQuery,
  search,
  searchKeymap,
  setSearchQuery,
} from '@codemirror/search'
import type { Panel, ViewUpdate } from '@codemirror/view'
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
    fontFamily: "'JetBrains Mono', 'Consolas', 'Menlo', 'SF Mono', 'Fira Code', monospace",
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
  '.cm-panels.cm-panels-top': {
    position: 'absolute',
    inset: '0',
    height: '0',
    overflow: 'visible',
    pointerEvents: 'none',
  },
  '.cm-panel.cm-search': {
    position: 'absolute',
    top: '8px',
    right: '12px',
    zIndex: '10',
    display: 'flex',
    alignItems: 'center',
    gap: '4px',
    boxSizing: 'border-box',
    padding: '4px 8px',
    backgroundColor: 'var(--bg-surface)',
    color: 'var(--text-primary)',
    border: '1px solid var(--border-default)',
    borderRadius: '6px',
    boxShadow: '0 4px 10px rgba(0, 0, 0, 0.24)',
    pointerEvents: 'auto',
  },
  '.cm-search input': {
    boxSizing: 'border-box',
    width: '176px',
    height: '24px',
    padding: '0',
    backgroundColor: 'var(--bg-inset)',
    color: 'var(--text-primary)',
    border: 'none',
    font: 'inherit',
    outline: 'none',
  },
  '.cm-search button': {
    boxSizing: 'border-box',
    minWidth: '24px',
    height: '24px',
    padding: '0 4px',
    border: 'none',
    borderRadius: '4px',
    backgroundColor: 'transparent',
    color: 'var(--text-secondary)',
    font: 'inherit',
    cursor: 'pointer',
  },
  '.cm-search button:hover': {
    backgroundColor: 'var(--bg-surface-hover)',
    color: 'var(--text-primary)',
  },
  '.cm-search button[data-active=true]': {
    backgroundColor: 'var(--accent-blue)',
    color: 'white',
    boxShadow: 'inset 0 2px 3px rgba(0, 0, 0, 0.35)',
  },
  '.cm-search .cm-search-count': {
    minWidth: '52px',
    color: 'var(--text-muted)',
    fontSize: '11px',
    textAlign: 'right',
    fontVariantNumeric: 'tabular-nums',
  },
  '.cm-search .cm-search-count[data-invalid=true]': {
    color: 'var(--error)',
  },
  '&.cm-search-hide-highlights .cm-searchMatch:not(.cm-searchMatch-selected)': {
    backgroundColor: 'transparent !important',
  },
})

/** A CodeMirror search panel styled and operated like the terminal search bar. */
class ReaderSearchPanel implements Panel {
  dom: HTMLElement
  private query: SearchQuery
  private readonly input: HTMLInputElement
  private readonly caseButton: HTMLButtonElement
  private readonly wordButton: HTMLButtonElement
  private readonly regexButton: HTMLButtonElement
  private readonly highlightButton: HTMLButtonElement
  private readonly count: HTMLSpanElement
  private highlightAll = true

  constructor(private readonly view: EditorView) {
    this.query = new SearchQuery({ search: '' })
    this.input = document.createElement('input')
    this.input.placeholder = '在代码中查找'
    this.input.setAttribute('aria-label', '在代码中查找')
    this.input.setAttribute('main-field', 'true')
    this.input.addEventListener('input', () => this.commit())

    this.caseButton = this.optionButton('Aa', '区分大小写', () => ({ caseSensitive: !this.query.caseSensitive }))
    this.wordButton = this.optionButton('wd', '全词匹配', () => ({ wholeWord: !this.query.wholeWord }))
    this.wordButton.style.textDecoration = 'underline'
    this.wordButton.style.textUnderlineOffset = '2px'
    this.regexButton = this.optionButton('.*', '正则表达式', () => ({ regexp: !this.query.regexp }))
    this.highlightButton = this.button('全亮', '高亮全部命中', () => {
      this.highlightAll = !this.highlightAll
      this.highlightButton.dataset.active = String(this.highlightAll)
      this.view.dom.classList.toggle('cm-search-hide-highlights', !this.highlightAll)
    })
    this.highlightButton.dataset.active = 'true'
    this.count = document.createElement('span')
    this.count.className = 'cm-search-count'

    const previous = this.button('↑', '上一个 (Shift+Enter)', () => findPrevious(this.view))
    const next = this.button('↓', '下一个 (Enter)', () => findNext(this.view))
    const close = this.button('✕', '关闭 (Esc)', () => closeSearchPanel(this.view))
    this.dom = document.createElement('div')
    this.dom.className = 'cm-search'
    this.dom.append(this.input, this.caseButton, this.wordButton, this.regexButton, this.highlightButton, this.count, previous, next, close)
    this.dom.addEventListener('keydown', event => {
      if (event.key === 'Enter') {
        event.preventDefault()
        ;(event.shiftKey ? findPrevious : findNext)(this.view)
      } else if (event.key === 'Escape') {
        event.preventDefault()
        closeSearchPanel(this.view)
        this.view.focus()
      }
    })
    this.sync(this.query)
  }

  private button(label: string, title: string, action: () => void): HTMLButtonElement {
    const button = document.createElement('button')
    button.type = 'button'
    button.textContent = label
    button.title = title
    button.addEventListener('click', action)
    return button
  }

  private optionButton(label: string, title: string, change: () => Partial<ConstructorParameters<typeof SearchQuery>[0]>): HTMLButtonElement {
    return this.button(label, title, () => {
      const next = new SearchQuery({
        search: this.query.search,
        caseSensitive: this.query.caseSensitive,
        wholeWord: this.query.wholeWord,
        regexp: this.query.regexp,
        ...change(),
      })
      this.view.dispatch({ effects: setSearchQuery.of(next) })
      if (next.valid) findNext(this.view)
    })
  }

  private commit() {
    // Match @codemirror/search's default SearchPanel: update the query on
    // each keystroke, but do NOT call findNext here. findNext ends with
    // selectSearchInput(), which selects the whole field while it still has
    // focus — so the next typed character replaces the previous one and the
    // box appears stuck at a single letter. Navigation stays on Enter /
    // option toggles (and the match decorations still refresh from the query).
    const next = new SearchQuery({
      search: this.input.value,
      caseSensitive: this.query.caseSensitive,
      wholeWord: this.query.wholeWord,
      regexp: this.query.regexp,
    })
    if (!next.eq(this.query)) {
      this.query = next
      this.view.dispatch({ effects: setSearchQuery.of(next) })
      this.updateCount()
    }
  }

  private sync(query: SearchQuery) {
    this.query = query
    // Avoid clobbering the caret when the query already matches what the
    // user typed (we set this.query in commit before dispatching).
    if (this.input.value !== query.search) this.input.value = query.search
    this.caseButton.dataset.active = String(query.caseSensitive)
    this.wordButton.dataset.active = String(query.wholeWord)
    this.regexButton.dataset.active = String(query.regexp)
    this.updateCount()
  }

  private updateCount() {
    if (!this.query.search) {
      this.count.textContent = ''
      this.count.dataset.invalid = 'false'
      return
    }
    if (!this.query.valid) {
      this.count.textContent = '无效正则'
      this.count.dataset.invalid = 'true'
      return
    }
    let index = -1
    let total = 0
    const selection = this.view.state.selection.main
    const cursor = this.query.getCursor(this.view.state)
    for (let next = cursor.next(); !next.done; next = cursor.next()) {
      const match = next.value
      if (match.from === selection.from && match.to === selection.to) index = total
      total++
    }
    this.count.dataset.invalid = 'false'
    this.count.textContent = total ? `${index >= 0 ? index + 1 : 0}/${total}` : '无结果'
  }

  update(update: ViewUpdate) {
    for (const transaction of update.transactions) {
      for (const effect of transaction.effects) {
        if (effect.is(setSearchQuery) && !effect.value.eq(this.query)) this.sync(effect.value)
      }
    }
    if (update.selectionSet || update.docChanged) this.updateCount()
  }

  mount() { this.input.select() }
  destroy() { this.view.dom.classList.remove('cm-search-hide-highlights') }
  get top() { return true }
}

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
      if (view) {
        // searchKeymap's Mod+F command opens this same custom panel.
        // Calling it here keeps the header button and keyboard shortcut aligned.
        openSearchPanel(view)
      }
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
        search({ top: true, createPanel: view => new ReaderSearchPanel(view) }),
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
