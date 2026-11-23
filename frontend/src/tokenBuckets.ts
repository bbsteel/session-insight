import type { TokenUsage } from './types'

/**
 * Full prompt-side volume. The three input buckets are mutually exclusive:
 * uncached prompt, cache read, and cache write. Cache reads are still tokens
 * the model processed, so headline totals must include them.
 */
export function totalInputTokens(usage: TokenUsage): number {
  return usage.prompt_tokens + usage.cache_read_tokens + usage.cache_write_tokens
}

/** Every token the model processed: full input plus generated output. */
export function totalTokenCount(usage: TokenUsage): number {
  return totalInputTokens(usage) + usage.completion_tokens
}
