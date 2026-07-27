import assert from 'node:assert/strict'
import { streamRenderANSI } from '/tmp/session-insight-render-stream/api.js'

const encoded = new TextEncoder().encode('\x1b[32mhello 中文\x1b[0m')
const chunks = [
  encoded.slice(0, 8),
  encoded.slice(8, 13),
  encoded.slice(13),
]

globalThis.fetch = async (_url, init) => {
  assert.equal(init.signal instanceof AbortSignal, true)
  return new Response(new ReadableStream({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(chunk)
      controller.close()
    },
  }), {
    status: 200,
    headers: { 'Content-Type': 'text/plain; charset=utf-8' },
  })
}

const received = []
let writing = false
const controller = new AbortController()
const ansi = await streamRenderANSI('session-1', 175, 'user,assistant', async chunk => {
  assert.equal(writing, false, 'onChunk calls must be serialized')
  writing = true
  await Promise.resolve()
  received.push(chunk)
  writing = false
}, controller.signal)

assert.equal(ansi, '\x1b[32mhello 中文\x1b[0m')
assert.equal(received.join(''), ansi)

console.log('render stream tests passed')
