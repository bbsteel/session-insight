import assert from 'node:assert/strict'
import { pythonLanguage } from '@codemirror/lang-python'
import { javaLanguage } from '@codemirror/lang-java'
import { markdownLanguage } from '@codemirror/lang-markdown'
import { activeOutlineId, flattenOutline, outlineFromTree } from '/tmp/session-insight-codeOutline.mjs'
import {
  codeParsePolicy,
  formatByteSize,
  LARGE_FILE_PARSE_WARNING_BYTES,
  MAX_FILE_PARSE_BYTES,
} from '/tmp/session-insight-codeReaderPolicy.mjs'

assert.equal(codeParsePolicy(LARGE_FILE_PARSE_WARNING_BYTES - 1, false), 'enabled')
assert.equal(codeParsePolicy(LARGE_FILE_PARSE_WARNING_BYTES, false), 'confirm')
assert.equal(codeParsePolicy(MAX_FILE_PARSE_BYTES, false), 'confirm')
assert.equal(codeParsePolicy(MAX_FILE_PARSE_BYTES + 1, false), 'refused')
assert.equal(codeParsePolicy(10, true), 'refused')
assert.equal(formatByteSize(1536), '1.5 KiB')

{
  const source = `class Worker:
    def run(self):
        def normalize():
            pass
        return normalize()

async def start():
    pass
`
  const outline = outlineFromTree(pythonLanguage.parser.parse(source), source, 'python')
  assert.deepEqual(outline.map(x => [x.name, x.kind, x.line]), [
    ['Worker', 'class', 1],
    ['start', 'function', 7],
  ])
  assert.deepEqual(outline[0].children.map(x => [x.name, x.kind, x.line]), [['run', 'method', 2]])
  assert.deepEqual(outline[0].children[0].children.map(x => [x.name, x.kind]), [['normalize', 'function']])
  assert.equal(activeOutlineId(outline, source.indexOf('return')), outline[0].children[0].id)
}

{
  const source = `public class Service {
  Service() {}
  void execute() {}
  interface Listener { void changed(); }
  enum State { READY }
}`
  const outline = outlineFromTree(javaLanguage.parser.parse(source), source, 'java')
  assert.equal(outline[0].name, 'Service')
  assert.deepEqual(outline[0].children.map(x => [x.name, x.kind]), [
    ['Service', 'constructor'],
    ['execute', 'method'],
    ['Listener', 'interface'],
    ['State', 'enum'],
  ])
  assert.deepEqual(outline[0].children[2].children.map(x => [x.name, x.kind]), [['changed', 'method']])
}

{
  const source = `# Guide

## Install
Text

### Linux
Text

## Usage
Text

Reference
=========
`
  const outline = outlineFromTree(markdownLanguage.parser.parse(source), source, 'markdown')
  assert.deepEqual(outline.map(x => [x.name, x.line]), [['Guide', 1], ['Reference', 12]])
  assert.deepEqual(outline[0].children.map(x => x.name), ['Install', 'Usage'])
  assert.equal(outline[0].children[0].children[0].name, 'Linux')
  assert.equal(flattenOutline(outline).length, 5)
}

console.log('code outline tests passed')
