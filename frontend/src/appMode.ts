import { fetchVersion } from './api'

let appModePromise: Promise<boolean> | null = null

// isAppMode 报告后端是否以 --app 桌面窗口模式启动（/api/version 的 appMode
// 字段；失败按非 app 模式处理）。进程生命周期内不变，缓存一次请求。
export function isAppMode(): Promise<boolean> {
  if (!appModePromise) {
    appModePromise = fetchVersion()
      .then(info => info.appMode === true)
      .catch(() => false)
  }
  return appModePromise
}

function absoluteUrl(url: string): string {
  // 相对地址（如 #/file?...）按当前 origin 展开后再交给系统浏览器。
  return new URL(url, window.location.origin).href
}

async function routeToSystemBrowser(url: string): Promise<void> {
  try {
    await fetch('/api/open-url', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ url: absoluteUrl(url) }),
    })
  } catch {
    // 打开器不可用时静默：点击流程已经走完，不再打断用户。
  }
}

// installAppWindowExternalLinks 在 --app 桌面窗口模式下接管新窗口请求。
// 内嵌 webview 不处理 window.open / target=_blank（GTK 后端不连接 WebKit
// 的 create 信号，Cocoa 委托未实现 createWebViewWithConfiguration），
// 这些请求会被静默丢弃；统一改由后端 /api/open-url 调起系统浏览器。
// 浏览器模式下不做任何改动。
export function installAppWindowExternalLinks(): void {
  void isAppMode().then(appMode => {
    if (!appMode) return

    window.open = ((url?: string | URL): Window | null => {
      if (url) void routeToSystemBrowser(String(url))
      return null
    }) as typeof window.open

    document.addEventListener(
      'click',
      event => {
        const anchor = (event.target as HTMLElement | null)?.closest?.('a[target="_blank"]')
        if (!(anchor instanceof HTMLAnchorElement)) return
        event.preventDefault()
        void routeToSystemBrowser(anchor.href)
      },
      true
    )
  })
}
