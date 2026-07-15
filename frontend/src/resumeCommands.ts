import type { SessionSummary } from './types'

export type ResumeShell = 'powershell' | 'git-bash'
export type ResumeShellReason = 'last-used' | 'recommended' | null
export type ResumeCommandMode = 'standard' | 'skip-permissions'

export interface ResumeCommandOption {
  shell: ResumeShell
  command: string
  reason: ResumeShellReason
  mode: ResumeCommandMode
}

interface AgentResumeArgs {
  standard: string[]
  skipPermissions?: string[]
}

function agentResumeArgs(session: SessionSummary): AgentResumeArgs | null {
  const agent = session.agent_type.toLowerCase()
  // A Codex session's UI id is the rollout filename stem, which the CLI does
  // not accept. Never fall back to it while the native UUID is being indexed.
  if (agent.includes('codex') && !session.resume_id) return null
  const id = session.resume_id || session.id
  if (agent.includes('claude')) return {
    standard: ['claude', '--resume', id],
    skipPermissions: ['claude', '--dangerously-skip-permissions', '--resume', id],
  }
  if (agent.includes('codex')) return {
    standard: ['codex', 'resume', id],
    skipPermissions: ['codex', '--dangerously-bypass-approvals-and-sandbox', 'resume', id],
  }
  if (agent.includes('opencode')) return {
    standard: ['opencode', '-s', id],
    skipPermissions: ['opencode', '--auto', '-s', id],
  }
  if (agent.includes('chrys')) return { standard: ['chrys', '-s', id] }
  if (agent.includes('grok')) return {
    standard: ['grok', '--resume', id],
    skipPermissions: ['grok', '--always-approve', '--resume', id],
  }
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
  const resumeArgs = agentResumeArgs(session)
  if (!resumeArgs) return []

  const recordedShell = session.shell_kind === 'powershell' || session.shell_kind === 'git-bash'
    ? session.shell_kind
    : null
  const primary = recordedShell ?? preferredShell ?? 'powershell'
  const shells: ResumeShell[] = primary === 'powershell' ? ['powershell', 'git-bash'] : ['git-bash', 'powershell']

  return shells.flatMap(shell => {
    const reason = shell === recordedShell ? 'last-used' : !recordedShell && shell === primary ? 'recommended' : null
    const variants: [ResumeCommandMode, string[]][] = [['standard', resumeArgs.standard]]
    if (resumeArgs.skipPermissions) variants.push(['skip-permissions', resumeArgs.skipPermissions])
    return variants.map(([mode, args]) => {
      const agentCommand = shell === 'powershell'
        ? `& ${args.map(quotePowerShell).join(' ')}`
        : args.map(quoteBash).join(' ')
      const command = !session.cwd
        ? agentCommand
        : shell === 'powershell'
          ? `Set-Location -LiteralPath ${quotePowerShell(session.cwd)}; ${agentCommand}`
          : `cd ${quoteBash(toGitBashPath(session.cwd))} && ${agentCommand}`
      return { shell, command, reason, mode }
    })
  })
}
