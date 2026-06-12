import { useState, useEffect } from 'react'

export default function ThemeToggle() {
  const [dark, setDark] = useState(() => {
    const stored = localStorage.getItem('session-insight-theme')
    if (stored) return stored === 'dark'
    return false // default light
  })

  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark)
    localStorage.setItem('session-insight-theme', dark ? 'dark' : 'light')
  }, [dark])

  return (
    <button
      onClick={() => setDark(d => !d)}
      className="w-6 h-6 flex items-center justify-center rounded-sm hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast text-nav"
      title={dark ? 'Switch to light mode' : 'Switch to dark mode'}
    >
      {dark ? '☀' : '☾'}
    </button>
  )
}
