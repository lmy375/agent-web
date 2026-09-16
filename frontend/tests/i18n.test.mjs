import assert from 'node:assert/strict'
import test from 'node:test'
import { effortLabel, errorMessage, LocalizedError, messages, relativeTime, resolveLocale, translate } from '../src/i18n/core.ts'
import { applyEvent, emptyTranscript } from '../src/store/transcript.ts'

test('both dictionaries have matching keys and interpolation parameters', () => {
  assert.deepEqual(Object.keys(messages.zh).sort(), Object.keys(messages.en).sort())
  const placeholders = (value) => [...value.matchAll(/\{(\w+)\}/g)].map((match) => match[1]).sort()
  for (const key of Object.keys(messages.en)) {
    assert.ok(messages.zh[key].trim(), key)
    assert.deepEqual(placeholders(messages.zh[key]), placeholders(messages.en[key]), key)
  }
})

test('saved preference wins; invalid preferences use supported browser languages', () => {
  assert.equal(resolveLocale('en', ['zh-CN']), 'en')
  assert.equal(resolveLocale('zh', ['en-US']), 'zh')
  assert.equal(resolveLocale(null, ['zh-TW', 'en']), 'zh')
  assert.equal(resolveLocale(null, ['EN-gb', 'zh']), 'en')
  assert.equal(resolveLocale('invalid', ['fr-FR', 'zh-HK']), 'zh')
  assert.equal(resolveLocale(null, ['ja-JP']), 'en')
  assert.equal(resolveLocale(null, []), 'en')
})

test('interpolation preserves paths, model names and literal replacement characters', () => {
  assert.equal(translate('zh', 'newInProject', { project: '/work/$&/{project}' }), '在 /work/$&/{project} 中新建对话')
  assert.equal(translate('en', 'promptPlaceholder', { agent: 'Codex' }), 'Ask Codex to do something')
  assert.equal(translate('zh', 'imageLimit', { agent: 'Claude Code', count: 0 }), 'Claude Code 每条消息最多支持 0 张图片。')
})

test('existing errors translate at display time and retain diagnostic details', () => {
  const error = new LocalizedError('imageLimit', { agent: 'Codex', count: 4 })
  assert.equal(errorMessage('zh', error), 'Codex 每条消息最多支持 4 张图片。')
  assert.equal(errorMessage('en', error), 'Codex takes at most 4 images per message.')
  const apiError = Object.assign(new Error('/work/missing: no such directory'), { code: 'cwd_invalid' })
  assert.equal(errorMessage('zh', apiError), '工作目录无效: /work/missing: no such directory')
  assert.equal(errorMessage('zh', new Error('Failed to fetch')), '无法连接服务器')
  assert.equal(errorMessage('zh', new Error('vendor detail')), 'vendor detail')
})

test('relative dates use the selected locale instead of the browser locale', () => {
  const now = Date.UTC(2026, 8, 16, 12)
  assert.equal(relativeTime('zh', now, now), '刚刚')
  assert.equal(relativeTime('en', now, now), 'just now')
  assert.equal(relativeTime('zh', now - 5 * 60000, now), '5分钟前')
  assert.equal(relativeTime('en', now - 5 * 60000, now), '5 min. ago')
  assert.equal(relativeTime('zh', null, now), '')
  assert.notEqual(relativeTime('zh', now - 10 * 86400000, now), relativeTime('en', now - 10 * 86400000, now))
})

test('capability labels are translated without changing their protocol IDs', () => {
  assert.equal(effortLabel('zh', 'xhigh', 'Extra high'), '极高')
  assert.equal(effortLabel('en', 'high', 'High'), 'High')
  assert.equal(effortLabel('zh', 'future-mode', 'Vendor setting'), 'Vendor setting')
})

test('context history stores language-neutral data while external notices stay verbatim', () => {
  const compacted = applyEvent(emptyTranscript(), { type: 'context_boundary', tokens_before: 12000 })
  assert.equal(compacted.items[0].contextBoundary, true)
  assert.equal(compacted.items[0].tokensBefore, 12000)
  assert.equal(compacted.items[0].label, '')
  const notice = applyEvent(emptyTranscript(), { type: 'notice', message: 'Vendor notice' })
  assert.equal(notice.items[0].label, 'Vendor notice')
})
