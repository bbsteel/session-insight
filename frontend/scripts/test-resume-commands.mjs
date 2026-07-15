import assert from 'node:assert/strict'
import { getResumeCommandOptions, isWindowsSession, toGitBashPath } from '/tmp/session-insight-resume-commands/resumeCommands.js'

const session = {
  id: 'rollout-date-abc-123', resume_id: 'abc-123', agent_type: 'codex', name: '', model_name: '', repository: '', branch: '',
  project: 'demo', cwd: "C:\\work\\John's project", preview_text: '', turn_count: 0,
  message_count: 0, is_live: false, bookmarked: false, created_at: '', updated_at: '',
}

assert.equal(toGitBashPath('C:\\Users\\me\\repo'), '/c/Users/me/repo')
assert.equal(isWindowsSession(session), true)

const defaults = getResumeCommandOptions(session)
assert.equal(defaults[0].shell, 'powershell')
assert.equal(defaults[0].reason, 'recommended')
assert.equal(defaults[0].mode, 'standard')
assert.equal(defaults[0].command, "Set-Location -LiteralPath 'C:\\work\\John''s project'; & 'codex' 'resume' 'abc-123'")
assert.equal(defaults[1].mode, 'skip-permissions')
assert.equal(defaults[1].command, "Set-Location -LiteralPath 'C:\\work\\John''s project'; & 'codex' '--dangerously-bypass-approvals-and-sandbox' 'resume' 'abc-123'")
assert.equal(defaults[2].command, "cd '/c/work/John'\"'\"'s project' && 'codex' 'resume' 'abc-123'")

const codexWithoutNativeId = getResumeCommandOptions({ ...session, resume_id: '' })
assert.deepEqual(codexWithoutNativeId, [])

const preferred = getResumeCommandOptions(session, 'git-bash')
assert.equal(preferred[0].shell, 'git-bash')
assert.equal(preferred[0].reason, 'recommended')

const recorded = getResumeCommandOptions({ ...session, shell_kind: 'git-bash' }, 'powershell')
assert.equal(recorded[0].shell, 'git-bash')
assert.equal(recorded[0].reason, 'last-used')
assert.equal(recorded[2].reason, null)

const claude = getResumeCommandOptions({ ...session, agent_type: 'claude' })
assert.equal(claude[1].command.includes("'--dangerously-skip-permissions'"), true)

const opencode = getResumeCommandOptions({ ...session, agent_type: 'opencode' })
assert.equal(opencode[1].command.includes("'--auto'"), true)

const chrys = getResumeCommandOptions({ ...session, agent_type: 'chrys' })
assert.equal(chrys.length, 2)
assert.equal(chrys.some(option => option.mode === 'skip-permissions'), false)

const grok = getResumeCommandOptions({ ...session, agent_type: 'grok', resume_id: '', id: '019f61d0-4553-70d3-ba91-e67bf43f1fea' })
assert.equal(grok[0].mode, 'standard')
assert.equal(grok[0].command.includes("'grok' '--resume' '019f61d0-4553-70d3-ba91-e67bf43f1fea'"), true)
assert.equal(grok[1].mode, 'skip-permissions')
assert.equal(grok[1].command.includes("'--always-approve'"), true)
assert.equal(grok[1].command.includes("'--resume'"), true)

console.log('resume command tests passed')
