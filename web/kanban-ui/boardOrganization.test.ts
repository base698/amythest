import assert from 'node:assert/strict'
import test from 'node:test'

import {
  boardFocusCandidate,
  boardLabel,
  organizeBoards,
  selectedFocusCard,
  suggestedFocusCard,
  type BoardMetadata,
  type FocusableCard,
  type RankedFocusCard,
} from './boardOrganization.ts'

const boards: BoardMetadata[] = [
  { name: 'later', displayName: 'Later', sortOrder: 20, pinned: true, archived: false },
  { name: 'archive', displayName: 'Old work', sortOrder: 1, pinned: true, archived: true },
  { name: 'ideas', displayName: 'Ideas', sortOrder: 5, pinned: false, archived: false },
  { name: 'home', displayName: 'Home base', sortOrder: 5, pinned: true, archived: false },
]

test('organizes active boards with pinned boards first and archives separately', () => {
  const result = organizeBoards(boards)

  assert.deepEqual(result.active.map((board) => board.name), ['home', 'later', 'ideas'])
  assert.deepEqual(result.pinned.map((board) => board.name), ['home', 'later'])
  assert.deepEqual(result.archived.map((board) => board.name), ['archive'])
})

test('uses explicit display names and falls back to a readable slug', () => {
  assert.equal(boardLabel({ name: 'release-planning', displayName: 'Launch room' }), 'Launch room')
  assert.equal(boardLabel({ name: 'release-planning', displayName: '  ' }), 'Release planning')
})

test('returns only an active card selected as the board focus', () => {
  const cards: FocusableCard[] = [{ id: 'k_one' }, { id: 'k_two' }]
  assert.equal(selectedFocusCard(cards, 'k_two')?.id, 'k_two')
  assert.equal(selectedFocusCard(cards, 'k_missing'), undefined)
  assert.equal(selectedFocusCard(cards, ''), undefined)
})

test('suggests executable work by priority then lifecycle and ignores blocked cards', () => {
  const cards: RankedFocusCard[] = [
    { id: 'blocked', status: 'in_progress', priority: 'p0', blocked: true },
    { id: 'urgent-ready', status: 'ready', priority: 'p0', dueDate: '2026-08-09' },
    { id: 'started', status: 'in_progress', priority: 'p2', dueDate: '2026-08-12' },
    { id: 'overdue-triage', status: 'triage', priority: 'p0', dueDate: '2026-08-08' },
  ]
  assert.equal(suggestedFocusCard(cards)?.id, 'urgent-ready')
})

test('priority outranks lifecycle state and backlog is not suggested', () => {
  const cards: RankedFocusCard[] = [
    { id: 'doing-low', status: 'in_progress', priority: 'p3', createdAt: '2026-08-01T00:00:00Z' },
    { id: 'ready-urgent', status: 'ready', priority: 'p0', createdAt: '2026-08-02T00:00:00Z' },
    { id: 'backlog-urgent', status: 'backlog', priority: 'p0', createdAt: '2026-07-01T00:00:00Z' },
  ]
  assert.equal(suggestedFocusCard(cards)?.id, 'ready-urgent')
})

test('explicit board focus wins over the fallback suggestion', () => {
  const cards: RankedFocusCard[] = [
    { id: 'started', status: 'in_progress', priority: 'p0' },
    { id: 'chosen', status: 'backlog', priority: 'p3', blocked: true },
  ]
  assert.deepEqual(boardFocusCandidate(cards, 'chosen'), { card: cards[1], explicit: true })
  assert.deepEqual(boardFocusCandidate(cards, 'missing'), { card: cards[0], explicit: false })
})
