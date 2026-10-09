import { useCallback, useState } from 'react'
import type { ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { useI18n } from '../i18n'

export interface ConfirmOptions {
  /** 已本地化的确认文案（支持换行） */
  message: string
  /** 危险操作（删除类）用红色确认按钮 */
  danger?: boolean
}

interface PendingConfirm {
  options: ConfirmOptions
  resolve: (ok: boolean) => void
}

// useConfirm 提供应用内渲染的确认对话框，替代 window.confirm。
// 背景：--app 桌面窗口的内嵌 webview 不实现原生 confirm 面板，WebKit
// 会把未实现的确认当 Cancel 处理，导致删除模型源 / 删除导入副本 /
// 大批量导出等流程在 app 模式下被静默中止。返回的 confirmDialog 必须
// 渲染到组件树中（通常放在组件返回的根部）。
export function useConfirm(): {
  confirm: (options: ConfirmOptions) => Promise<boolean>
  confirmDialog: ReactNode
} {
  const { t } = useI18n()
  const [pending, setPending] = useState<PendingConfirm | null>(null)

  const confirm = useCallback(
    (options: ConfirmOptions) =>
      new Promise<boolean>(resolve => {
        setPending({ options, resolve })
      }),
    []
  )

  const finish = (ok: boolean) => {
    pending?.resolve(ok)
    setPending(null)
  }

  const confirmDialog = createPortal(
    pending && (
      <>
        <div
          className="fixed inset-0 z-[calc(var(--z-toast,50)+1)] bg-[rgba(0,0,0,var(--opacity-overlay,0.4))]"
          onClick={() => finish(false)}
        />
        <div
          role="alertdialog"
          aria-modal="true"
          className="fixed left-1/2 top-1/3 z-[calc(var(--z-toast,50)+2)] w-[420px] -translate-x-1/2 rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] p-4 shadow-lg"
        >
          <p className="text-body text-[var(--text-secondary)] break-all">{pending.options.message}</p>
          <div className="mt-4 flex items-center justify-end gap-2">
            <button
              onClick={() => finish(false)}
              className="h-7 px-3 rounded-md border border-[var(--border-default)] text-nav text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
            >
              {t('common.cancel')}
            </button>
            <button
              onClick={() => finish(true)}
              className={`h-7 px-3 rounded-md text-nav text-white transition-opacity duration-fast focus-visible:outline-none focus-visible:ring-2 ${
                pending.options.danger
                  ? 'bg-[var(--error)] focus-visible:ring-[var(--error)]'
                  : 'bg-[var(--accent-blue)] focus-visible:ring-[var(--accent-blue)]'
              }`}
            >
              {t('common.confirm')}
            </button>
          </div>
        </div>
      </>
    ),
    document.body
  )

  return { confirm, confirmDialog }
}
