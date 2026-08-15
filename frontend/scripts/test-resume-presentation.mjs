import assert from 'node:assert/strict'
import {
  isSessionWriting,
  preloadedResumePlanForCopy,
  presentResumeControl,
  presentResumeMenu,
  presentSidebarResume,
  resumeBindingLabelKey,
  SESSION_WRITING_WINDOW_MS,
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
assert.equal(presentation.canLaunch, true)
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
assert.equal(presentation.running, true)
assert.equal(presentation.canLaunch, false)
assert.equal(presentation.primaryLabelKey, 'resume.runningUnknown')

// A live plan with a "none" binding must not look idle / continuable.
presentation = presentResumeControl({ ...plan, status: 'session_running' }, terminal, false)
assert.equal(presentation.state, 'active_unknown')
assert.equal(presentation.canLaunch, false)
assert.equal(presentation.primaryLabelKey, 'resume.runningUnknown')
assert.equal(presentation.continueBlockedReasonKey, 'resume.continueBlockedRunning')

presentation = presentResumeControl({ ...plan, status: 'cwd_unavailable' }, terminal, false)
assert.equal(presentation.primaryLabelKey, 'resume.workspaceMissing')
assert.equal(presentResumeControl(plan, terminal, true).primaryLabelKey, 'resume.working')

// Process-liveness false-negative while the transcript is still growing.
presentation = presentResumeControl(plan, terminal, false, { emitting: true })
assert.equal(presentation.ready, true)
assert.equal(presentation.canLaunch, false)
assert.equal(presentation.emitting, true)
assert.equal(presentation.primaryLabelKey, 'resume.writing')
assert.equal(presentation.continueBlockedReasonKey, 'resume.continueBlockedWriting')

// Session detail says live even if the resume plan is still "ready".
presentation = presentResumeControl(plan, terminal, false, { sessionLive: true })
assert.equal(presentation.running, true)
assert.equal(presentation.canLaunch, false)
assert.equal(presentation.primaryLabelKey, 'resume.runningUnknown')
assert.equal(presentation.continueBlockedReasonKey, 'resume.continueBlockedRunning')

// Running takes precedence over writing for the blocked reason.
presentation = presentResumeControl(plan, terminal, false, { sessionLive: true, emitting: true })
assert.equal(presentation.continueBlockedReasonKey, 'resume.continueBlockedRunning')
assert.equal(presentation.primaryLabelKey, 'resume.runningUnknown')

const now = Date.parse('2026-08-15T12:00:00.000Z')
assert.equal(isSessionWriting(new Date(now - 1_000).toISOString(), now), true)
assert.equal(isSessionWriting(new Date(now - SESSION_WRITING_WINDOW_MS).toISOString(), now), false)
assert.equal(isSessionWriting(new Date(now - SESSION_WRITING_WINDOW_MS + 1).toISOString(), now), true)
assert.equal(isSessionWriting(undefined, now), false)
assert.equal(isSessionWriting('not-a-date', now), false)

assert.equal(resumeBindingLabelKey(terminal), 'resume.bindingUnknown')
assert.equal(resumeBindingLabelKey({ ...terminal, confidence: 'exact' }), 'resume.bindingExact')
assert.equal(resumeBindingLabelKey({ ...terminal, confidence: 'instance' }), 'resume.bindingInstance')

const idleMenu = presentResumeMenu(plan, presentResumeControl(plan, terminal, false))
assert.deepEqual(idleMenu.map(item => item.kind), ['continue', 'copy', 'unsafe'])
assert.ok(idleMenu.every(item => item.enabled))

const writingMenu = presentResumeMenu(plan, presentResumeControl(plan, terminal, false, { emitting: true }))
assert.equal(writingMenu.find(item => item.kind === 'continue').enabled, false)
assert.equal(writingMenu.find(item => item.kind === 'continue').disabledReasonKey, 'resume.continueBlockedWriting')
assert.equal(writingMenu.find(item => item.kind === 'copy').enabled, true)
assert.equal(writingMenu.find(item => item.kind === 'unsafe').enabled, false)

const focusMenu = presentResumeMenu(plan, presentResumeControl(plan, {
  ...terminal, state: 'active', terminal_name: 'Konsole', confidence: 'exact', focusable: true,
}, false))
assert.equal(focusMenu[0].kind, 'focus')
assert.equal(focusMenu[0].enabled, true)

const sidebar = presentSidebarResume(plan, true)
assert.equal(sidebar.command, plan.command)
assert.equal(sidebar.isLive, false, 'the preloaded plan is authoritative over the fallback snapshot')
assert.equal(sidebar.supportsUnsafe, true)
assert.equal(presentSidebarResume(null, true).isLive, true)
assert.equal(preloadedResumePlanForCopy(plan, false), plan)
assert.equal(preloadedResumePlanForCopy(plan, true), null)

console.log('resume presentation tests passed')
