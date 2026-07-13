export const LARGE_FILE_PARSE_WARNING_BYTES = 1 << 20
export const MAX_FILE_PARSE_BYTES = 2 << 20

export type CodeParsePolicy = 'enabled' | 'confirm' | 'confirmed' | 'refused'

export function codeParsePolicy(size: number, truncated: boolean): CodeParsePolicy {
  if (truncated || size > MAX_FILE_PARSE_BYTES) return 'refused'
  if (size >= LARGE_FILE_PARSE_WARNING_BYTES) return 'confirm'
  return 'enabled'
}

export function formatByteSize(size: number): string {
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(size < 10 * 1024 ? 1 : 0)} KiB`
  return `${(size / (1024 * 1024)).toFixed(1)} MiB`
}
