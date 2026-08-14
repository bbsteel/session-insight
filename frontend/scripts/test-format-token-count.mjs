import assert from 'node:assert/strict'
import { formatTokenCount } from '../.runtime/format-token-count-test/formatTokenCount.js'

// full mode
assert.equal(formatTokenCount('en', 1234567, 'full'), '1,234,567')
assert.equal(formatTokenCount('zh-CN', 1234567, 'full'), '1,234,567')

// compact en
assert.equal(formatTokenCount('en', 0, 'compact'), '0')
assert.equal(formatTokenCount('en', 999, 'compact'), '999')
assert.equal(formatTokenCount('en', 1_000, 'compact'), '1K')
assert.equal(formatTokenCount('en', 1_500, 'compact'), '1.5K')
assert.equal(formatTokenCount('en', 1_200_000, 'compact'), '1.2M')
assert.equal(formatTokenCount('en', 1_100_000_000, 'compact'), '1.1B')
assert.equal(formatTokenCount('en', -2_000, 'compact'), '-2K')

// Boundary promotion after rounding (no 1,000K / 1,000M)
assert.equal(formatTokenCount('en', 999_950, 'compact'), '1M')
assert.equal(formatTokenCount('en', -999_950, 'compact'), '-1M')
assert.equal(formatTokenCount('en', 999_500, 'compact'), '999.5K')
assert.equal(formatTokenCount('en', 999_950_000, 'compact'), '1B')
assert.equal(formatTokenCount('en', -999_950_000, 'compact'), '-1B')
assert.equal(formatTokenCount('en', 999_500_000, 'compact'), '999.5M')

// compact zh-CN: tenThousand / hundredMillion units from i18n (tests use ASCII stubs)
const zhUnits = { tenThousand: 'W', hundredMillion: 'Y' }
assert.equal(formatTokenCount('zh-CN', 999, 'compact', zhUnits), '999')
assert.equal(formatTokenCount('zh-CN', 9_999, 'compact', zhUnits), '9,999')
assert.equal(formatTokenCount('zh-CN', 12_000, 'compact', zhUnits), '1.2W')
assert.equal(formatTokenCount('zh-CN', 1_200_000, 'compact', zhUnits), '120W')
assert.equal(formatTokenCount('zh-CN', 150_000_000, 'compact', zhUnits), '1.5Y')

// Boundary promotion: 10,000×tenThousand → hundredMillion
assert.equal(formatTokenCount('zh-CN', 99_999_500, 'compact', zhUnits), '1Y')
assert.equal(formatTokenCount('zh-CN', -99_999_500, 'compact', zhUnits), '-1Y')
assert.equal(formatTokenCount('zh-CN', 99_995_000, 'compact', zhUnits), '9,999.5W')

// default mode is compact
assert.equal(formatTokenCount('en', 2_000_000), '2M')

console.log('formatTokenCount: ok')
