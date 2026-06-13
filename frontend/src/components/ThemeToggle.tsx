import { useState, useEffect } from 'react'

export default function ThemeToggle() {
  const [dark, setDark] = useState(() => {
    const stored = localStorage.getItem('recap-theme') || localStorage.getItem('session-insight-theme')
    if (stored) return stored === 'dark'
    return true
  })

  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark)
    localStorage.setItem('recap-theme', dark ? 'dark' : 'light')
  }, [dark])

  return (
    <button
      onClick={() => setDark(d => !d)}
      className="w-7 h-7 flex items-center justify-center rounded-md hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast text-nav focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg-primary)]"
      title={dark ? '切换到亮色' : '切换到暗色'}
      aria-label={dark ? '切换到亮色' : '切换到暗色'}
    >
      {dark ? '☀' : '☾'}
    </button>
  )
}
