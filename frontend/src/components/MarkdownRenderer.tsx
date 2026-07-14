import { lazy, Suspense } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

interface Props {
  content: string
}

const CodeBlock = lazy(() => import('./SyntaxCodeBlock'))

export default function MarkdownRenderer({ content }: Props) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      components={{
        code({ className, children, ...props }) {
          const match = /language-(\w+)/.exec(className || '')
          const codeStr = String(children).replace(/\n$/, '')
          // Fenced blocks without a language tag still contain newlines —
          // render them as real code blocks, not a multi-line inline lump.
          const isBlock = match !== null || String(children).includes('\n')
          if (isBlock) {
            return (
              <Suspense fallback={<pre className="rounded-sm bg-[var(--code-bg)] p-3 text-meta text-[var(--code-text)] overflow-x-auto"><code>{codeStr}</code></pre>}>
                <CodeBlock code={codeStr} language={match?.[1] ?? 'text'} />
              </Suspense>
            )
          }
          return (
            <code className="bg-[var(--code-bg)] px-1 py-0.5 rounded-sm text-meta font-mono text-[var(--code-text)]" {...props}>
              {children}
            </code>
          )
        },
      }}
    >
      {content}
    </ReactMarkdown>
  )
}
