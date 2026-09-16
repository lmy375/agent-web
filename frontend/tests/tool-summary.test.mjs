import assert from 'node:assert/strict'
import test from 'node:test'
import { toolPreview, toolSummary } from '../src/lib/toolSummary.ts'

test('shell activity uses description while approvals retain the real command', () => {
  const input = { command: "find backend/internal -name '*.go' | sort", description: 'List Go files under backend/internal' }
  assert.equal(toolPreview('shell', 'Bash', input), input.description)
  assert.equal(toolSummary('shell', 'Bash', input), input.command)
})

test('shell preview falls back to command when description is missing or blank', () => {
  for (const description of [undefined, null, '', '   ', 42]) {
    assert.equal(toolPreview('shell', 'Bash', { command: 'pwd', description }), 'pwd')
  }
  assert.equal(toolPreview('shell', 'Bash', { description: '  Inspect project  ' }), 'Inspect project')
})

test('non-shell tools preserve their existing summaries', () => {
  assert.equal(toolPreview('file_read', 'Read', { file_path: '/src/App.tsx', description: 'irrelevant' }), '/src/App.tsx')
})
