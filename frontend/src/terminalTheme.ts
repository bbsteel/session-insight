import { useEffect, useState } from 'react'
import type { ITheme } from '@xterm/xterm'

// Terminal color themes.
//
// The backend emits *indexed* ANSI colors (\x1b[38;5;Nm / \x1b[48;5;Nm, N=0-15)
// rather than truecolor, so the actual RGB for each slot is resolved here on the
// client. Switching theme is therefore a palette swap on the live xterm instance
// (term.options.theme = …) — instant, no network round-trip, no re-fetch of the
// ANSI stream, and the position cache stays valid.
//
// Slot assignment mirrors the backend (internal/render/theme.go) and maps onto
// xterm's 16 named ANSI colors (0-7 = black..white, 8-15 = bright*):
//   1 error      -> red          6 subagent   -> cyan
//   2 success    -> green        7 fg         -> white
//   3 warning    -> yellow       8 muted      -> brightBlack
//   4 tool       -> blue         9 diffDelBg  -> brightRed
//   5 skill      -> magenta     10 diffAddBg  -> brightGreen
//
// Slots 9/10 hold diff line *backgrounds*; we only ever emit them via 48;5;N,
// never as foreground, so their "bright red/green" names being dark tints is
// fine — this is our private semantic palette, both ends controlled by us.
//
// Palette values are sourced from Claude Code's own "Dark mode" / "Light mode"
// themes (claude terracotta #d77757 for the sub-agent accent, etc.).

export type TerminalThemeName = 'dark' | 'light'

const DARK: ITheme = {
  background: '#1a1b26',
  foreground: '#e6e6e6',
  cursor: '#e6e6e6',
  cursorAccent: '#1a1b26',
  selectionBackground: 'rgba(122,162,247,0.35)',

  black: '#1a1b26',
  red: '#ff6b80', // 1 error
  green: '#4eba65', // 2 success / user
  yellow: '#ffc107', // 3 warning
  blue: '#93a5ff', // 4 tool
  magenta: '#af87ff', // 5 skill
  cyan: '#d77757', // 6 subagent (Claude terracotta)
  white: '#e6e6e6', // 7 default fg

  brightBlack: '#999999', // 8 muted
  brightRed: '#7a2936', // 9 diff deleted line bg
  brightGreen: '#225c2b', // 10 diff added line bg
  brightYellow: '#ffc107', // 11 spare
  brightBlue: '#93a5ff', // 12 spare
  brightMagenta: '#af87ff', // 13 spare
  brightCyan: '#d77757', // 14 spare
  brightWhite: '#93a5ff', // 15 bold fg — matches Claude Code bold blue
}

const LIGHT: ITheme = {
  background: '#f7f8fa',
  foreground: '#1a1a1a',
  cursor: '#1a1a1a',
  cursorAccent: '#f7f8fa',
  selectionBackground: 'rgba(87,105,247,0.25)',

  black: '#f7f8fa',
  red: '#ab2b3f', // 1 error
  green: '#2c7a39', // 2 success / user
  yellow: '#966c1e', // 3 warning
  blue: '#5769f7', // 4 tool
  magenta: '#8700ff', // 5 skill
  cyan: '#d77757', // 6 subagent (Claude terracotta)
  white: '#1a1a1a', // 7 default fg

  brightBlack: '#666666', // 8 muted
  brightRed: '#fdd2d8', // 9 diff deleted line bg (pale)
  brightGreen: '#c7e1cb', // 10 diff added line bg (pale)
  brightYellow: '#966c1e', // 11 spare
  brightBlue: '#5769f7', // 12 spare
  brightMagenta: '#8700ff', // 13 spare
  brightCyan: '#d77757', // 14 spare
  brightWhite: '#5769f7', // 15 bold fg — matches Claude Code bold blue
}

const THEMES: Record<TerminalThemeName, ITheme> = { dark: DARK, light: LIGHT }

export function terminalTheme(isDark: boolean): ITheme {
  return THEMES[isDark ? 'dark' : 'light']
}

// useIsDark tracks the global theme by observing the `.dark` class that
// ThemeToggle toggles on <html>, so the terminal re-skins in sync with the UI
// without any prop drilling.
export function useIsDark(): boolean {
  const [isDark, setIsDark] = useState(() =>
    document.documentElement.classList.contains('dark'),
  )
  useEffect(() => {
    const el = document.documentElement
    const observer = new MutationObserver(() => {
      setIsDark(el.classList.contains('dark'))
    })
    observer.observe(el, { attributes: true, attributeFilter: ['class'] })
    return () => observer.disconnect()
  }, [])
  return isDark
}
