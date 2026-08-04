/**
 * Durable logic tests: session deep-link helpers (src/sessionLink.ts) and the
 * shared session-ID copy logic (src/copySessionId.ts).
 *
 * Covers the #/session/<agent>/<id> hash route build/parse round-trip (with
 * URL-encoding), the new-tab click predicate (ctrl/cmd-click, middle-click),
 * and the resume_id || id copy fallback shared by the session list and the
 * collaboration dock.
 */

import assert from 'node:assert/strict'
import { pathToFileURL } from 'node:url'

const mod = '/tmp/session-insight-session-link'
const { sessionHash, parseSessionRoute, shouldOpenInNewTab } = await import(pathToFileURL(`${mod}/sessionLink.js`).href)
const { sessionCopyId, copySessionIdToClipboard } = await import(pathToFileURL(`${mod}/copySessionId.js`).href)

// --- Hash route build/parse ---------------------------------------------------

assert.equal(sessionHash('grok', 'abc-123'), '#/session/grok/abc-123')
assert.deepEqual(parseSessionRoute('#/session/grok/abc-123'), { agentType: 'grok', id: 'abc-123' })

// Round-trip with characters that require URL encoding (spaces, slashes,
// percent): encoded segments never confuse the two-segment parse.
const tricky = sessionHash('weird agent', 'a/b%c')
assert.equal(tricky, '#/session/weird%20agent/a%2Fb%25c')
assert.deepEqual(parseSessionRoute(tricky), { agentType: 'weird agent', id: 'a/b%c' })

// Malformed or unrelated hashes are rejected.
assert.equal(parseSessionRoute(''), null)
assert.equal(parseSessionRoute('#/file?path=/tmp/x'), null)
assert.equal(parseSessionRoute('#/session'), null)
assert.equal(parseSessionRoute('#/session/'), null)
assert.equal(parseSessionRoute('#/session/grok'), null)
assert.equal(parseSessionRoute('#/session/grok/'), null)
assert.equal(parseSessionRoute('#/session//abc'), null)
assert.equal(parseSessionRoute('#/session/a/b/c'), null)

// Malformed percent-encoding degrades to null instead of throwing URIError
// (App.tsx parses the hash in a useState initializer; a throw would crash load).
assert.equal(parseSessionRoute('#/session/foo%bar/baz'), null)
assert.equal(parseSessionRoute('#/session/grok/%'), null)
assert.equal(parseSessionRoute('#/session/%/x'), null)

// --- New-tab click predicate ----------------------------------------------------

assert.equal(shouldOpenInNewTab({ metaKey: true, ctrlKey: false }), true)
assert.equal(shouldOpenInNewTab({ metaKey: false, ctrlKey: true }), true)
assert.equal(shouldOpenInNewTab({ metaKey: false, ctrlKey: false, button: 1 }), true)
assert.equal(shouldOpenInNewTab({ metaKey: false, ctrlKey: false, button: 0 }), false)
assert.equal(shouldOpenInNewTab({ metaKey: false, ctrlKey: false }), false)

// --- Shared copy ID selection ---------------------------------------------------

assert.equal(sessionCopyId({ id: 'sess', resume_id: 'resume' }), 'resume')
assert.equal(sessionCopyId({ id: 'sess' }), 'sess')
assert.equal(sessionCopyId({ id: 'sess', resume_id: '' }), 'sess')
assert.equal(sessionCopyId({ id: 'sess', resume_id: null }), 'sess')

// copySessionIdToClipboard honors the same selection and reports failure.
{
  let written = null
  const original = globalThis.navigator
  Object.defineProperty(globalThis, 'navigator', {
    value: { clipboard: { writeText: async (text) => { written = text } } },
    configurable: true,
  })
  assert.equal(await copySessionIdToClipboard({ id: 'sess', resume_id: 'resume' }), true)
  assert.equal(written, 'resume')

  Object.defineProperty(globalThis, 'navigator', {
    value: { clipboard: { writeText: async () => { throw new Error('denied') } } },
    configurable: true,
  })
  assert.equal(await copySessionIdToClipboard({ id: 'sess' }), false)
  Object.defineProperty(globalThis, 'navigator', { value: original, configurable: true })
}

console.log('session-link + copy-session-id tests passed')
