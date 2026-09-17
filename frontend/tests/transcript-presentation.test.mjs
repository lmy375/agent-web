import assert from 'node:assert/strict'
import test from 'node:test'
import { activityStats, latestThought, presentTranscript } from '../src/lib/transcriptPresentation.ts'

const tool = (id, kind = 'shell', extra = {}) => ({ type: 'tool', id, name: 'run', toolKind: kind, input: { command: `echo ${id}` }, children: [], ...extra })
const assistant = (id, blocks, streaming = false) => ({ kind: 'assistant', id, blocks, streaming })
const text = (value) => ({ type: 'text', text: value, final: true })
const thinking = (value) => ({ type: 'thinking', text: value, final: true })

test('consecutive activity spans message boundaries but never swallows prose', () => {
  const rows = presentTranscript([
    assistant('a', [text('I will inspect the project.'), thinking('Inspecting files'), tool('1')]),
    assistant('b', [text('  '), tool('2'), thinking('Checking results')]),
    assistant('c', [text('Here is the result.'), tool('3')]),
  ])
  assert.deepEqual(rows.map((row) => row.kind), ['text', 'activity', 'text', 'activity'])
  assert.equal(rows[1].entries.length, 4)
  assert.equal(rows[2].text, 'Here is the result.')
  assert.deepEqual(rows[1].entries.filter((entry) => entry.kind === 'tool').map((entry) => entry.block.id), ['1', '2'])
})

test('user messages, errors and context boundaries remain visible and separate groups', () => {
  for (const boundary of [
    { kind: 'user', id: 'user', blocks: [text('Next task')] },
    { kind: 'alert', id: 'error', code: 'other', message: 'error detail', fatal: true },
    { kind: 'note', id: 'context', label: '', contextBoundary: true, tokensBefore: 1000 },
  ]) {
    const rows = presentTranscript([assistant('a', [tool('1')]), boundary, assistant('b', [tool('2')])])
    assert.deepEqual(rows.map((row) => row.kind), ['activity', 'item', 'activity'])
    assert.equal(rows[1].item, boundary)
  }
})

test('group identity survives streaming additions and older history is prepended', () => {
  const first = assistant('first', [tool('stable')])
  const original = presentTranscript([first])[0]
  const updated = presentTranscript([
    { kind: 'user', id: 'earlier', blocks: [text('Earlier')] },
    first,
    assistant('next', [tool('new')]),
  ])[1]
  assert.equal(updated.id, original.id)
  assert.equal(updated.entries.length, 2)
  assert.equal(first.blocks.length, 1)
})

test('empty streaming messages have a pending activity; empty settled text disappears', () => {
  assert.equal(presentTranscript([assistant('pending', [], true)])[0].entries[0].streaming, true)
  assert.deepEqual(presentTranscript([assistant('empty', [text(' '), thinking('')])]), [])
})

test('summary counts include nested failures and distinguish missing results from success', () => {
  const result = { content: [], isError: false }
  const subtask = tool('parent', 'subagent', { result, children: [assistant('child', [tool('nested', 'shell', { result: { content: [], isError: true } })])] })
  const rows = presentTranscript([assistant('a', [tool('done', 'shell', { result }), tool('pending'), subtask])])
  const stats = activityStats(rows[0].entries)
  assert.equal(stats.tools, 4)
  assert.equal(stats.commands, 3)
  assert.equal(stats.failed, 1)
  assert.equal(stats.pending, 1)
})

test('stopped turns and completed subtasks cannot keep the summary running', () => {
  const entries = presentTranscript([assistant('pending', [tool('unfinished')])])[0].entries
  assert.equal(activityStats(entries, true).running, true)
  assert.equal(activityStats(entries, false).running, false)
  const completedSubtask = tool('parent', 'subagent', {
    result: { content: [], isError: false },
    children: [assistant('child', [tool('unfinished-child')], true)],
  })
  const nestedEntries = presentTranscript([assistant('a', [completedSubtask])])[0].entries
  assert.equal(activityStats(nestedEntries, true).running, false)
  assert.equal(activityStats(nestedEntries, true).pending, 1)
})

test('a collapsed group is labelled by the newest thought it holds', () => {
  const rows = presentTranscript([
    assistant('a', [thinking('Reading the build log'), tool('1'), thinking('The failure is the missing go.sum\n\nSo the fix is to commit it'), tool('2')]),
  ])
  assert.equal(latestThought(rows[0].entries), 'So the fix is to commit it')
  assert.equal(latestThought(presentTranscript([assistant('b', [tool('1')])])[0].entries), '')
})
