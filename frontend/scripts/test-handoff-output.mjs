import assert from 'node:assert/strict'
import {
  generateAI, ModelUnavailableError, splitHandoffOutput,
} from '/tmp/session-insight-handoff-output/api.js'
import { registerRuntimeTranslator } from '/tmp/session-insight-handoff-output/i18nRuntime.js'

registerRuntimeTranslator(key => `[${key}]`)

const metadata = {
  difficulty: '中等',
  difficulty_reason: '需要确认现有 UI 状态',
  recommended: [{ executor: 'Codex CLI', reason: '适合本地仓库' }],
}
const restartedMetadata = {
  difficulty: '困难',
  difficulty_reason: '重启后的评估',
  recommended: [{ executor: 'Claude Code CLI', reason: '匹配任务' }],
}
const body = '# 任务交接\n\n继续修复会话交接渲染。'
const fenced = `\`\`\`json\n${JSON.stringify(metadata)}\n\`\`\``

assert.deepEqual(splitHandoffOutput(`${fenced}\n\n${body}`), { content: body, metadata })
assert.deepEqual(splitHandoffOutput(`我会先核对现状。\n\n${fenced}\n\n${body}`), { content: body, metadata })
assert.deepEqual(splitHandoffOutput(`${fenced}\n\n\`\`\`markdown\n${body}\n\`\`\``), { content: body, metadata })
assert.deepEqual(splitHandoffOutput(`\`\`\`markdown\n${fenced}\n\n${body}\n\`\`\``), { content: body, metadata })
const restartedFence = `\`\`\`json\n${JSON.stringify(restartedMetadata)}\n\`\`\``
assert.deepEqual(splitHandoffOutput(`${fenced}\n\n# 任务交接\n\n错误的第一稿。\nPR: https://example.test/41${restartedFence}\n\n${body}`), {
  content: body,
  metadata: restartedMetadata,
})
assert.deepEqual(splitHandoffOutput(`说明配置。\n\n\`\`\`json\n{"port":8080}\n\`\`\`\n\n${body}`), {
  content: `说明配置。\n\n\`\`\`json\n{"port":8080}\n\`\`\`\n\n${body}`,
  metadata: null,
})
const handoffExample = `${body}\n\n示例：\n${fenced}\n\n继续执行测试。`
assert.deepEqual(splitHandoffOutput(handoffExample), { content: handoffExample, metadata: null })
for (const invalidRecommended of [null, [{}]]) {
  const invalidContent = `\`\`\`json\n${JSON.stringify({ difficulty: '中等', recommended: invalidRecommended })}\n\`\`\`\n\n${body}`
  assert.deepEqual(splitHandoffOutput(invalidContent), { content: invalidContent, metadata: null })
}

const unavailableMessage = '模型「gpt-5.4-mini」已无法由 Codex CLI 使用；请刷新模型源并改选其他型号'
globalThis.fetch = async () => new Response(
  `event: error\ndata: ${JSON.stringify({
    message: unavailableMessage,
    code: 'model_unavailable',
    provider_id: 17,
  })}\n\n`,
  { status: 200, headers: { 'Content-Type': 'text/event-stream' } },
)
await assert.rejects(
  generateAI('session-1', 'handoff', () => {}),
  error => error instanceof ModelUnavailableError
    && error.message === '[error.model_unavailable]'
    && error.providerId === 17,
)

console.log('handoff output tests passed')
