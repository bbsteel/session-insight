import { useState } from 'react'
import { createPortal } from 'react-dom'
import { deleteSession, stopSession, SessionRunningError } from '../api'
import type { SessionSummary } from '../types'
import { getAgentLabel } from '../sidebarRows'

interface DeleteSessionDialogProps {
  session: SessionSummary
  onClose: () => void
  onDeleted: (session: SessionSummary) => void
}

// 删除是两态流程：停止态直接确认删除；进行态（后端探测到进程仍持有
// 会话文件）转入"先停止"形态，提供强制停止按钮，停止成功后回到可删除态。
type Phase = 'confirm' | 'deleting' | 'running' | 'stopping' | 'stopped'

export default function DeleteSessionDialog({ session, onClose, onDeleted }: DeleteSessionDialogProps) {
  const [phase, setPhase] = useState<Phase>('confirm')
  const [pids, setPids] = useState<number[]>([])
  const [error, setError] = useState<string | null>(null)
  const [copiedPid, setCopiedPid] = useState<number | null>(null)

  const copyPid = async (pid: number) => {
    try {
      await navigator.clipboard.writeText(String(pid))
      setCopiedPid(pid)
      setTimeout(() => setCopiedPid(current => (current === pid ? null : current)), 1500)
    } catch {
      // Clipboard is optional; the pid is still visible to copy manually.
    }
  }

  const busy = phase === 'deleting' || phase === 'stopping'
  const sessionName = session.name || session.repository || session.id.slice(0, 8)

  const doDelete = async () => {
    setPhase('deleting')
    setError(null)
    try {
      await deleteSession(session.id)
      onDeleted(session)
    } catch (err) {
      if (err instanceof SessionRunningError) {
        setPids(err.pids)
        setPhase('running')
      } else {
        setError(err instanceof Error ? err.message : '删除失败')
        setPhase('confirm')
      }
    }
  }

  const doStop = async () => {
    setPhase('stopping')
    setError(null)
    try {
      await stopSession(session.id)
      setPhase('stopped')
    } catch (err) {
      setError(err instanceof Error ? err.message : '停止失败')
      setPhase('running')
    }
  }

  return createPortal(
    <>
      <div
        className="fixed inset-0 z-[calc(var(--z-toast,50)+1)] bg-[rgba(0,0,0,var(--opacity-overlay,0.4))]"
        onClick={busy ? undefined : onClose}
      />
      <div
        role="dialog"
        aria-modal="true"
        aria-label="删除会话"
        className="fixed left-1/2 top-1/3 z-[calc(var(--z-toast,50)+2)] w-[420px] -translate-x-1/2 rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] p-4 shadow-lg"
      >
        <h3 className="text-nav font-semibold text-[var(--text-primary)]">删除会话</h3>
        <div className="mt-2 text-body text-[var(--text-secondary)] break-all">
          <span className="text-[var(--text-primary)]">{sessionName}</span>
          <span className="ml-1.5 text-meta text-[var(--text-muted)]">{getAgentLabel(session.agent_type)}</span>
        </div>

        {(phase === 'confirm' || phase === 'deleting') && (
          <p className="mt-3 text-body text-[var(--text-secondary)]">
            将从磁盘永久删除该会话的原始记录文件、Agent 全局历史中的对应条目，
            以及本站的索引、书签和 AI 生成记录。<span className="text-[var(--error)]">此操作不可恢复。</span>
          </p>
        )}
        {(phase === 'running' || phase === 'stopping') && (
          <p className="mt-3 text-body text-[var(--text-secondary)]">
            该会话正在运行
            {pids.length > 0 && (
              <>
                （PID{' '}
                {pids.map((pid, i) => (
                  <span key={pid}>
                    {i > 0 && '、'}
                    <button
                      onClick={() => copyPid(pid)}
                      title="点击复制 PID"
                      className="font-mono font-semibold text-[var(--error)] hover:underline cursor-pointer focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--error)] rounded-sm"
                    >
                      {pid}
                    </button>
                    {copiedPid === pid && <span className="ml-1 text-meta text-[var(--success)]">已复制</span>}
                  </span>
                ))}
                ）
              </>
            )}
            ，无法直接删除。请先在对应终端结束它，或点击"强制停止"结束该进程后再删除。
          </p>
        )}
        {phase === 'stopped' && (
          <p className="mt-3 text-body text-[var(--text-secondary)]">
            会话进程已停止，现在可以执行删除。<span className="text-[var(--error)]">此操作不可恢复。</span>
          </p>
        )}
        {error && <p className="mt-2 text-meta text-[var(--error)] break-all">{error}</p>}

        <div className="mt-4 flex justify-end gap-2">
          <button
            onClick={onClose}
            disabled={busy}
            className="h-7 px-3 rounded-md border border-[var(--border-default)] text-nav text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] disabled:opacity-50 transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
          >
            取消
          </button>
          {(phase === 'running' || phase === 'stopping') ? (
            <button
              onClick={doStop}
              disabled={busy}
              className="h-7 px-3 rounded-md bg-[var(--error)] text-nav text-white hover:opacity-90 disabled:opacity-50 transition-opacity duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--error)]"
            >
              {phase === 'stopping' ? '正在停止…' : '强制停止'}
            </button>
          ) : (
            <button
              onClick={doDelete}
              disabled={busy}
              className="h-7 px-3 rounded-md bg-[var(--error)] text-nav text-white hover:opacity-90 disabled:opacity-50 transition-opacity duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--error)]"
            >
              {phase === 'deleting' ? '正在删除…' : '永久删除'}
            </button>
          )}
        </div>
      </div>
    </>,
    document.body
  )
}
