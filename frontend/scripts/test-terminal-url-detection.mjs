import assert from 'node:assert/strict'
import { extractTerminalUrl } from '/tmp/session-insight-terminal-url/terminalUrlDetection.js'
import { resolveMatcherTooltip } from '/tmp/session-insight-terminal-url/terminalControl.js'

assert.equal(
  extractTerminalUrl('• PR：Fix session analysis provider return (https://github.com/bbsteel/session-insight/pull/87)'),
  'https://github.com/bbsteel/session-insight/pull/87',
)
assert.equal(extractTerminalUrl('See https://example.test/docs.'), 'https://example.test/docs')
assert.equal(
  extractTerminalUrl('https://shown.example (https://target.example/pull/88)'),
  'https://target.example/pull/88',
)
assert.equal(
  extractTerminalUrl('Read (https://example.test/a_(b)).'),
  'https://example.test/a_(b)',
)
assert.equal(extractTerminalUrl('See https://example.test/?q=!'), 'https://example.test/?q=!')
assert.equal(extractTerminalUrl('See https://example.test/path!'), 'https://example.test/path!')
assert.equal(
  extractTerminalUrl('**PR #143**: https://github.com/bbsteel/session-insight/pull/143（ready for review，未合并）'),
  'https://github.com/bbsteel/session-insight/pull/143',
)
assert.equal(
  extractTerminalUrl('https://github.com/bbsteel/session-insight/pull/143（ready'),
  'https://github.com/bbsteel/session-insight/pull/143',
)
assert.equal(extractTerminalUrl('见 https://example.test/docs。'), 'https://example.test/docs')
assert.equal(extractTerminalUrl('见 https://example.test/docs，然后'), 'https://example.test/docs')
assert.equal(extractTerminalUrl('打开 https://zh.wikipedia.org/wiki/测试 页面'), 'https://zh.wikipedia.org/wiki/测试')
assert.equal(extractTerminalUrl('no external link'), null)
assert.equal(extractTerminalUrl('javascript:alert(1)'), null)

assert.equal(resolveMatcherTooltip(undefined, null), '')
assert.equal(resolveMatcherTooltip('Open link in new tab', 'https://example.test'), 'Open link in new tab')
assert.equal(
  resolveMatcherTooltip(
    (url) => `Open link in new tab\n${url}`,
    'https://www.kimi.com/code/#pricing',
  ),
  'Open link in new tab\nhttps://www.kimi.com/code/#pricing',
)
console.log('terminal URL detection tests passed')
