import test from 'node:test'
import assert from 'node:assert/strict'
import { groupAttention, attentionStatus, type Attention } from './work-inbox.ts'
const item = (id: string, overrides: Partial<Attention> = {}): Attention => ({
 subject: `run:${id}`, reason: 'needs-review', version: '1', card_id: '', run_id: id, collector: 'laptop', title: 'Repair exports', session_id: 'session', project: 'Demo', agent: 'codex', finished_at: id, outcome: 'success', preview: '', output_count: 0, ...overrides,
})
test('repeated turns become one session with the newest result first', () => {
 const groups = groupAttention([item('1'), item('3'), item('2')])
 assert.equal(groups.length, 1)
 assert.equal(groups[0].latest.run_id, '3')
 assert.deepEqual(groups[0].items.map(i => i.run_id), ['3', '2', '1'])
 assert.equal(attentionStatus(groups[0].latest, 3), '3 turns finished')
})
test('distinct sessions, cards, collectors and errors remain visible', () => {
 const groups = groupAttention([item('1'), item('2', { session_id: 'another' }), item('3', { card_id: 'card' }), item('4', { collector: 'desktop' }), item('5', { outcome: 'failure' })])
 assert.equal(groups.length, 5)
 assert.equal(attentionStatus(item('5', { outcome: 'failure' })), '1 turn reported an error')
})
test('blocked and offline alerts sort ahead of completed turns', () => {
 const groups = groupAttention([item('9'), item('1', { reason: 'blocked', subject: 'slot:p' }), item('2', { reason: 'offline', subject: 'machine:m' })])
 assert.equal(groups.at(-1)?.latest.reason, 'needs-review')
 assert.equal(attentionStatus(groups[0].latest).includes('success'), false)
})
