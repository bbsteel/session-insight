import { createContext, useContext, useMemo, useState, type ReactNode } from 'react'

export type Locale = 'zh-CN' | 'en'
type MessageValue = string | ((vars: Record<string, string | number>) => string)
type Messages = Record<string, MessageValue>

const LOCALE_KEY = 'si-locale'
export const fallbackLocale: Locale = 'en'

export const messages: Record<Locale, Messages> = {
  en: {
    'app.openSessions': 'Open session list',
    'settings.language': 'Language',
    'settings.languageHelp': 'Choose the language used by the application.',
    'settings.languageSystem': 'System default',
    'settings.languageEnglish': 'English',
    'settings.languageChinese': '简体中文',
    'common.close': 'Close',
    'common.back': 'Back to sessions',
    'common.search': 'Search',
    'common.loading': 'Loading…',
    'common.error': 'Something went wrong. Please try again.',
    'common.noResults': 'No results found',
    'common.copy': 'Copy',
    'common.retry': 'Retry',
    'search.placeholder': 'Search all sessions… ({shortcut})',
    'search.indexing': 'Indexing… {percent}%',
    'search.recent': 'Recent searches',
    'search.results': 'Results',
    'search.searching': 'Searching…',
    'search.noMatches': 'No matching results',
    'search.pin': 'Pin',
    'search.unpin': 'Unpin',
    'search.delete': 'Delete',
    'search.indexCompleteWithErrors': 'Indexing complete; some sessions failed and will be retried.',
    'search.indexReady': 'Full-text index is ready',
    'search.partialAvailable': 'Completed sessions can be searched now',
    'settings.title': 'Settings',
    'settings.tab.appearance': 'Appearance',
    'settings.tab.navigation': 'Session navigation',
    'settings.tab.search': 'Full-text search',
    'settings.tab.terminal': 'Terminal appearance',
    'settings.tab.fonts': 'Fonts',
    'settings.tab.editor': 'File viewer',
    'settings.tab.ai': 'AI',
    'settings.tab.about': 'About',
    'replay.noSelection': 'No session selected',
    'replay.selectHint': 'Select a session from the list to view its terminal content.',
    'replay.noReplay': 'This session has no replayable content',
    'replay.noSessions': 'No sessions yet',
    'replay.bookmark': 'Bookmark',
    'replay.removeBookmark': 'Remove bookmark',
    'replay.active': 'Live',
    'replay.follow': 'Follow',
    'replay.export': 'Export',
    'replay.analytics': 'Analytics',
    'replay.tokens': 'tokens',
    'replay.turns': 'active turns',
    'replay.createdNoTurns': 'This session was created but has no user turns yet. It will appear here after a conversation starts.',
    'replay.agentHint': 'Sessions will appear here after you use an agent for coding.',
    'replay.followUnavailable': 'Follow output is available only for live sessions.',
    'replay.followOn': 'Stop following output',
    'replay.followOff': 'Follow output as new content arrives',
    'replay.aiPanel': 'AI summary, title, and handoff',
    'replay.rolledBack': 'rolled back',
    'replay.navigation': 'Navigation',
    'replay.messages': 'Messages',
    'replay.toolCalls': 'Tool calls',
    'sidebar.sessions': 'Sessions',
    'sidebar.total': 'total',
    'sidebar.filter': 'Filter sessions…',
    'sidebar.clearFilter': 'Clear search',
    'sidebar.noMatches': 'No matching sessions',
    'sidebar.tryAnother': 'Try another keyword or clear filters',
    'sidebar.clearFilters': 'Clear filters',
    'sidebar.live': 'Live',
    'sidebar.disconnected': 'Connection lost',
    'sidebar.disconnectedHelp': 'The live connection to the backend was lost. The list will refresh when it reconnects.',
    'sidebar.backendUnavailable': 'Backend unavailable',
    'sidebar.backendUnavailableHelp': 'Start the Go service to show local sessions.',
    'sidebar.openMenu': 'Open context menu',
  },
  'zh-CN': {
    'app.openSessions': '打开会话列表',
    'settings.language': '语言',
    'settings.languageHelp': '选择应用界面使用的语言。',
    'settings.languageSystem': '跟随系统',
    'settings.languageEnglish': 'English',
    'settings.languageChinese': '简体中文',
    'common.close': '关闭',
    'common.back': '返回会话列表',
    'common.search': '搜索',
    'common.loading': '加载中…',
    'common.error': '发生错误，请重试。',
    'common.noResults': '没有找到结果',
    'common.copy': '复制',
    'common.retry': '重试',
    'search.placeholder': '全文搜索…（{shortcut}）',
    'search.indexing': '索引进行中… {percent}%',
    'search.recent': '最近搜索',
    'search.results': '结果',
    'search.searching': '搜索中…',
    'search.noMatches': '无匹配结果',
    'search.pin': '钉住',
    'search.unpin': '取消钉住',
    'search.delete': '删除',
    'search.indexCompleteWithErrors': '索引完成，部分会话失败，将自动重试。',
    'search.indexReady': '全文索引已就绪',
    'search.partialAvailable': '可先搜索已完成的会话',
    'settings.title': '设置',
    'settings.tab.appearance': '外观',
    'settings.tab.navigation': '会话导航',
    'settings.tab.search': '全文搜索',
    'settings.tab.terminal': '终端外观',
    'settings.tab.fonts': '字体',
    'settings.tab.editor': '文件查看器',
    'settings.tab.ai': 'AI',
    'settings.tab.about': '关于',
    'replay.noSelection': '还没有选中会话',
    'replay.selectHint': '从左侧选择一个会话后，这里会显示终端内容。',
    'replay.noReplay': '此会话暂无可回放内容',
    'replay.noSessions': '还没有会话记录',
    'replay.bookmark': '收藏',
    'replay.removeBookmark': '取消收藏',
    'replay.active': '活跃中',
    'replay.follow': '跟随',
    'replay.export': '导出',
    'replay.analytics': '分析',
    'replay.tokens': 'tokens',
    'replay.turns': '活动 turns',
    'replay.createdNoTurns': '会话已创建但尚无用户回合。开始对话后会显示在这里。',
    'replay.agentHint': '使用 agent 进行编码后，会话将自动出现在这里。',
    'replay.followUnavailable': '仅活跃会话可跟随输出。',
    'replay.followOn': '关闭跟随输出',
    'replay.followOff': '有新内容时跟随输出',
    'replay.aiPanel': 'AI 总结、标题和交接',
    'replay.rolledBack': '已回滚',
    'replay.navigation': '导航',
    'replay.messages': '交互消息',
    'replay.toolCalls': '工具调用',
    'sidebar.sessions': '会话',
    'sidebar.total': '总计',
    'sidebar.filter': '过滤会话…',
    'sidebar.clearFilter': '清除搜索',
    'sidebar.noMatches': '未找到匹配的会话',
    'sidebar.tryAnother': '尝试其他关键词或清除筛选条件',
    'sidebar.clearFilters': '清除筛选',
    'sidebar.live': '活跃中',
    'sidebar.disconnected': '连接已断开',
    'sidebar.disconnectedHelp': '与后端的实时连接已断开；恢复后列表会自动刷新。',
    'sidebar.backendUnavailable': '后端未连接',
    'sidebar.backendUnavailableHelp': '启动 Go 服务后会显示本地会话。',
    'sidebar.openMenu': '打开右键菜单',
  },
}

export function systemLocale(language = typeof navigator === 'undefined' ? fallbackLocale : navigator.language): Locale {
  return language.toLowerCase().startsWith('zh') ? 'zh-CN' : fallbackLocale
}

export function getLocale(): Locale {
  try {
    const saved = localStorage.getItem(LOCALE_KEY)
    if (saved === 'zh-CN' || saved === 'en') return saved
  } catch { /* system default is safe when storage is unavailable */ }
  return systemLocale()
}

export function saveLocale(locale: Locale | null) {
  try {
    if (locale) localStorage.setItem(LOCALE_KEY, locale)
    else localStorage.removeItem(LOCALE_KEY)
  } catch { /* keep the in-memory selection */ }
}

export function translate(locale: Locale, key: string, vars: Record<string, string | number> = {}): string {
  const value = messages[locale][key] ?? messages[fallbackLocale][key] ?? key
  if (typeof value === 'function') return value(vars)
  return value.replace(/\{(\w+)\}/g, (_, name) => String(vars[name] ?? `{${name}}`))
}

export function formatDate(locale: Locale, value: Date | string | number, options?: Intl.DateTimeFormatOptions) {
  return new Intl.DateTimeFormat(locale, options).format(new Date(value))
}
export function formatNumber(locale: Locale, value: number, options?: Intl.NumberFormatOptions) {
  return new Intl.NumberFormat(locale, options).format(value)
}
export function formatRelativeTime(locale: Locale, value: number, unit: Intl.RelativeTimeFormatUnit = 'second') {
  return new Intl.RelativeTimeFormat(locale, { numeric: 'auto' }).format(value, unit)
}

interface I18nContextValue {
  locale: Locale
  preference: Locale | null
  setPreference: (locale: Locale | null) => void
  t: (key: string, vars?: Record<string, string | number>) => string
}
const I18nContext = createContext<I18nContextValue | null>(null)

export function I18nProvider({ children }: { children: ReactNode }) {
  const [preference, setStoredPreference] = useState<Locale | null>(() => {
    try { const value = localStorage.getItem(LOCALE_KEY); return value === 'zh-CN' || value === 'en' ? value : null } catch { return null }
  })
  const locale = preference ?? systemLocale()
  const value = useMemo<I18nContextValue>(() => ({
    locale, preference,
    setPreference(next) { saveLocale(next); setStoredPreference(next) },
    t: (key, vars) => translate(locale, key, vars),
  }), [locale, preference])
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>
}

export function useI18n() {
  const value = useContext(I18nContext)
  if (!value) throw new Error('useI18n must be used inside I18nProvider')
  return value
}
