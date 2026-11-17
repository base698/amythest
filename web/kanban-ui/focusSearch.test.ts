import assert from 'node:assert/strict'
import test from 'node:test'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { FocusSearch, noteSearchPath, searchFocusCards, searchFocusTasks, searchNotes } from './FocusSearch.ts'
import type { FocusBoard, VaultTask } from './focusExperience.ts'

const boards: FocusBoard[] = [
  { name: 'alpha', displayName: 'Research', cards: [
    { id: 'a1', title: 'Measure response time', description: 'Latency under load', status: 'backlog', priority: 'p2', labels: ['performance'], comments: [], createdAt: '', updatedAt: '' },
  ] },
  { name: 'beta', displayName: 'Launch', cards: [
    { id: 'b1', title: 'Publish release', description: '', status: 'ready', priority: 'p1', labels: [], comments: [], createdAt: '', updatedAt: '' },
  ] },
]
const tasks: VaultTask[] = [{ slug: 'Projects/Launch', path: 'Projects/Launch.md', line: 7, text: 'Review release', tags: ['review'], status: 'open', priority: 3, version: 'a'.repeat(64) }]

test('Focus searches all loaded cards, including backlog, with board names and card metadata', () => {
  assert.deepEqual(searchFocusCards(boards, 'RESEARCH latency').map(hit => [hit.board.name, hit.card.id]), [['alpha', 'a1']])
  assert.deepEqual(searchFocusCards(boards, 'launch release').map(hit => hit.card.id), ['b1'])
  assert.equal(searchFocusCards(boards, 'research release').length, 0)
  assert.equal(searchFocusCards(boards, 'performance')[0].card, boards[0].cards[0])
})

test('Focus task search preserves completion coordinates and matches note names, text and tags', () => {
  assert.equal(searchFocusTasks(tasks, 'launch REVIEW')[0], tasks[0])
  assert.equal(searchFocusTasks(tasks, 'release')[0].version, tasks[0].version)
  assert.deepEqual(searchFocusTasks(tasks, 'unrelated'), [])
})

test('note result links preserve the mount and reject external targets', () => {
  const note = { slug: 'Projects/R&D plan', title: 'Plan' }
  assert.equal(noteSearchPath('/notes', note), '/notes/Projects/R%26D%20plan')
  assert.equal(noteSearchPath('/notes/', { ...note, url: 'Projects/Plan#details' }), '/notes/Projects/Plan#details')
  for (const url of ['https://example.com', '//example.com', 'javascript:alert(1)', '../private', '\\example.com']) {
    assert.equal(noteSearchPath('/notes/', { ...note, url }), '/notes/Projects/R%26D%20plan')
  }
})

test('note search sends an encoded same-origin query and propagates cancellation', async () => {
  const controller = new AbortController()
  const notes = [{ slug: 'Project', title: 'Project' }]
  assert.deepEqual(await searchNotes('/notes/', 'R&D #review', controller.signal, async (url, options) => {
    assert.equal(url, '/notes/api/search?kind=note&q=R%26D%20%23review')
    assert.equal(options?.credentials, 'same-origin')
    assert.equal(options?.signal, controller.signal)
    return new Response(JSON.stringify(notes))
  }), notes)
  await assert.rejects(searchNotes('/', 'x', controller.signal, async () => new Response('', { status: 401 })), /Sign in again/)
})

test('Focus search renders an accessible field, clear action, empty states and escaped results', () => {
  const props = { query: 'release', onChange: () => {}, notesBase: '/notes/', boards, tasks, busy: false, taskError: '', onOpen: () => {}, onOpenTask: () => {}, onCompleteTask: () => {} }
  const html = renderToStaticMarkup(createElement(FocusSearch, props))
  assert.match(html, /role="search"/)
  assert.match(html, /aria-label="Search cards, tasks and notes"/)
  assert.match(html, /Clear search/)
  assert.match(html, /Publish release/)
  assert.match(html, /Complete task: Review release/)
  const empty = renderToStaticMarkup(createElement(FocusSearch, { ...props, query: '<script>' }))
  assert.match(empty, /No matching cards/)
  assert.match(empty, /No matching open tasks/)
  assert.doesNotMatch(empty, /<script>/)
})


test('Focus uses the Notes search archive option without changing matching or ranking', async () => {
  const ranked = [{ slug: 'Second', title: 'Second', archived: true }, { slug: 'First', title: 'First' }]
  const result = await searchNotes('/notes/', 'release', new AbortController().signal, async (url) => {
    assert.equal(url, '/notes/api/search?kind=note&q=release&include_archived=1')
    return new Response(JSON.stringify(ranked))
  }, true)
  assert.deepEqual(result, ranked)
})
