import { useSyncExternalStore } from 'react'
import { getUserAvatar, subscribeUserAvatar } from '../userAvatar'
import { UserIcon } from './icons'

// 交互消息面板/设置共用的用户头像:设置了自定义图像则渲染圆形 img,
// 否则渲染内置人头图标。默认图标不设底圈、按 size 全尺寸描边渲染,
// 与助手行的 agent 图标(填满整个 size 盒)保持同一视觉尺寸。
export function useUserAvatar(): string | null {
  return useSyncExternalStore(subscribeUserAvatar, getUserAvatar)
}

interface Props {
  size?: number
  className?: string
}

export default function UserAvatar({ size = 14, className = '' }: Props) {
  const avatar = useUserAvatar()
  if (avatar) {
    return (
      <img
        src={avatar}
        alt="用户头像"
        className={`flex-shrink-0 rounded-full object-cover ${className}`}
        style={{ width: size, height: size }}
        draggable={false}
      />
    )
  }
  return (
    <span
      className={`inline-flex flex-shrink-0 items-center justify-center text-[var(--accent-blue)] ${className}`}
      style={{ width: size, height: size }}
      aria-hidden="true"
    >
      <UserIcon style={{ width: size, height: size }} />
    </span>
  )
}
