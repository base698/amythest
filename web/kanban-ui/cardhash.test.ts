import test from 'node:test'
import assert from 'node:assert/strict'
import { cardIdFromHash } from './mount.ts'

test('reads the card id a move reference links to', () => {
  assert.equal(cardIdFromHash('#card-k_19fc6d904dc_f479e594cc'), 'k_19fc6d904dc_f479e594cc')
})

test('ignores fragments that are not card links', () => {
  for (const hash of ['', '#', '#card-', '#heading', '#card-a/b', '#card-a b']) {
    assert.equal(cardIdFromHash(hash), '', `hash ${hash}`)
  }
})
