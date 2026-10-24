import assert from 'node:assert/strict'
import test from 'node:test'

import { applyFavoriteMutation, shouldLeaveAfterSettingsSave } from './boardUpdates.ts'

const summary = (name: string, pinned = false) => ({
  name,
  displayName: name.toUpperCase(),
  description: '',
  icon: '',
  color: '',
  sortOrder: 0,
  pinned,
  archived: false,
  dispatchEnabled: false,
  counts: { ready: 2 },
})

const data = (name: string, pinned: boolean) => ({
  ...summary(name, pinned),
  version: 7,
  cards: [{ id: 'secret-card' }],
})

test('favorite response updates only its summary and cannot replace a board selected later', () => {
  const currentBoard = data('board-b', false)
  const result = applyFavoriteMutation([summary('board-a'), summary('board-b')], currentBoard, 'board-a', data('board-a', true))

  assert.equal(result.board, currentBoard)
  assert.equal(result.summaries[0].pinned, true)
  assert.equal(result.summaries[0].counts.ready, 2)
  assert.equal('cards' in result.summaries[0], false)
  assert.equal('version' in result.summaries[0], false)
})

test('settings save leaves only for an active to archived transition', () => {
  assert.equal(shouldLeaveAfterSettingsSave(false, true), true)
  assert.equal(shouldLeaveAfterSettingsSave(true, true), false)
  assert.equal(shouldLeaveAfterSettingsSave(true, false), false)
  assert.equal(shouldLeaveAfterSettingsSave(false, false), false)
})
