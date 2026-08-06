import assert from 'node:assert/strict'
import {
  preloadedResumePlanForCopy,
  presentResumeControl,
  presentSidebarResume,
} from '/tmp/session-insight-resume-presentation/resumePresentation.js'

const terminal = {
  state: 'none', session_live: false, liveness_state: 'exact', confidence: 'unknown', focusable: false,
}
const plan = {
  status: 'ready', agent_type: 'codex', session_id: 's1', cwd: '/tmp/project', command: "codex resume s1",
  supports_unsafe: true, liveness: { is_live: false, state: 'exact' }, terminal,
}

let presentation = presentResumeControl(plan, terminal, false)
assert.equal(presentation.state, 'none')
assert.equal(presentation.active, false)
assert.equal(presentation.canFocus, false)
assert.equal(presentation.ready, true)
assert.equal(presentation.primaryLabelKey, 'resume.continue')

presentation = presentResumeControl(plan, { ...terminal, state: 'launching' }, false)
assert.equal(presentation.primaryLabelKey, 'resume.starting')

presentation = presentResumeControl(plan, {
  ...terminal, state: 'active', terminal_name: 'Konsole', tab_id: '9', confidence: 'exact', focusable: true,
}, false)
assert.equal(presentation.active, true)
assert.equal(presentation.canFocus, true)
assert.equal(presentation.primaryLabelKey, 'resume.returnTerminal')
assert.equal(presentation.preferTerminalLabel, true)

presentation = presentResumeControl({ ...plan, status: 'session_running' }, null, false)
assert.equal(presentation.state, 'active_unknown')
assert.equal(presentation.primaryLabelKey, 'resume.runningUnknown')

presentation = presentResumeControl({ ...plan, status: 'cwd_unavailable' }, terminal, false)
assert.equal(presentation.primaryLabelKey, 'resume.workspaceMissing')
assert.equal(presentResumeControl(plan, terminal, true).primaryLabelKey, 'resume.working')

const sidebar = presentSidebarResume(plan, true)
assert.equal(sidebar.command, plan.command)
assert.equal(sidebar.isLive, false, 'the preloaded plan is authoritative over the fallback snapshot')
assert.equal(sidebar.supportsUnsafe, true)
assert.equal(presentSidebarResume(null, true).isLive, true)
assert.equal(preloadedResumePlanForCopy(plan, false), plan)
assert.equal(preloadedResumePlanForCopy(plan, true), null)

console.log('resume presentation tests passed')
