import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter'
import { oneDark } from 'react-syntax-highlighter/dist/esm/styles/prism'

interface Props {
  content: string
}

export default function MarkdownRenderer({ content }: Props) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      components={{
        code({ className, children, ...props }) {
          const match = /language-(\w+)/.exec(className || '')
          const codeStr = String(children).replace(/\n$/, '')
          const isInline = !match && !String(children).includes('\n')
          if (match && !isInline) {
            return (
              <SyntaxHighlighter
                style={oneDark}
                language={match[1]}
                PreTag="div"
                customStyle={{ borderRadius: '4px', fontSize: '13px', margin: '8px 0' }}
              >
                {codeStr}
              </SyntaxHighlighter>
            )
          }
          return (
            <code className="bg-[var(--code-bg)] px-1 py-0.5 rounded-sm text-meta font-mono" {...props}>
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
