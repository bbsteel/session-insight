import React from 'react'
import ReactDOM from 'react-dom/client'
import '@fontsource/inter/400.css'
import '@fontsource/inter/500.css'
import '@fontsource/inter/600.css'
import '@fontsource/inter/700.css'
import '@fontsource/jetbrains-mono/400.css'
import '@fontsource/jetbrains-mono/500.css'
import '@xterm/xterm/css/xterm.css'
import App from './App'
import './app.css'
import { initTheme } from './theme'

// Apply theme before first paint (default light; stored preference wins).
initTheme()

// macOS/iOS: enable grayscale font smoothing (ClearType on Windows must stay default)
if (typeof navigator !== 'undefined' && /Mac|iPhone|iPad|iPod/i.test(navigator.userAgent)) {
  document.documentElement.classList.add('platform-apple')
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
