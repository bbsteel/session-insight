// 用户头像:交互消息面板中用户消息的图标。默认是内置人头 SVG;可在设置里
// 上传自定义图像(≤200KB),以 data URL 存 localStorage。模式和 navPrefs
// 一致(纯前端偏好,不入后端)。

const AVATAR_KEY = 'si-user-avatar'
const CHANGED_EVENT = 'si-user-avatar-changed'

// 上传源图大小上限(data URL 会再膨胀约 1/3,仍在 localStorage 配额内)。
export const AVATAR_MAX_BYTES = 200 * 1024

export function getUserAvatar(): string | null {
  try {
    return localStorage.getItem(AVATAR_KEY)
  } catch {
    return null
  }
}

// dataUrl 为 null 时恢复默认图标。
export function setUserAvatar(dataUrl: string | null): void {
  try {
    if (dataUrl) {
      localStorage.setItem(AVATAR_KEY, dataUrl)
    } else {
      localStorage.removeItem(AVATAR_KEY)
    }
    window.dispatchEvent(new Event(CHANGED_EVENT))
  } catch {
    // 配额满等异常:忽略,头像保持旧值
  }
}

// 同窗口自定义事件 + 跨窗口 storage 事件都触发订阅。
export function subscribeUserAvatar(cb: () => void): () => void {
  window.addEventListener(CHANGED_EVENT, cb)
  window.addEventListener('storage', cb)
  return () => {
    window.removeEventListener(CHANGED_EVENT, cb)
    window.removeEventListener('storage', cb)
  }
}
