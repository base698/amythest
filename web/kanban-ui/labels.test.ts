import assert from 'node:assert/strict'
import test from 'node:test'
import {
  boardFilterURL,
  buildLabelOptions,
  filterCardsByLabels,
  filterSettingsFromSearch,
  type LabelledCard,
} from './labels.ts'

const card = (id: string, labels: string[]): LabelledCard => ({ id, labels })
const cards = [
  card('a', ['bug', 'alpha']),
  card('b', ['bug', 'beta']),
  card('c', ['feature', 'beta']),
  card('d', ['bug']),
]

test('orders label options by frequency and preserves selected labels beyond the cap', () => {
  assert.deepEqual(buildLabelOptions(cards, ['feature'], ['missing'], 2), [
    { label: 'bug', count: 3 },
    { label: 'beta', count: 2 },
    { label: 'feature', count: 1 },
    { label: 'missing', count: 0 },
  ])
})

test('filters with OR by default and supports AND plus exclusions', () => {
  assert.deepEqual(filterCardsByLabels(cards, ['alpha', 'beta']).map((item) => item.id), ['a', 'b', 'c'])
  assert.deepEqual(filterCardsByLabels(cards, ['bug', 'beta'], 'and').map((item) => item.id), ['b'])
  assert.deepEqual(filterCardsByLabels(cards, ['bug'], 'or', ['alpha']).map((item) => item.id), ['b', 'd'])
  assert.deepEqual(filterCardsByLabels(cards, [], 'or', ['bug']).map((item) => item.id), ['c'])
})

test('round-trips filter mode and exclusions in a linkable URL', () => {
  const url = boardFilterURL('work', ['bug', 'alpha'], 'and', ['beta'])
  assert.equal(url, '/kanban/work/bug/alpha?mode=and&exclude=beta')
  assert.deepEqual(filterSettingsFromSearch('?mode=and&exclude=beta&exclude=beta'), { mode: 'and', excluded: ['beta'] })
  assert.deepEqual(filterSettingsFromSearch('?mode=unknown&exclude='), { mode: 'or', excluded: [] })
})
