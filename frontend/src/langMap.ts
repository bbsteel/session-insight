// File extension → Prism language id, shared by the file viewer and the
// diff modal's per-line highlighting.

const EXT_LANG: Record<string, string> = {
  ts: 'typescript', tsx: 'tsx', js: 'javascript', jsx: 'jsx', mjs: 'javascript', cjs: 'javascript',
  go: 'go', py: 'python', rs: 'rust', java: 'java', kt: 'kotlin', rb: 'ruby', php: 'php',
  c: 'c', h: 'c', cpp: 'cpp', cc: 'cpp', hpp: 'cpp', cs: 'csharp', swift: 'swift',
  css: 'css', scss: 'scss', less: 'less', html: 'markup', xml: 'markup', vue: 'markup', svelte: 'markup',
  json: 'json', yaml: 'yaml', yml: 'yaml', toml: 'toml', ini: 'ini',
  md: 'markdown', sh: 'bash', bash: 'bash', zsh: 'bash', sql: 'sql', diff: 'diff',
}

export function langForPath(path: string): string {
  const ext = path.split('.').pop()?.toLowerCase() ?? ''
  return EXT_LANG[ext] ?? 'text'
}
