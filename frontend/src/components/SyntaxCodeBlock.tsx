import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter'
import { oneDark } from 'react-syntax-highlighter/dist/esm/styles/prism'

interface Props {
  code: string
  language: string
}

export default function SyntaxCodeBlock({ code, language }: Props) {
  return (
    <SyntaxHighlighter
      style={oneDark}
      language={language}
      PreTag="div"
      customStyle={{ borderRadius: '4px', fontSize: '13px', margin: '8px 0' }}
    >
      {code}
    </SyntaxHighlighter>
  )
}
