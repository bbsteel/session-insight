import { useEffect, useRef, useState } from 'react'
import { fetchSettings, saveSettings } from '../api'
import { getNavOpenPref, setNavOpenPref, type NavOpenPref } from '../navPrefs'
import { AVATAR_MAX_BYTES, setUserAvatar } from '../userAvatar'
import {
  DEFAULT_TERMINAL_FONT,
  DEFAULT_TERMINAL_FONT_SIZE,
  DEFAULT_UI_FONT,
  DEFAULT_UI_FONT_SIZE,
  getTerminalFont,
  getTerminalFontSize,
  getUIFont,
  getUIFontSize,
  onFontChange,
  setTerminalFont as setTerminalFontPref,
  setTerminalFontSize as setTerminalFontSizePref,
  setUIFont as setUIFontPref,
  setUIFontSize as setUIFontSizePref,
} from '../fontPrefs'
import {
  defaultBannerColor,
  getBannerColorOverride,
  setBannerColorOverride,
  useIsDark,
} from '../terminalTheme'
import FontPicker from './FontPicker'
import { ThemeSelect } from './ThemeToggle'
import UserAvatar, { useUserAvatar } from './UserAvatar'
import {
  AppearanceIcon,
  EditorIcon,
  FontIcon,
  NavigationIcon,
  SearchIcon,
  SparklesIcon,
  TerminalIcon,
} from './icons'

interface Props {
  open: boolean
  onClose: () => void
  historyLimit: number
  onHistoryLimitChange: (n: number) => void
  onClearHistory: () => void
  onOpenAISettings: () => void
}

type TabId = 'appearance' | 'navigation' | 'search' | 'terminal' | 'fonts' | 'editor' | 'ai'

interface TabDef {
  id: TabId
  label: string
  icon: React.ComponentType<{ className?: string }>
}

const TABS: TabDef[] = [
  { id: 'appearance', label: '外观', icon: AppearanceIcon },
  { id: 'navigation', label: '会话导航', icon: NavigationIcon },
  { id: 'search', label: '全文搜索', icon: SearchIcon },
  { id: 'terminal', label: '终端外观', icon: TerminalIcon },
  { id: 'fonts', label: '字体', icon: FontIcon },
  { id: 'editor', label: '文件查看器', icon: EditorIcon },
  { id: 'ai', label: 'AI', icon: SparklesIcon },
]

const HISTORY_LIMIT_MIN = 1
const HISTORY_LIMIT_MAX = 50

export default function SettingsDialog({
  open,
  onClose,
  historyLimit,
  onHistoryLimitChange,
  onClearHistory,
  onOpenAISettings,
}: Props) {
  const [activeTab, setActiveTab] = useState<TabId>('appearance')
  const [loading, setLoading] = useState(false)
  const [editorCommand, setEditorCommand] = useState('')
  const [editorCommandDefault, setEditorCommandDefault] = useState('')
  const savedEditorCommandRef = useRef('')
  const [fileExts, setFileExts] = useState('')
  const savedFileExtsRef = useRef('')
  const [tsKinds, setTsKinds] = useState<string[]>([])
  const [navOpenPref, setNavOpenPrefState] = useState<NavOpenPref>(getNavOpenPref)
  const [uiFont, setUiFont] = useState(getUIFont)
  const [uiFontSize, setUiFontSize] = useState(getUIFontSize)
  const [terminalFont, setTerminalFont] = useState(getTerminalFont)
  const [terminalFontSize, setTerminalFontSize] = useState(getTerminalFontSize)
  const isDark = useIsDark()
  const [bannerColor, setBannerColor] = useState<string | null>(getBannerColorOverride)
  const [drag, setDrag] = useState({ x: 0, y: 0 })
  const dragStartRef = useRef({ x: 0, y: 0 })
  const dragCleanupRef = useRef<(() => void) | null>(null)
  const userAvatar = useUserAvatar()
  const avatarFileRef = useRef<HTMLInputElement>(null)
  // 单调递增的读取序号:连续快速选择两张图时,先开始的 FileReader 可能
  // 后完成,序号过期即丢弃,避免旧读取覆盖新选择(或覆盖中途的恢复默认)。
  const avatarReadSeq = useRef(0)
  const [avatarError, setAvatarError] = useState('')

  // 上传自定义头像:仅校验类型和大小(≤200KB),原图以 data URL 存本地。
  const handleAvatarFile = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    e.target.value = '' // 允许再次选择同一文件
    if (!file) return
    if (!file.type.startsWith('image/')) {
      setAvatarError('请选择图像文件')
      return
    }
    if (file.size > AVATAR_MAX_BYTES) {
      setAvatarError(`图像大小 ${Math.ceil(file.size / 1024)}KB,超过 200KB 上限,请压缩后再上传`)
      return
    }
    const readId = ++avatarReadSeq.current
    const reader = new FileReader()
    reader.onload = () => {
      if (readId !== avatarReadSeq.current) return
      if (setUserAvatar(typeof reader.result === 'string' ? reader.result : null)) {
        setAvatarError('')
      } else {
        setAvatarError('头像保存失败：浏览器本地存储不可用或已满')
      }
    }
    reader.onerror = () => {
      if (readId !== avatarReadSeq.current) return
      setAvatarError('读取文件失败,请重试')
    }
    reader.readAsDataURL(file)
  }

  const handleAvatarReset = () => {
    avatarReadSeq.current++ // 使进行中的读取失效,避免覆盖本次还原
    if (setUserAvatar(null)) {
      setAvatarError('')
    } else {
      setAvatarError('头像保存失败：浏览器本地存储不可用或已满')
    }
  }

  // Load server-side settings whenever the dialog opens.
  useEffect(() => {
    if (!open) return
    setActiveTab('appearance')
    setDrag({ x: 0, y: 0 })
    setLoading(true)
    setNavOpenPrefState(getNavOpenPref())
    setBannerColor(getBannerColorOverride())
    setAvatarError('')
    setUiFont(getUIFont())
    setUiFontSize(getUIFontSize())
    setTerminalFont(getTerminalFont())
    setTerminalFontSize(getTerminalFontSize())
    fetchSettings()
      .then(s => {
        setEditorCommand(s.editor_command)
        setEditorCommandDefault(s.editor_command_default)
        savedEditorCommandRef.current = s.editor_command
        setFileExts(s.file_open_extensions ?? '')
        savedFileExtsRef.current = s.file_open_extensions ?? ''
        setTsKinds((s.timestamp_kinds ?? '').split(',').filter(Boolean))
      })
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [open])

  // Keep local font state in sync when changed from another tab or component.
  useEffect(() => {
    return onFontChange(() => {
      setUiFont(getUIFont())
      setUiFontSize(getUIFontSize())
      setTerminalFont(getTerminalFont())
      setTerminalFontSize(getTerminalFontSize())
    })
  }, [])

  // Close on Escape.
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  // Remove drag listeners if the dialog unmounts mid-drag.
  useEffect(() => {
    return () => dragCleanupRef.current?.()
  }, [])

  const startDrag = (e: React.PointerEvent) => {
    if (e.button !== 0) return
    const target = e.target as HTMLElement
    if (target.closest('button, a, input, select, textarea')) return
    e.preventDefault()
    try {
      ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
    } catch {
      // Capture may not be supported; fall back to window listeners.
    }
    dragStartRef.current = { x: e.clientX - drag.x, y: e.clientY - drag.y }

    const onMove = (ev: PointerEvent) => {
      setDrag({ x: ev.clientX - dragStartRef.current.x, y: ev.clientY - dragStartRef.current.y })
    }
    const cleanup = () => {
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
      window.removeEventListener('pointercancel', onUp)
    }
    const onUp = () => {
      cleanup()
      dragCleanupRef.current = null
    }
    dragCleanupRef.current = cleanup
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
    window.addEventListener('pointercancel', onUp)
  }

  const toggleTsKind = (kind: string) => {
    const next = tsKinds.includes(kind) ? tsKinds.filter(k => k !== kind) : [...tsKinds, kind]
    setTsKinds(next)
    void saveSettings({ timestamp_kinds: next.join(',') })
      .then(() => window.dispatchEvent(new Event('si-settings-changed')))
      .catch(() => setTsKinds(tsKinds))
  }

  const persistEditorCommand = async () => {
    const value = editorCommand.trim()
    if (value === savedEditorCommandRef.current) return
    try {
      await saveSettings({ editor_command: value })
      savedEditorCommandRef.current = value
    } catch {
      setEditorCommand(savedEditorCommandRef.current)
    }
  }

  const persistFileExts = async () => {
    const value = fileExts.trim()
    if (value === savedFileExtsRef.current) return
    try {
      await saveSettings({ file_open_extensions: value })
      savedFileExtsRef.current = value
    } catch {
      setFileExts(savedFileExtsRef.current)
    }
  }

  const handleNavOpenPref = (next: NavOpenPref) => {
    setNavOpenPref(next)
    setNavOpenPrefState(next)
  }

  const handleHistoryLimit = (raw: number) => {
    const n = Math.min(
      HISTORY_LIMIT_MAX,
      Math.max(HISTORY_LIMIT_MIN, Math.round(raw) || historyLimit),
    )
    onHistoryLimitChange(n)
  }

  const handleOpenAISettings = () => {
    onOpenAISettings()
    onClose()
  }

  const inputCls =
    'h-7 rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] px-2 text-helper text-[var(--text-primary)] placeholder:text-[var(--text-muted)] transition-colors duration-fast hover:border-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none focus:ring-1 focus:ring-[var(--accent-blue)]'
  const selectCls =
    'h-7 rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] px-2 text-helper text-[var(--text-primary)] transition-colors duration-fast hover:border-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none focus:ring-1 focus:ring-[var(--accent-blue)]'
  const btnCls =
    'h-7 rounded-md border border-[var(--border-default)] px-2.5 text-helper text-[var(--text-secondary)] transition-colors duration-fast hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] disabled:opacity-50'
  const primaryBtnCls =
    'h-7 rounded-md border border-[var(--accent-blue)] px-2.5 text-helper font-medium text-[var(--accent-blue)] transition-colors duration-fast hover:bg-[color-mix(in_srgb,var(--accent-blue)_10%,transparent)] disabled:opacity-50'
  const dangerBtnCls =
    'h-7 rounded-md border border-[var(--error)] px-2.5 text-helper text-[var(--error)] transition-colors duration-fast hover:bg-[color-mix(in_srgb,var(--error)_10%,transparent)] disabled:opacity-50'
  const checkboxCls = 'h-3.5 w-3.5 accent-[var(--accent-blue)] cursor-pointer'
  const sectionTitle = 'text-helper font-medium text-[var(--text-primary)]'
  const sectionDesc = 'mt-0.5 text-meta text-[var(--text-muted)]'
  const sectionBox = 'rounded-lg border border-[var(--border-muted)] bg-[var(--bg-surface)] p-4'

  const activeTabDef = TABS.find(t => t.id === activeTab) ?? TABS[0]

  if (!open) return null

  return (
    <div
      className="fixed inset-0 z-[390] flex items-center justify-center bg-black/50"
      onClick={onClose}
      role="dialog"
      aria-modal="true"
      aria-labelledby="settings-title"
    >
      <div
        className="bg-[var(--bg-surface)] border border-[var(--border-default)] rounded-xl shadow-2xl w-[min(760px,94vw)] h-[min(640px,88vh)] flex overflow-hidden"
        style={{ transform: `translate(${drag.x}px, ${drag.y}px)` }}
        onClick={e => e.stopPropagation()}
      >
        {/* Left sidebar: vertical tabs */}
        <div className="w-44 flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-surface)] p-3 flex flex-col">
          <div className="mb-4 px-2.5 pt-1 text-body font-semibold text-[var(--text-primary)]">
            设置
          </div>
          <nav className="flex-1 space-y-0.5 overflow-y-auto">
            {TABS.map(tab => {
              const Icon = tab.icon
              const selected = activeTab === tab.id
              return (
                <button
                  key={tab.id}
                  onClick={() => setActiveTab(tab.id)}
                  className={`group relative w-full flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-helper transition-all duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
                    selected
                      ? 'bg-[color-mix(in_srgb,var(--accent-blue)_10%,transparent)] font-medium text-[var(--accent-blue)]'
                      : 'text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
                  }`}
                  aria-current={selected ? 'page' : undefined}
                >
                  {selected && (
                    <span
                      className="absolute left-0 top-1/2 h-5 w-[3px] -translate-y-1/2 rounded-r-full bg-[var(--accent-blue)]"
                      aria-hidden="true"
                    />
                  )}
                  <Icon
                    className={`h-4 w-4 shrink-0 ${
                      selected
                        ? 'text-[var(--accent-blue)]'
                        : 'text-[var(--text-muted)] group-hover:text-[var(--text-secondary)]'
                    }`}
                  />
                  <span className="truncate">{tab.label}</span>
                </button>
              )
            })}
          </nav>
        </div>

        {/* Right content */}
        <div className="flex min-w-0 flex-1 flex-col bg-[var(--bg-surface)]">
          <div
            className="flex flex-shrink-0 cursor-move select-none items-center justify-between border-b border-[var(--border-muted)] px-5 py-3"
            onPointerDown={startDrag}
          >
            <h2 id="settings-title" className="text-body font-semibold text-[var(--text-primary)]">
              {activeTabDef.label}
            </h2>
            <button
              onClick={onClose}
              className="flex h-7 w-7 items-center justify-center rounded-md text-[var(--text-muted)] transition-colors duration-fast hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
              aria-label="关闭"
            >
              ✕
            </button>
          </div>

          <div className="flex-1 overflow-y-auto px-5 py-4">
            {loading && activeTab === 'editor' && (
              <div className="text-helper text-[var(--text-muted)]">加载中…</div>
            )}

            {activeTab === 'appearance' && (
              <div className="space-y-4">
                <div className={sectionBox}>
                  <div className="flex items-center justify-between gap-4">
                    <div>
                      <div className={sectionTitle}>主题</div>
                      <div className={sectionDesc}>默认浅色；跟随系统时随 OS 深/浅切换</div>
                    </div>
                    <ThemeSelect />
                  </div>
                </div>
                <div className={sectionBox}>
                  <div className="flex items-center justify-between gap-4">
                    <div>
                      <div className={sectionTitle}>用户头像</div>
                      <div className={sectionDesc}>交互消息面板中用户消息的图标；可上传图像（不超过 200KB）</div>
                    </div>
                    <div className="flex items-center gap-2">
                      <UserAvatar size={32} />
                      <button onClick={() => avatarFileRef.current?.click()} className={btnCls}>
                        上传图像
                      </button>
                      {userAvatar && (
                        <button onClick={handleAvatarReset} className={btnCls}>
                          恢复默认
                        </button>
                      )}
                      <input
                        ref={avatarFileRef}
                        type="file"
                        accept="image/*"
                        className="hidden"
                        aria-label="上传用户头像图像"
                        onChange={handleAvatarFile}
                      />
                    </div>
                  </div>
                  {avatarError && (
                    <div className="mt-2 text-helper text-[var(--accent-red)]" role="alert">{avatarError}</div>
                  )}
                </div>
              </div>
            )}

            {activeTab === 'navigation' && (
              <div className={sectionBox}>
                <div className="flex items-center justify-between gap-4">
                  <div>
                    <div className={sectionTitle}>打开时展开</div>
                    <div className={sectionDesc}>打开会话时展开导航面板并启用钉住</div>
                  </div>
                  <select
                    value={navOpenPref}
                    onChange={e => handleNavOpenPref(e.target.value as NavOpenPref)}
                    className={selectCls}
                    aria-label="打开会话时展开导航"
                  >
                    <option value="user">交互消息</option>
                    <option value="tool">工具调用</option>
                    <option value="off">不展开</option>
                  </select>
                </div>
              </div>
            )}

            {activeTab === 'search' && (
              <div className={sectionBox}>
                <div className="flex items-center justify-between gap-4">
                  <div>
                    <div className={sectionTitle}>历史记录条数</div>
                    <div className={sectionDesc}>钉住的记录不占用条数限制</div>
                  </div>
                  <input
                    type="number"
                    min={HISTORY_LIMIT_MIN}
                    max={HISTORY_LIMIT_MAX}
                    value={historyLimit}
                    onChange={e => handleHistoryLimit(Number(e.target.value))}
                    className={`${inputCls} w-16 text-right`}
                    aria-label="历史记录条数"
                  />
                </div>
                <div className="mt-4 pt-4 border-t border-[var(--border-muted)]">
                  <button onClick={onClearHistory} className={dangerBtnCls}>
                    清空搜索历史
                  </button>
                </div>
              </div>
            )}

            {activeTab === 'terminal' && (
              <div className="space-y-4">
                <div className={sectionBox}>
                  <div className="flex items-center justify-between gap-4">
                    <div>
                      <div className={sectionTitle}>Turn 横幅颜色</div>
                      <div className={sectionDesc}>默认随深/浅主题，改动即时生效</div>
                    </div>
                    <span className="flex items-center gap-1.5">
                      <input
                        type="color"
                        value={bannerColor ?? defaultBannerColor(isDark)}
                        onChange={e => {
                          setBannerColor(e.target.value)
                          setBannerColorOverride(e.target.value)
                        }}
                        className="h-7 w-9 cursor-pointer rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] p-0.5"
                        aria-label="Turn 横幅颜色"
                      />
                      {bannerColor && (
                        <button
                          onClick={() => {
                            setBannerColor(null)
                            setBannerColorOverride(null)
                          }}
                          className={btnCls}
                        >
                          恢复默认
                        </button>
                      )}
                    </span>
                  </div>
                </div>

                <div className={sectionBox}>
                  <div className={sectionTitle}>消息时间戳前缀</div>
                  <div className={sectionDesc}>勾选的消息类型在终端里显示 HH:MM:SS，保存后立即重渲染</div>
                  <div className="mt-3 flex flex-wrap items-center gap-4">
                    {(
                      [
                        ['user', '用户输入'],
                        ['assistant', '助手回复'],
                        ['tool', '工具调用'],
                      ] as const
                    ).map(([kind, label]) => (
                      <label
                        key={kind}
                        className="flex cursor-pointer items-center gap-2 text-helper text-[var(--text-primary)]"
                      >
                        <input
                          type="checkbox"
                          checked={tsKinds.includes(kind)}
                          onChange={() => toggleTsKind(kind)}
                          className={checkboxCls}
                        />
                        {label}
                      </label>
                    ))}
                  </div>
                </div>
              </div>
            )}

            {activeTab === 'fonts' && (
              <div className="space-y-4">
                <div className={sectionBox}>
                  <div className="text-body font-semibold text-[var(--text-primary)] mb-3">界面字体</div>
                  <div className="space-y-3">
                    <FontPicker
                      label="字体"
                      value={uiFont}
                      onChange={next => {
                        setUiFont(next)
                        setUIFontPref(next)
                      }}
                    />
                    <div>
                      <div className="text-helper text-[var(--text-primary)]">字号</div>
                      <div className="mt-1 flex items-center gap-2">
                        {[
                          { key: 'small', label: '小' },
                          { key: 'medium', label: '中' },
                          { key: 'large', label: '大' },
                        ].map(({ key, label }) => {
                          const selected = uiFontSize === key
                          return (
                            <button
                              key={key}
                              onClick={() => {
                                setUiFontSize(key)
                                setUIFontSizePref(key)
                              }}
                              className={`h-7 px-3 rounded-md border text-helper transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
                                selected
                                  ? 'bg-[color-mix(in_srgb,var(--accent-blue)_10%,transparent)] border-[var(--accent-blue)] text-[var(--accent-blue)]'
                                  : 'border-[var(--border-default)] text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
                              }`}
                            >
                              {label}
                            </button>
                          )
                        })}
                      </div>
                    </div>
                  </div>
                </div>

                <div className={sectionBox}>
                  <div className="text-body font-semibold text-[var(--text-primary)] mb-3">终端字体</div>
                  <div className="space-y-3">
                    <FontPicker
                      label="字体"
                      value={terminalFont}
                      monospaceOnly
                      onChange={next => {
                        setTerminalFont(next)
                        setTerminalFontPref(next)
                      }}
                    />
                    <div>
                      <div className="text-helper text-[var(--text-primary)]">字号</div>
                      <div className="mt-1 flex items-center gap-3">
                        <input
                          type="range"
                          min={10}
                          max={20}
                          step={1}
                          value={terminalFontSize}
                          onChange={e => {
                            const size = Number(e.target.value)
                            setTerminalFontSize(size)
                            setTerminalFontSizePref(size)
                          }}
                          className="h-7 flex-1 accent-[var(--accent-blue)]"
                          aria-label="终端字号"
                        />
                        <span className="w-10 text-right text-helper text-[var(--text-primary)] tabular-nums">
                          {terminalFontSize}px
                        </span>
                      </div>
                    </div>
                  </div>
                </div>

                <div className="flex items-center justify-between gap-4 rounded-lg border border-[var(--border-muted)] bg-[var(--bg-surface)] p-4">
                  <div className={sectionDesc}>提示：使用 ↑ / ↓ 可实时预览效果</div>
                  <button
                    onClick={() => {
                      setUiFont(DEFAULT_UI_FONT)
                      setUIFontPref(DEFAULT_UI_FONT)
                      setUiFontSize(DEFAULT_UI_FONT_SIZE)
                      setUIFontSizePref(DEFAULT_UI_FONT_SIZE)
                      setTerminalFont(DEFAULT_TERMINAL_FONT)
                      setTerminalFontPref(DEFAULT_TERMINAL_FONT)
                      setTerminalFontSize(DEFAULT_TERMINAL_FONT_SIZE)
                      setTerminalFontSizePref(DEFAULT_TERMINAL_FONT_SIZE)
                    }}
                    className={btnCls}
                  >
                    恢复默认字体
                  </button>
                </div>
              </div>
            )}

            {activeTab === 'editor' && (
              <div className="space-y-4">
                <div className={sectionBox}>
                  <label className="block text-helper font-medium text-[var(--text-primary)]">
                    打开文件的程序
                    <input
                      type="text"
                      value={editorCommand}
                      placeholder={editorCommandDefault || '默认使用系统默认编辑器（如 code / xdg-open）'}
                      onChange={e => setEditorCommand(e.target.value)}
                      onBlur={() => void persistEditorCommand()}
                      className={`${inputCls} mt-1.5 w-full font-mono text-meta`}
                    />
                  </label>
                  <div className={sectionDesc}>
                    自定义打开文件时使用的程序；留空则使用系统默认。占位符：{'{path}'}、{'{line}'}
                  </div>
                </div>

                <div className={sectionBox}>
                  <label className="block text-helper font-medium text-[var(--text-primary)]">
                    可打开文件类型
                    <input
                      type="text"
                      value={fileExts}
                      placeholder="留空 = 默认代码/文本扩展名"
                      onChange={e => setFileExts(e.target.value)}
                      onBlur={() => void persistFileExts()}
                      className={`${inputCls} mt-1.5 w-full font-mono text-meta`}
                    />
                  </label>
                  <div className={sectionDesc}>
                    逗号分隔（如 go,ts,vue）；* 表示不限制。终端里只有这些类型的文件才会出现打开入口，改动刷新后生效
                  </div>
                </div>
              </div>
            )}

            {activeTab === 'ai' && (
              <div className={sectionBox}>
                <div className={sectionTitle}>AI 模型源</div>
                <div className={sectionDesc}>用于会话总结、会话标题与会话交接</div>
                <div className="mt-4">
                  <button onClick={handleOpenAISettings} className={primaryBtnCls}>
                    管理 AI 模型源…
                  </button>
                </div>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
