// Unit coverage for the canonical token totals shared by the replay header,
// minimap, and analytics. The three input buckets are mutually exclusive:
// headline totals must include cache reads and writes.
import assert from 'node:assert/strict'
import { totalInputTokens, totalTokenCount } from '../.runtime/token-buckets-test/tokenBuckets.js'

const usage = (prompt, completion, cacheRead, cacheWrite) => ({
  prompt_tokens: prompt,
  completion_tokens: completion,
  reasoning_tokens: 0,
  cache_read_tokens: cacheRead,
  cache_write_tokens: cacheWrite,
  premium_requests: 0,
})

assert.equal(totalInputTokens(usage(100, 50, 900, 300)), 1300)
assert.equal(totalTokenCount(usage(100, 50, 900, 300)), 1350)
assert.equal(totalTokenCount(usage(0, 0, 0, 0)), 0)
assert.equal(totalTokenCount(usage(7, 11, 0, 0)), 18)

console.log('token bucket tests passed')
