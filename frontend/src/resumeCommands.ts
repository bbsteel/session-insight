import type { SessionSummary } from './types'

export type ResumeShell = 'powershell' | 'git-bash'
export type ResumeShellReason = 'last-used' | 'recommended' | null

export interface ResumeCommandOption {
  shell: ResumeShell
  command: string
  reason: ResumeShellReason
}

function agentResumeArgs(session: SessionSummary): string[] | null {
  const agent = session.agent_type.toLowerCase()
  const id = session.resume_id || session.id
  if (agent.includes('claude')) return ['claude', '--resume', id]
  if (agent.includes('codex')) return ['codex', 'resume', id]
  if (agent.includes('opencode')) return ['opencode', '-s', id]
  if (agent.includes('chrys')) return ['chrys', '-s', id]
  return null
}

function quotePowerShell(value: string): string {
  return `'${value.split("'").join("''")}'`
}

function quoteBash(value: string): string {
  return `'${value.split("'").join(`'"'"'`)}'`
}

export function toGitBashPath(path: string): string {
  const normalized = path.split('\\').join('/')
  const drive = normalized.match(/^([A-Za-z]):(?:\/(.*))?$/)
  if (drive) return `/${drive[1].toLowerCase()}/${drive[2] ?? ''}`.replace(/\/$/, '')
  if (normalized.startsWith('//')) return normalized
  return normalized
}

export function isWindowsSession(session: Pick<SessionSummary, 'cwd'>, hostIsWindows = false): boolean {
  return hostIsWindows || /^[A-Za-z]:[\\/]/.test(session.cwd) || /^\\\\/.test(session.cwd) || /^\/[A-Za-z]\//.test(session.cwd)
}

export function getResumePreferenceKey(session: Pick<SessionSummary, 'agent_type' | 'project' | 'cwd'>): string {
  return `resume-shell:${session.agent_type.toLowerCase()}:${session.project || session.cwd}`
}

export function getResumeCommandOptions(
  session: SessionSummary,
  preferredShell: ResumeShell | null = null,
): ResumeCommandOption[] {
  const args = agentResumeArgs(session)
  if (!args) return []

  const powershellCommand = `& ${args.map(quotePowerShell).join(' ')}`
  const bashCommand = args.map(quoteBash).join(' ')
  const commands: Record<ResumeShell, string> = {
    powershell: session.cwd
      ? `Set-Location -LiteralPath ${quotePowerShell(session.cwd)}; ${powershellCommand}`
      : powershellCommand,
    'git-bash': session.cwd
      ? `cd ${quoteBash(toGitBashPath(session.cwd))} && ${bashCommand}`
      : bashCommand,
  }

  const recordedShell = session.shell_kind === 'powershell' || session.shell_kind === 'git-bash'
    ? session.shell_kind
    : null
  const primary = recordedShell ?? preferredShell ?? 'powershell'
  const shells: ResumeShell[] = primary === 'powershell' ? ['powershell', 'git-bash'] : ['git-bash', 'powershell']

  return shells.map(shell => ({
    shell,
    command: commands[shell],
    reason: shell === recordedShell ? 'last-used' : !recordedShell && shell === primary ? 'recommended' : null,
  }))
}
