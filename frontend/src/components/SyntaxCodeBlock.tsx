import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter'
import { oneDark, oneLight } from 'react-syntax-highlighter/dist/esm/styles/prism'
import { useIsDark } from '../terminalTheme'

interface Props {
  code: string
  language: string
}

export default function SyntaxCodeBlock({ code, language }: Props) {
  const isDark = useIsDark()
  return (
    <SyntaxHighlighter
      style={isDark ? oneDark : oneLight}
      language={language}
      PreTag="div"
      customStyle={{ borderRadius: '4px', fontSize: '13px', margin: '8px 0' }}
    >
      {code}
    </SyntaxHighlighter>
  )
}
