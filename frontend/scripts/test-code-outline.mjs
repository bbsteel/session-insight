import assert from 'node:assert/strict'
import { pythonLanguage } from '@codemirror/lang-python'
import { javaLanguage } from '@codemirror/lang-java'
import { markdownLanguage } from '@codemirror/lang-markdown'
import { goLanguage } from '@codemirror/lang-go'
import { javascriptLanguage, typescriptLanguage } from '@codemirror/lang-javascript'
import { rustLanguage } from '@codemirror/lang-rust'
import { rubyLanguage } from 'codemirror-lang-ruby'
import { csharpLanguage } from '@replit/codemirror-lang-csharp'
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

{
  const source = `package main

func Hello() {}

type Foo struct{}

func (f *Foo) Bar() {}

type Reader interface {
	Read()
}
`
  const outline = outlineFromTree(goLanguage.parser.parse(source), source, 'go')
  assert.deepEqual(outline.map(x => [x.name, x.kind]), [
    ['Hello', 'function'],
    ['Foo', 'class'],
    ['Bar', 'method'],
    ['Reader', 'interface'],
  ])
  assert.deepEqual(outline[3].children.map(x => [x.name, x.kind]), [['Read', 'method']])
}

{
  const source = `export function foo() {}
class Bar {
  constructor() {}
  method() {}
}
`
  const outline = outlineFromTree(javascriptLanguage.parser.parse(source), source, 'javascript')
  assert.deepEqual(outline.map(x => [x.name, x.kind]), [
    ['foo', 'function'],
    ['Bar', 'class'],
  ])
  assert.deepEqual(outline[1].children.map(x => [x.name, x.kind]), [
    ['constructor', 'constructor'],
    ['method', 'method'],
  ])
}

{
  const source = `export interface I { x: number }
enum E { A }
class C { m(): void {} }
`
  const outline = outlineFromTree(typescriptLanguage.parser.parse(source), source, 'javascript')
  assert.deepEqual(outline.map(x => [x.name, x.kind]), [
    ['I', 'interface'],
    ['E', 'enum'],
    ['C', 'class'],
  ])
  assert.deepEqual(outline[2].children.map(x => [x.name, x.kind]), [['m', 'method']])
}

{
  const source = `fn main() {}
struct Foo {}
impl Foo {
  fn bar(&self) {}
}
enum E { A }
`
  const outline = outlineFromTree(rustLanguage.parser.parse(source), source, 'rust')
  assert.deepEqual(outline.map(x => [x.name, x.kind]), [
    ['main', 'function'],
    ['Foo', 'class'],
    ['Foo', 'class'],
    ['E', 'enum'],
  ])
  assert.deepEqual(outline[2].children.map(x => [x.name, x.kind]), [['bar', 'method']])
}

{
  const source = `module App
  class Worker
    def run
      def nested; end
    end
  end
  def top; end
end
`
  const outline = outlineFromTree(rubyLanguage.parser.parse(source), source, 'ruby')
  assert.deepEqual(outline.map(x => [x.name, x.kind]), [['App', 'class']])
  assert.deepEqual(outline[0].children.map(x => [x.name, x.kind]), [
    ['Worker', 'class'],
    ['top', 'method'],
  ])
  assert.deepEqual(outline[0].children[0].children.map(x => [x.name, x.kind]), [['run', 'method']])
  assert.deepEqual(outline[0].children[0].children[0].children.map(x => [x.name, x.kind]), [['nested', 'method']])
}

{
  const source = `namespace Demo {
  public class Service {
    public void Execute() {}
    public void Stop() {}
  }
  public interface IListener {
    void Changed();
  }
  public enum State { Ready }
}
`
  const outline = outlineFromTree(csharpLanguage.parser.parse(source), source, 'csharp')
  assert.deepEqual(outline.map(x => [x.name, x.kind]), [
    ['Service', 'class'],
    ['IListener', 'interface'],
    ['State', 'enum'],
  ])
  assert.deepEqual(outline[0].children.map(x => [x.name, x.kind]), [
    ['Execute', 'method'],
    ['Stop', 'method'],
  ])
  assert.deepEqual(outline[1].children.map(x => [x.name, x.kind]), [['Changed', 'method']])
}

console.log('code outline tests passed')
