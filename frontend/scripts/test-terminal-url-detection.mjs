import assert from 'node:assert/strict'
import { extractTerminalUrl } from '/tmp/session-insight-terminal-url/terminalUrlDetection.js'

assert.equal(
  extractTerminalUrl('• PR：Fix session analysis provider return (https://github.com/bbsteel/session-insight/pull/87)'),
  'https://github.com/bbsteel/session-insight/pull/87',
)
assert.equal(extractTerminalUrl('See https://example.test/docs.'), 'https://example.test/docs')
assert.equal(extractTerminalUrl('no external link'), null)
assert.equal(extractTerminalUrl('javascript:alert(1)'), null)
console.log('terminal URL detection tests passed')
