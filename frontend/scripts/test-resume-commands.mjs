import assert from 'node:assert/strict'
import { getResumeCommandOptions, isWindowsSession, toGitBashPath } from '/tmp/session-insight-resume-commands/resumeCommands.js'

const session = {
  id: 'abc-123', agent_type: 'codex', name: '', model_name: '', repository: '', branch: '',
  project: 'demo', cwd: "C:\\work\\John's project", preview_text: '', turn_count: 0,
  message_count: 0, is_live: false, bookmarked: false, created_at: '', updated_at: '',
}

assert.equal(toGitBashPath('C:\\Users\\me\\repo'), '/c/Users/me/repo')
assert.equal(isWindowsSession(session), true)

const defaults = getResumeCommandOptions(session)
assert.equal(defaults[0].shell, 'powershell')
assert.equal(defaults[0].reason, 'recommended')
assert.equal(defaults[0].command, "Set-Location -LiteralPath 'C:\\work\\John''s project'; & 'codex' 'resume' 'abc-123'")
assert.equal(defaults[1].command, "cd '/c/work/John'\"'\"'s project' && 'codex' 'resume' 'abc-123'")

const preferred = getResumeCommandOptions(session, 'git-bash')
assert.equal(preferred[0].shell, 'git-bash')
assert.equal(preferred[0].reason, 'recommended')

const recorded = getResumeCommandOptions({ ...session, shell_kind: 'git-bash' }, 'powershell')
assert.equal(recorded[0].shell, 'git-bash')
assert.equal(recorded[0].reason, 'last-used')
assert.equal(recorded[1].reason, null)

console.log('resume command tests passed')
