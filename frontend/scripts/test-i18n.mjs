import assert from 'node:assert/strict'
import { formatNumber, messages, systemLocale, translate } from '../.runtime/i18n-test/i18n.js'

assert.equal(systemLocale('zh-TW'), 'zh-CN')
assert.equal(systemLocale('en-GB'), 'en')
assert.equal(translate('zh-CN', 'common.search'), '搜索')
assert.equal(translate('zh-CN', 'app.openSessions'), '打开会话列表')
assert.equal(translate('zh-CN', 'missing.key'), 'missing.key')
assert.equal(translate('zh-CN', 'settings.language'), '语言')
assert.equal(translate('en', 'panel.filterMessages'), 'Filter message content…')
assert.equal(translate('en', 'panel.filterTools'), 'Filter: tool name / arguments…')
assert.equal(formatNumber('en', 12345), '12,345')
for (const key of Object.keys(messages.en)) assert.ok(key in messages['zh-CN'], `zh-CN missing ${key}`)
for (const key of Object.keys(messages['zh-CN'])) assert.ok(key in messages.en, `en missing ${key}`)
console.log('i18n resource alignment and fallback passed')
