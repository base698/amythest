import assert from 'node:assert/strict'
import test from 'node:test'

import { searchCards, type SearchableCard } from './cardSearch.ts'

const cards: SearchableCard[] = [
  {
    id: 'card-101',
    title: 'Review release checklist',
    description: 'Confirm the deployment steps',
    assignee: 'operator',
    labels: ['release', 'review'],
    milestone: 'Version 2',
  },
  {
    id: 'card-102',
    title: 'Investigate queue latency',
    description: 'Measure processing time under load',
    assignee: 'automation',
    labels: ['performance'],
  },
]

test('returns every card for an empty search', () => {
  assert.deepEqual(searchCards(cards, '  '), cards)
})

test('searches card text and metadata without changing card order', () => {
  assert.deepEqual(searchCards(cards, 'release review').map((card) => card.id), ['card-101'])
  assert.deepEqual(searchCards(cards, 'processing performance').map((card) => card.id), ['card-102'])
  assert.deepEqual(searchCards(cards, 'version 2').map((card) => card.id), ['card-101'])
  assert.deepEqual(searchCards(cards, 'CARD-102').map((card) => card.id), ['card-102'])
})

test('requires every search term to match some field', () => {
  assert.deepEqual(searchCards(cards, 'release latency'), [])
})
