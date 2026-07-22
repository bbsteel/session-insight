import { useEffect, useRef, useState } from 'react'
import { fetchSettings, fetchVersion, saveSettings, type VersionInfo } from '../api'
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
import { useI18n, type Locale } from '../i18n'
import UserAvatar, { useUserAvatar } from './UserAvatar'
import {
  AppearanceIcon,
  EditorIcon,
  FontIcon,
  InfoIcon,
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
  /** 打开时定位到的 Tab（如侧边栏版本号点击进入「关于」）；默认外观 */
  initialTab?: TabId
}

type TabId = 'appearance' | 'navigation' | 'search' | 'terminal' | 'fonts' | 'editor' | 'ai' | 'about'

interface TabDef {
  id: TabId
  labelKey: string
  icon: React.ComponentType<{ className?: string }>
}

const TABS: TabDef[] = [
  { id: 'appearance', labelKey: 'settings.tab.appearance', icon: AppearanceIcon },
  { id: 'navigation', labelKey: 'settings.tab.navigation', icon: NavigationIcon },
  { id: 'search', labelKey: 'settings.tab.search', icon: SearchIcon },
  { id: 'terminal', labelKey: 'settings.tab.terminal', icon: TerminalIcon },
  { id: 'fonts', labelKey: 'settings.tab.fonts', icon: FontIcon },
  { id: 'editor', labelKey: 'settings.tab.editor', icon: EditorIcon },
  { id: 'ai', labelKey: 'settings.tab.ai', icon: SparklesIcon },
  { id: 'about', labelKey: 'settings.tab.about', icon: InfoIcon },
]

const GITHUB_REPO_URL = 'https://github.com/bbsteel/session-insight'

const HISTORY_LIMIT_MIN = 1
const HISTORY_LIMIT_MAX = 50

export default function SettingsDialog({
  open,
  onClose,
  historyLimit,
  onHistoryLimitChange,
  onClearHistory,
  onOpenAISettings,
  initialTab,
}: Props) {
  const { preference, setPreference, t } = useI18n()
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
  const [versionInfo, setVersionInfo] = useState<VersionInfo | null>(null)

  // 上传自定义头像:仅校验类型和大小(≤200KB),原图以 data URL 存本地。
  const handleAvatarFile = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    e.target.value = '' // 允许再次选择同一文件
    if (!file) return
    if (!file.type.startsWith('image/')) {
      setAvatarError(t('settings.avatarImageOnly'))
      return
    }
    if (file.size > AVATAR_MAX_BYTES) {
      setAvatarError(t('settings.avatarTooLarge', { size: Math.ceil(file.size / 1024) }))
      return
    }
    const readId = ++avatarReadSeq.current
    const reader = new FileReader()
    reader.onload = () => {
      if (readId !== avatarReadSeq.current) return
      if (setUserAvatar(typeof reader.result === 'string' ? reader.result : null)) {
        setAvatarError('')
      } else {
        setAvatarError(t('settings.avatarSaveFailed'))
      }
    }
    reader.onerror = () => {
      if (readId !== avatarReadSeq.current) return
      setAvatarError(t('settings.avatarReadFailed'))
    }
    reader.readAsDataURL(file)
  }

  const handleAvatarReset = () => {
    avatarReadSeq.current++ // 使进行中的读取失效,避免覆盖本次还原
    if (setUserAvatar(null)) {
      setAvatarError('')
    } else {
      setAvatarError(t('settings.avatarSaveFailed'))
    }
  }

  // Load server-side settings whenever the dialog opens.
  useEffect(() => {
    if (!open) return
    setActiveTab(initialTab ?? 'appearance')
    setDrag({ x: 0, y: 0 })
    setLoading(true)
    setNavOpenPrefState(getNavOpenPref())
    setBannerColor(getBannerColorOverride())
    setAvatarError('')
    setUiFont(getUIFont())
    setUiFontSize(getUIFontSize())
    setTerminalFont(getTerminalFont())
    setTerminalFontSize(getTerminalFontSize())
    void fetchVersion().then(setVersionInfo)
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
  }, [open, initialTab])

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
            {t('settings.title')}
          </div>
          <nav className="flex-1 space-y-0.5 overflow-y-auto">
            {TABS.map(tab => {
              const Icon = tab.icon
              const selected = activeTab === tab.id
              return (
                <div key={tab.id}>
                  {tab.id === 'about' && (
                    <div className="my-2 border-t border-[var(--border-muted)]" aria-hidden="true" />
                  )}
                  <button
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
                    <span className="truncate">{t(tab.labelKey)}</span>
                  </button>
                </div>
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
              {t(activeTabDef.labelKey)}
            </h2>
            <button
              onClick={onClose}
              className="flex h-7 w-7 items-center justify-center rounded-md text-[var(--text-muted)] transition-colors duration-fast hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
              aria-label={t('common.close')}
            >
              ✕
            </button>
          </div>

          <div className="flex-1 overflow-y-auto px-5 py-4">
            {loading && activeTab === 'editor' && (
              <div className="text-helper text-[var(--text-muted)]">{t('common.loading')}</div>
            )}

            {activeTab === 'appearance' && (
              <div className="space-y-4">
                <div className={sectionBox}>
                  <div className="flex items-center justify-between gap-4">
                    <div>
                      <div className={sectionTitle}>{t('settings.language')}</div>
                      <div className={sectionDesc}>{t('settings.languageHelp')}</div>
                    </div>
                    <select
                      value={preference ?? 'system'}
                      onChange={e => setPreference(e.target.value === 'system' ? null : e.target.value as Locale)}
                      className={selectCls}
                      aria-label={t('settings.language')}
                    >
                      <option value="system">{t('settings.languageSystem')}</option>
                      <option value="en">{t('settings.languageEnglish')}</option>
                      <option value="zh-CN">{t('settings.languageChinese')}</option>
                    </select>
                  </div>
                </div>
                <div className={sectionBox}>
                  <div className="flex items-center justify-between gap-4">
                    <div>
                      <div className={sectionTitle}>{t('theme.label')}</div>
                      <div className={sectionDesc}>{t('settings.themeHelp')}</div>
                    </div>
                    <ThemeSelect />
                  </div>
                </div>
                <div className={sectionBox}>
                  <div className="flex items-center justify-between gap-4">
                    <div>
                      <div className={sectionTitle}>{t('settings.avatar')}</div>
                      <div className={sectionDesc}>{t('settings.avatarHelp')}</div>
                    </div>
                    <div className="flex items-center gap-2">
                      <UserAvatar size={32} />
                      <button onClick={() => avatarFileRef.current?.click()} className={btnCls}>
                        {t('settings.avatarUpload')}
                      </button>
                      {userAvatar && (
                        <button onClick={handleAvatarReset} className={btnCls}>
                          {t('settings.avatarReset')}
                        </button>
                      )}
                      <input
                        ref={avatarFileRef}
                        type="file"
                        accept="image/*"
                        className="hidden"
                        aria-label={t('settings.avatarUploadLabel')}
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
                    <div className={sectionTitle}>{t('settings.navOpen')}</div>
                    <div className={sectionDesc}>{t('settings.navOpenHelp')}</div>
                  </div>
                  <select
                    value={navOpenPref}
                    onChange={e => handleNavOpenPref(e.target.value as NavOpenPref)}
                    className={selectCls}
                    aria-label={t('settings.navOpenLabel')}
                  >
                    <option value="user">{t('settings.navUser')}</option>
                    <option value="tool">{t('settings.navTool')}</option>
                    <option value="off">{t('settings.navOff')}</option>
                  </select>
                </div>
              </div>
            )}

            {activeTab === 'search' && (
              <div className={sectionBox}>
                <div className="flex items-center justify-between gap-4">
                  <div>
                    <div className={sectionTitle}>{t('settings.historyLimit')}</div>
                    <div className={sectionDesc}>{t('settings.historyLimitHelp')}</div>
                  </div>
                  <input
                    type="number"
                    min={HISTORY_LIMIT_MIN}
                    max={HISTORY_LIMIT_MAX}
                    value={historyLimit}
                    onChange={e => handleHistoryLimit(Number(e.target.value))}
                    className={`${inputCls} w-16 text-right`}
                    aria-label={t('settings.historyLimit')}
                  />
                </div>
                <div className="mt-4 pt-4 border-t border-[var(--border-muted)]">
                  <button onClick={onClearHistory} className={dangerBtnCls}>
                    {t('settings.clearHistory')}
                  </button>
                </div>
              </div>
            )}

            {activeTab === 'terminal' && (
              <div className="space-y-4">
                <div className={sectionBox}>
                  <div className="flex items-center justify-between gap-4">
                    <div>
                      <div className={sectionTitle}>{t('settings.bannerColor')}</div>
                      <div className={sectionDesc}>{t('settings.bannerColorHelp')}</div>
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
                        aria-label={t('settings.bannerColor')}
                      />
                      {bannerColor && (
                        <button
                          onClick={() => {
                            setBannerColor(null)
                            setBannerColorOverride(null)
                          }}
                          className={btnCls}
                        >
                          {t('settings.restoreDefault')}
                        </button>
                      )}
                    </span>
                  </div>
                </div>

                <div className={sectionBox}>
                  <div className={sectionTitle}>{t('settings.timestamp')}</div>
                  <div className={sectionDesc}>{t('settings.timestampHelp')}</div>
                  <div className="mt-3 flex flex-wrap items-center gap-4">
                    {(
                      [
                        ['user', 'settings.timestampUser'],
                        ['assistant', 'settings.timestampAssistant'],
                        ['tool', 'settings.timestampTool'],
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
                        {t(label)}
                      </label>
                    ))}
                  </div>
                </div>
              </div>
            )}

            {activeTab === 'fonts' && (
              <div className="space-y-4">
                <div className={sectionBox}>
                  <div className="text-body font-semibold text-[var(--text-primary)] mb-3">{t('settings.uiFont')}</div>
                  <div className="space-y-3">
                    <FontPicker
                      label={t('settings.font')}
                      value={uiFont}
                      onChange={next => {
                        setUiFont(next)
                        setUIFontPref(next)
                      }}
                    />
                    <div>
                      <div className="text-helper text-[var(--text-primary)]">{t('settings.fontSize')}</div>
                      <div className="mt-1 flex items-center gap-2">
                        {[
                          { key: 'small', label: t('settings.fontSmall') },
                          { key: 'medium', label: t('settings.fontMedium') },
                          { key: 'large', label: t('settings.fontLarge') },
                        ].map(({ key, label }) => {
                          const selected = uiFontSize === key
                          return (
                            <button
                              key={key}
                              onClick={() => {
                                setUiFontSize(key)
                                setUIFontSizePref(key)
                              }}
                              aria-pressed={selected}
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
                  <div className="text-body font-semibold text-[var(--text-primary)] mb-3">{t('settings.terminalFont')}</div>
                  <div className="space-y-3">
                    <FontPicker
                      label={t('settings.font')}
                      value={terminalFont}
                      monospaceOnly
                      onChange={next => {
                        setTerminalFont(next)
                        setTerminalFontPref(next)
                      }}
                    />
                    <div>
                      <div className="text-helper text-[var(--text-primary)]">{t('settings.fontSize')}</div>
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
                          aria-label={t('settings.terminalFontSize')}
                        />
                        <span className="w-10 text-right text-helper text-[var(--text-primary)] tabular-nums">
                          {terminalFontSize}px
                        </span>
                      </div>
                    </div>
                  </div>
                </div>

                <div className="flex items-center justify-between gap-4 rounded-lg border border-[var(--border-muted)] bg-[var(--bg-surface)] p-4">
                  <div className={sectionDesc}>{t('settings.fontPreviewHelp')}</div>
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
                    {t('settings.restoreFonts')}
                  </button>
                </div>
              </div>
            )}

            {activeTab === 'editor' && (
              <div className="space-y-4">
                <div className={sectionBox}>
                  <label className="block text-helper font-medium text-[var(--text-primary)]">
                    {t('settings.editorCommand')}
                    <input
                      type="text"
                      value={editorCommand}
                      placeholder={editorCommandDefault || t('settings.editorDefault')}
                      onChange={e => setEditorCommand(e.target.value)}
                      onBlur={() => void persistEditorCommand()}
                      className={`${inputCls} mt-1.5 w-full font-mono text-meta`}
                    />
                  </label>
                  <div className={sectionDesc}>
                    {t('settings.editorHelp', { path: '{path}', line: '{line}' })}
                  </div>
                </div>

                <div className={sectionBox}>
                  <label className="block text-helper font-medium text-[var(--text-primary)]">
                    {t('settings.fileTypes')}
                    <input
                      type="text"
                      value={fileExts}
                      placeholder={t('settings.fileTypesPlaceholder')}
                      onChange={e => setFileExts(e.target.value)}
                      onBlur={() => void persistFileExts()}
                      className={`${inputCls} mt-1.5 w-full font-mono text-meta`}
                    />
                  </label>
                  <div className={sectionDesc}>
                    {t('settings.fileTypesHelp')}
                  </div>
                </div>
              </div>
            )}

            {activeTab === 'ai' && (
              <div className={sectionBox}>
                <div className={sectionTitle}>{t('settings.aiProviders')}</div>
                <div className={sectionDesc}>{t('settings.aiProvidersHelp')}</div>
                <div className="mt-4">
                  <button onClick={handleOpenAISettings} className={primaryBtnCls}>
                    {t('settings.manageAI')}
                  </button>
                </div>
              </div>
            )}

            {activeTab === 'about' && (
              <div className="space-y-4">
                <div className={sectionBox}>
                  <div className="flex items-baseline gap-2">
                    <span className="text-body font-semibold text-[var(--text-primary)]">
                      Session Insight
                    </span>
                    <span className="text-helper text-[var(--text-secondary)]" data-testid="about-version">
                      {versionInfo?.version ?? '…'}
                    </span>
                  </div>
                  {versionInfo?.commit && (
                    <div className="mt-1 text-meta text-[var(--text-muted)]">
                      {t('settings.devBuild', { commit: versionInfo.commit })}
                    </div>
                  )}
                  <div className={sectionDesc}>
                    {t('settings.aboutSummary')}
                  </div>
                  <div className={sectionDesc}>
                    {t('settings.aboutPrivacy')}
                  </div>
                </div>

                <div className={sectionBox}>
                  <div className={sectionTitle}>{t('settings.links')}</div>
                  <div className="mt-2 space-y-1.5">
                    {(
                      [
                        [t('settings.github'), GITHUB_REPO_URL],
                        [t('settings.releases'), `${GITHUB_REPO_URL}/releases`],
                        [t('settings.issues'), `${GITHUB_REPO_URL}/issues`],
                      ] as const
                    ).map(([label, url]) => (
                      <div key={url}>
                        <a
                          href={url}
                          target="_blank"
                          rel="noreferrer noopener"
                          className="text-helper text-[var(--accent-blue)] hover:underline"
                        >
                          {label}
                        </a>
                      </div>
                    ))}
                  </div>
                </div>

                <div className={sectionBox}>
                  <div className="text-meta text-[var(--text-muted)]">
                    MIT License · © 2026 bbsteel
                  </div>
                </div>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
