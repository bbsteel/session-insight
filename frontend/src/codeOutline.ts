import type { Tree, SyntaxNode } from '@lezer/common'

export type OutlineLanguage = 'python' | 'java' | 'markdown' | 'go' | 'javascript' | 'rust' | 'ruby' | 'csharp'
export type OutlineKind = 'class' | 'interface' | 'enum' | 'function' | 'method' | 'constructor' | 'heading'

export interface OutlineItem {
  id: string
  name: string
  kind: OutlineKind
  from: number
  to: number
  line: number
  children: OutlineItem[]
}

function lineStarts(content: string): number[] {
  const starts = [0]
  for (let i = 0; i < content.length; i++) {
    if (content.charCodeAt(i) === 10) starts.push(i + 1)
  }
  return starts
}

function lineAt(starts: number[], offset: number): number {
  let lo = 0
  let hi = starts.length
  while (lo + 1 < hi) {
    const mid = (lo + hi) >>> 1
    if (starts[mid] <= offset) lo = mid
    else hi = mid
  }
  return lo + 1
}

function childText(node: SyntaxNode, childName: string, content: string): string {
  const child = node.getChild(childName)
  return child ? content.slice(child.from, child.to) : ''
}

function hasAncestor(node: SyntaxNode, name: string): boolean {
  for (let p = node.parent; p; p = p.parent) {
    if (p.name === name) return true
  }
  return false
}

function codeSymbol(node: SyntaxNode, language: Exclude<OutlineLanguage, 'markdown'>, content: string): { name: string; kind: OutlineKind } | null {
  if (language === 'python') {
    if (node.name === 'ClassDefinition') return { name: childText(node, 'VariableName', content), kind: 'class' }
    if (node.name === 'FunctionDefinition') {
      const inClass = node.parent?.parent?.name === 'ClassDefinition'
      return { name: childText(node, 'VariableName', content), kind: inClass ? 'method' : 'function' }
    }
    return null
  }

  if (language === 'go') {
    if (node.name === 'FunctionDecl') return { name: childText(node, 'DefName', content), kind: 'function' }
    if (node.name === 'MethodDecl') return { name: childText(node, 'FieldName', content), kind: 'method' }
    if (node.name === 'TypeSpec') {
      const name = childText(node, 'DefName', content)
      if (node.getChild('InterfaceType')) return { name, kind: 'interface' }
      return { name, kind: 'class' }
    }
    if (node.name === 'MethodElem') return { name: childText(node, 'FieldName', content), kind: 'method' }
    return null
  }

  if (language === 'javascript') {
    if (node.name === 'FunctionDeclaration') return { name: childText(node, 'VariableDefinition', content), kind: 'function' }
    if (node.name === 'ClassDeclaration') return { name: childText(node, 'VariableDefinition', content), kind: 'class' }
    if (node.name === 'MethodDeclaration') {
      const name = childText(node, 'PropertyDefinition', content)
      return { name, kind: name === 'constructor' ? 'constructor' : 'method' }
    }
    if (node.name === 'InterfaceDeclaration') return { name: childText(node, 'TypeDefinition', content), kind: 'interface' }
    if (node.name === 'EnumDeclaration') return { name: childText(node, 'TypeDefinition', content), kind: 'enum' }
    return null
  }

  if (language === 'rust') {
    if (node.name === 'FunctionItem') {
      const name = childText(node, 'BoundIdentifier', content)
      return { name, kind: hasAncestor(node, 'ImplItem') ? 'method' : 'function' }
    }
    if (node.name === 'StructItem') return { name: childText(node, 'TypeIdentifier', content), kind: 'class' }
    if (node.name === 'EnumItem') return { name: childText(node, 'TypeIdentifier', content), kind: 'enum' }
    if (node.name === 'ImplItem') return { name: childText(node, 'TypeIdentifier', content), kind: 'class' }
    return null
  }

  if (language === 'ruby') {
    if (node.name === 'ClassDef') return { name: childText(node, 'Constant', content), kind: 'class' }
    // Modules share class-like outline slots (no separate "module" kind).
    if (node.name === 'ModuleDef') return { name: childText(node, 'Constant', content), kind: 'class' }
    if (node.name === 'MethodDef') {
      const name = childText(node, 'Identifier', content)
      const nested = hasAncestor(node, 'ClassDef') || hasAncestor(node, 'ModuleDef')
      return { name, kind: nested ? 'method' : 'function' }
    }
    return null
  }

  // csharp uses a separate token-level walk (Replit grammar rarely emits
  // structural classDecl/methodDecl nodes).

  // Java (and similar Definition-named grammars)
  const name = childText(node, 'Definition', content)
  if (node.name === 'ClassDeclaration') return { name, kind: 'class' }
  if (node.name === 'InterfaceDeclaration') return { name, kind: 'interface' }
  if (node.name === 'EnumDeclaration') return { name, kind: 'enum' }
  if (node.name === 'MethodDeclaration') return { name, kind: 'method' }
  if (node.name === 'ConstructorDeclaration') return { name, kind: 'constructor' }
  return null
}

function makeItem(kind: OutlineKind, name: string, from: number, to: number, starts: number[]): OutlineItem {
  return {
    id: `${kind}:${from}:${name}`,
    name,
    kind,
    from,
    to,
    line: lineAt(starts, from),
    children: [],
  }
}

// @replit/codemirror-lang-csharp often fails to build classDecl/methodDecl
// nodes (error recovery leaves Keyword + TypeIdentifier + MethodName). Walk
// those tokens for a flat/approx-nested outline.
function csharpOutline(tree: Tree, content: string, starts: number[]): OutlineItem[] {
  const root: OutlineItem[] = []
  let pending: OutlineKind | 'skip-type' | null = null
  let currentType: OutlineItem | null = null

  tree.iterate({
    enter(node) {
      if (node.name === 'Keyword') {
        const kw = content.slice(node.from, node.to)
        if (kw === 'class' || kw === 'struct' || kw === 'record') pending = 'class'
        else if (kw === 'interface') pending = 'interface'
        else if (kw === 'enum') pending = 'enum'
        else if (kw === 'namespace') pending = 'skip-type'
        else if (pending !== 'skip-type') pending = null
        return
      }
      if (node.name === 'TypeIdentifier' && pending) {
        if (pending === 'skip-type') {
          pending = null
          return
        }
        const name = content.slice(node.from, node.to)
        if (!name) {
          pending = null
          return
        }
        const item = makeItem(pending, name, node.from, node.to, starts)
        root.push(item)
        currentType = item
        pending = null
        return
      }
      if (node.name === 'MethodName') {
        const name = content.slice(node.from, node.to)
        if (!name) return
        const item = makeItem('method', name, node.from, node.to, starts)
        if (currentType) currentType.children.push(item)
        else root.push(item)
      }
    },
  })
  return root
}

function codeOutline(tree: Tree, content: string, language: Exclude<OutlineLanguage, 'markdown'>, starts: number[]): OutlineItem[] {
  if (language === 'csharp') return csharpOutline(tree, content, starts)

  const root: OutlineItem[] = []

  const visit = (node: SyntaxNode, parent: OutlineItem[]) => {
    const symbol = codeSymbol(node, language, content)
    let children = parent
    if (symbol?.name) {
      const item = makeItem(symbol.kind, symbol.name, node.from, node.to, starts)
      parent.push(item)
      children = item.children
    }
    for (let child = node.firstChild; child; child = child.nextSibling) visit(child, children)
  }

  visit(tree.topNode, root)
  return root
}

function markdownOutline(tree: Tree, content: string, starts: number[]): OutlineItem[] {
  const root: OutlineItem[] = []
  const stack: { level: number; children: OutlineItem[] }[] = [{ level: 0, children: root }]

  const cursor = tree.cursor()
  do {
    const match = /^(?:ATXHeading|SetextHeading)([1-6])$/.exec(cursor.name)
    if (!match) continue
    const level = Number(match[1])
    const raw = content.slice(cursor.from, cursor.to)
    const name = raw.split('\n', 1)[0]
      .replace(/^\s{0,3}#{1,6}\s*/, '')
      .replace(/\s+#+\s*$/, '')
      .trim()
    if (!name) continue
    while (stack.length > 1 && stack[stack.length - 1].level >= level) stack.pop()
    const item: OutlineItem = {
      id: `heading:${cursor.from}:${name}`,
      name,
      kind: 'heading',
      from: cursor.from,
      to: cursor.to,
      line: lineAt(starts, cursor.from),
      children: [],
    }
    stack[stack.length - 1].children.push(item)
    stack.push({ level, children: item.children })
  } while (cursor.next())
  return root
}

export function outlineFromTree(tree: Tree, content: string, language: OutlineLanguage): OutlineItem[] {
  const starts = lineStarts(content)
  return language === 'markdown'
    ? markdownOutline(tree, content, starts)
    : codeOutline(tree, content, language, starts)
}

export function flattenOutline(items: OutlineItem[]): OutlineItem[] {
  const flat: OutlineItem[] = []
  const visit = (nodes: OutlineItem[]) => {
    for (const node of nodes) {
      flat.push(node)
      visit(node.children)
    }
  }
  visit(items)
  return flat
}

export function activeOutlineId(items: OutlineItem[], position: number): string | null {
  let activeId: string | null = null
  const visit = (nodes: OutlineItem[]) => {
    for (const node of nodes) {
      if (node.from <= position && position <= node.to) {
        activeId = node.id
        visit(node.children)
      }
    }
  }
  visit(items)
  return activeId
}
