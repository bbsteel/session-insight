import assert from 'node:assert/strict'
import { splitHandoffOutput } from '/tmp/session-insight-handoff-output/api.js'

const metadata = {
  difficulty: '中等',
  difficulty_reason: '需要确认现有 UI 状态',
  recommended: [{ executor: 'Codex CLI', reason: '适合本地仓库' }],
}
const body = '# 任务交接\n\n继续修复会话交接渲染。'
const fenced = `\`\`\`json\n${JSON.stringify(metadata)}\n\`\`\``

assert.deepEqual(splitHandoffOutput(`${fenced}\n\n${body}`), { content: body, metadata })
assert.deepEqual(splitHandoffOutput(`我会先核对现状。\n\n${fenced}\n\n${body}`), { content: body, metadata })
assert.deepEqual(splitHandoffOutput(`说明配置。\n\n\`\`\`json\n{"port":8080}\n\`\`\`\n\n${body}`), {
  content: `说明配置。\n\n\`\`\`json\n{"port":8080}\n\`\`\`\n\n${body}`,
  metadata: null,
})
for (const invalidRecommended of [null, [{}]]) {
  const invalidContent = `\`\`\`json\n${JSON.stringify({ difficulty: '中等', recommended: invalidRecommended })}\n\`\`\`\n\n${body}`
  assert.deepEqual(splitHandoffOutput(invalidContent), { content: invalidContent, metadata: null })
}

console.log('handoff output tests passed')
