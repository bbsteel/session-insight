import { useEffect, useState } from 'react'

// Local font preference storage and runtime application.
// UI font and terminal font are independent: the UI font may be any installed
// font, while the terminal font must be monospaced (enforced by the picker).
//
// UI font size uses three presets (small/medium/large) that scale the whole UI
// text hierarchy via a single CSS variable. Terminal font size is a per-pixel
// value between 10 and 20.

export const DEFAULT_UI_FONT = 'Inter'
export const DEFAULT_TERMINAL_FONT = 'JetBrains Mono'
export const DEFAULT_UI_FONT_SIZE = 'medium'
export const DEFAULT_TERMINAL_FONT_SIZE = 13

const UI_FONT_KEY = 'si-ui-font'
const TERMINAL_FONT_KEY = 'si-terminal-font'
const UI_FONT_SIZE_KEY = 'si-ui-font-size'
const TERMINAL_FONT_SIZE_KEY = 'si-terminal-font-size'
const FONT_CHANGE_EVENT = 'si-fonts-changed'

export const UI_FONT_SIZE_PRESETS: Record<string, string> = {
  // Body sizes: 12px / 13px / 14px. Other text-* classes scale proportionally.
  small: '0.923',
  medium: '1',
  large: '1.077',
}

export function getUIFont(): string {
  try {
    return localStorage.getItem(UI_FONT_KEY) || DEFAULT_UI_FONT
  } catch {
    return DEFAULT_UI_FONT
  }
}

export function getTerminalFont(): string {
  try {
    return localStorage.getItem(TERMINAL_FONT_KEY) || DEFAULT_TERMINAL_FONT
  } catch {
    return DEFAULT_TERMINAL_FONT
  }
}

export function getUIFontSize(): string {
  try {
    return localStorage.getItem(UI_FONT_SIZE_KEY) || DEFAULT_UI_FONT_SIZE
  } catch {
    return DEFAULT_UI_FONT_SIZE
  }
}

export function getTerminalFontSize(): number {
  try {
    const raw = localStorage.getItem(TERMINAL_FONT_SIZE_KEY)
    if (raw) {
      const n = Number(raw)
      if (!Number.isNaN(n)) return n
    }
    return DEFAULT_TERMINAL_FONT_SIZE
  } catch {
    return DEFAULT_TERMINAL_FONT_SIZE
  }
}

function setCssVariable(name: string, value: string) {
  if (typeof document === 'undefined') return
  document.documentElement.style.setProperty(name, value)
}

function quoteFontName(name: string): string {
  // CSS font-family values must be quoted if they contain spaces or special
  // characters. By always quoting we keep the CSS variable safe for any
  // installed font family name.
  const escaped = name.replace(/\\/g, '\\\\').replace(/"/g, '\\"')
  return `"${escaped}"`
}

export function applyUIFont(font: string) {
  setCssVariable('--font-sans-first', quoteFontName(font))
}

export function applyTerminalFont(font: string) {
  setCssVariable('--font-mono-first', quoteFontName(font))
}

export function applyUIFontSize(size: string) {
  const scale = UI_FONT_SIZE_PRESETS[size] ?? UI_FONT_SIZE_PRESETS[DEFAULT_UI_FONT_SIZE]
  setCssVariable('--ui-font-scale', scale)
}

export function applyTerminalFontSize(size: number) {
  setCssVariable('--terminal-font-size', `${size}px`)
}

export function setUIFont(font: string) {
  try {
    localStorage.setItem(UI_FONT_KEY, font)
  } catch {
    // Storage is optional.
  }
  applyUIFont(font)
  window.dispatchEvent(new Event(FONT_CHANGE_EVENT))
}

export function setTerminalFont(font: string) {
  try {
    localStorage.setItem(TERMINAL_FONT_KEY, font)
  } catch {
    // Storage is optional.
  }
  applyTerminalFont(font)
  window.dispatchEvent(new Event(FONT_CHANGE_EVENT))
}

export function setUIFontSize(size: string) {
  try {
    localStorage.setItem(UI_FONT_SIZE_KEY, size)
  } catch {
    // Storage is optional.
  }
  applyUIFontSize(size)
  window.dispatchEvent(new Event(FONT_CHANGE_EVENT))
}

export function setTerminalFontSize(size: number) {
  try {
    localStorage.setItem(TERMINAL_FONT_SIZE_KEY, String(size))
  } catch {
    // Storage is optional.
  }
  applyTerminalFontSize(size)
  window.dispatchEvent(new Event(FONT_CHANGE_EVENT))
}

export function initFonts(): void {
  applyUIFont(getUIFont())
  applyTerminalFont(getTerminalFont())
  applyUIFontSize(getUIFontSize())
  applyTerminalFontSize(getTerminalFontSize())
}

export function onFontChange(handler: () => void): () => void {
  window.addEventListener(FONT_CHANGE_EVENT, handler)
  return () => window.removeEventListener(FONT_CHANGE_EVENT, handler)
}

export function useTerminalFont(): string {
  const [font, setFont] = useState(getTerminalFont)
  useEffect(() => {
    return onFontChange(() => setFont(getTerminalFont()))
  }, [])
  return font
}

export function useTerminalFontSize(): number {
  const [size, setSize] = useState(getTerminalFontSize)
  useEffect(() => {
    return onFontChange(() => setSize(getTerminalFontSize()))
  }, [])
  return size
}

export function useUIFontSize(): string {
  const [size, setSize] = useState(getUIFontSize)
  useEffect(() => {
    return onFontChange(() => setSize(getUIFontSize()))
  }, [])
  return size
}

export function getTerminalFontStack(first: string): string {
  const escaped = first.replace(/\\/g, '\\\\').replace(/"/g, '\\"')
  return `"${escaped}", "JetBrains Mono", "Consolas", "Menlo", "SF Mono", "Fira Code", monospace`
}

export function getUIFontStack(first: string): string {
  const escaped = first.replace(/\\/g, '\\\\').replace(/"/g, '\\"')
  return `"${escaped}", "Inter", system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", "Microsoft YaHei UI", "Microsoft YaHei", "PingFang SC", sans-serif`
}
